package kodi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type FoundBox struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Name         string `json:"name,omitempty"`
	AuthRequired bool   `json:"auth_required"`
	Reachable    bool   `json:"reachable"`
	Source       string `json:"source"`
	Active       bool   `json:"active,omitempty"`
}

func Discover(ctx context.Context, currentHost string, currentPort int, user, pass string) []FoundBox {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
	}
	if currentPort <= 0 {
		currentPort = 8080
	}

	type hit struct {
		box FoundBox
	}
	outCh := make(chan hit, 32)
	var wg sync.WaitGroup
	emit := func(b FoundBox) {
		select {
		case outCh <- hit{box: b}:
		case <-ctx.Done():
		}
	}

	// 1. Configured target — long timeout, auth and no-auth.
	if currentHost != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b, ok := probeJSONRPC(ctx, currentHost, currentPort, user, pass, 2500*time.Millisecond); ok {
				b.Source = "configured"
				b.Active = true
				emit(b)
				return
			}
			if user != "" {
				if b, ok := probeJSONRPC(ctx, currentHost, currentPort, "", "", 1500*time.Millisecond); ok {
					b.Source = "configured"
					b.Active = true
					emit(b)
				}
			}
		}()
	}

	// 2. SSDP, then probe those IPs only (cheap).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, ip := range ssdpHosts(ctx) {
			ip := ip
			if currentHost != "" && ip == currentHost {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if b, ok := probeJSONRPC(ctx, ip, 8080, user, pass, 1500*time.Millisecond); ok {
					b.Source = "ssdp"
					emit(b)
					return
				}
				if b, ok := probeJSONRPC(ctx, ip, 8080, "", "", 800*time.Millisecond); ok {
					b.Source = "ssdp"
					emit(b)
				}
			}()
		}
	}()

	// 3. LAN /24 on :8080 only. Skip the configured host (already tried).
	sem := make(chan struct{}, 64)
	for _, ip := range localSubnetHosts() {
		if currentHost != "" && ip == currentHost {
			continue
		}
		ip := ip
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if b, ok := probeJSONRPC(ctx, ip, 8080, user, pass, 500*time.Millisecond); ok {
				b.Source = "subnet"
				emit(b)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(outCh)
	}()

	seen := map[string]FoundBox{}
	for h := range outCh {
		b := h.box
		key := fmt.Sprintf("%s:%d", b.Host, b.Port)
		if prev, ok := seen[key]; ok && prev.Source == "configured" {
			continue
		}
		if currentHost != "" && b.Host == currentHost && b.Port == currentPort {
			b.Active = true
		}
		seen[key] = b
	}
	out := make([]FoundBox, 0, len(seen))
	for _, b := range seen {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		if out[i].Host == out[j].Host {
			return out[i].Port < out[j].Port
		}
		return out[i].Host < out[j].Host
	})
	return out
}

func ssdpHosts(ctx context.Context) []string {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil
	}
	defer conn.Close()
	dst, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		return nil
	}
	queries := []string{
		"upnp:rootdevice",
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"urn:schemas-upnp-org:device:MediaServer:1",
	}
	for _, st := range queries {
		msg := "M-SEARCH * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			"MAN: \"ssdp:discover\"\r\n" +
			"MX: 1\r\n" +
			"ST: " + st + "\r\n\r\n"
		_, _ = conn.WriteTo([]byte(msg), dst)
	}
	deadline := time.Now().Add(1200 * time.Millisecond)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	_ = conn.SetReadDeadline(deadline)
	found := map[string]struct{}{}
	buf := make([]byte, 4096)
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		if ua, ok := src.(*net.UDPAddr); ok && ua.IP.To4() != nil {
			found[ua.IP.String()] = struct{}{}
		}
		body := string(buf[:n])
		low := strings.ToLower(body)
		if !strings.Contains(low, "kodi") && !strings.Contains(low, "xbmc") && !strings.Contains(low, "upnp") {
			continue
		}
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(strings.ToLower(line), "location:") {
				continue
			}
			loc := strings.TrimSpace(line[len("Location:"):])
			if host := hostFromURL(loc); host != "" {
				found[host] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(found))
	for ip := range found {
		out = append(out, ip)
	}
	return out
}

func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimPrefix(raw, "https://")
	if i := strings.IndexAny(raw, "/:"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func localSubnetHosts() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipn, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			// Skip obvious container/default docker nets so we scan the LAN, not 172.17.0.0/16.
			if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
				continue
			}
			base := ip.Mask(net.CIDRMask(24, 32))
			for i := 1; i < 255; i++ {
				host := net.IPv4(base[0], base[1], base[2], byte(i)).String()
				if _, ok := seen[host]; ok {
					continue
				}
				seen[host] = struct{}{}
				out = append(out, host)
			}
		}
	}
	return out
}

var probeClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		DisableKeepAlives:   true,
		DisableCompression:  true,
		MaxIdleConns:        2,
		TLSHandshakeTimeout: 800 * time.Millisecond,
		DialContext: (&net.Dialer{
			Timeout: 400 * time.Millisecond,
		}).DialContext,
	},
}

func probeJSONRPC(ctx context.Context, host string, port int, user, pass string, wait time.Duration) (FoundBox, bool) {
	if wait <= 0 {
		wait = 800 * time.Millisecond
	}
	pctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	body := []byte(`{"jsonrpc":"2.0","method":"JSONRPC.Ping","id":1}`)
	url := fmt.Sprintf("http://%s:%d/jsonrpc", host, port)
	req, err := http.NewRequestWithContext(pctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return FoundBox{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return FoundBox{}, false
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	box := FoundBox{Host: host, Port: port, Name: "Kodi", Reachable: true}
	if resp.StatusCode == http.StatusUnauthorized {
		box.AuthRequired = true
		return box, true
	}
	if resp.StatusCode >= 400 {
		return FoundBox{}, false
	}
	low := strings.ToLower(string(raw))
	if strings.Contains(low, "pong") || strings.Contains(low, "jsonrpc") || strings.Contains(low, "kodi") {
		return box, true
	}
	var rr struct {
		Result any `json:"result"`
	}
	if json.Unmarshal(raw, &rr) == nil && rr.Result != nil {
		return box, true
	}
	return FoundBox{}, false
}
