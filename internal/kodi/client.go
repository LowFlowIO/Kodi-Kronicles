package kodi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/local/kodi-kronicles/internal/config"
)

type Client struct {
	mu    sync.RWMutex
	cfg   config.Config
	http  *http.Client
	reqID atomic.Int64
}

func (c *Client) Config() config.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *Client) SetConfig(cfg config.Config) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
}

func New(cfg config.Config) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			// PVR/IPTV metadata can stall Kodi's HTTP server for a bit.
			Timeout: 15 * time.Second,
		},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int64  `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := c.reqID.Add(1)
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: id})
	if err != nil {
		return err
	}
	cfg := c.Config()
	endpoint := cfg.HTTPBase() + "/jsonrpc"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.KodiUser != "" {
		req.SetBasicAuth(cfg.KodiUser, cfg.KodiPass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kodi http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("kodi http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var rr rpcResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		return fmt.Errorf("kodi decode: %w", err)
	}
	if rr.Error != nil {
		return fmt.Errorf("kodi rpc %s: %s", method, rr.Error.Message)
	}
	if out == nil {
		return nil
	}
	if len(rr.Result) == 0 || string(rr.Result) == "null" {
		return nil
	}
	return json.Unmarshal(rr.Result, out)
}

type ActivePlayer struct {
	PlayerID int    `json:"playerid"`
	Type     string `json:"type"`
}

type TimeHMS struct {
	Hours        int `json:"hours"`
	Minutes      int `json:"minutes"`
	Seconds      int `json:"seconds"`
	Milliseconds int `json:"milliseconds"`
}

func (t TimeHMS) SecondsTotal() int {
	return t.Hours*3600 + t.Minutes*60 + t.Seconds
}

type PlayerProps struct {
	Speed     int     `json:"speed"`
	Time      TimeHMS `json:"time"`
	TotalTime TimeHMS `json:"totaltime"`
	Live      bool    `json:"live"`
	Type      string  `json:"type"`
}

type ArtMap map[string]string

type Item struct {
	ID            int               `json:"id"`
	Type          string            `json:"type"`
	Label         string            `json:"label"`
	Title         string            `json:"title"`
	File          string            `json:"file"`
	Thumbnail     string            `json:"thumbnail"`
	Fanart        string            `json:"fanart"`
	Channel       string            `json:"channel"`
	ChannelNumber int               `json:"channelnumber"`
	ChannelType   string            `json:"channeltype"`
	ShowTitle     string            `json:"showtitle"`
	TVShowID      int               `json:"tvshowid"`
	Season        int               `json:"season"`
	Episode       int               `json:"episode"`
	Year          int               `json:"year"`
	Plot          string            `json:"plot"`
	Genre         json.RawMessage   `json:"genre"`
	Studio        json.RawMessage   `json:"studio"`
	Director      json.RawMessage   `json:"director"`
	Artist        json.RawMessage   `json:"artist"`
	Album         string            `json:"album"`
	Runtime       int               `json:"runtime"`
	Duration      int               `json:"duration"`
	Premiered     string            `json:"premiered"`
	FirstAired    string            `json:"firstaired"`
	DateAdded     string            `json:"dateadded"`
	PlayCount     int               `json:"playcount"`
	UniqueID      map[string]string `json:"uniqueid"`
	Art           ArtMap            `json:"art"`
	StreamDetails json.RawMessage   `json:"streamdetails"`
}

type Playing struct {
	Connected bool          `json:"connected"`
	Idle      bool          `json:"idle"`
	Player    *ActivePlayer `json:"player,omitempty"`
	Item      *Item         `json:"item,omitempty"`
	Props     *PlayerProps  `json:"props,omitempty"`
}

// Safe on movies, episodes, YouTube, and PVR/IPTV channels.
var itemPropsBase = []string{
	"title", "file", "thumbnail", "fanart", "art", "plot", "year", "genre",
	"runtime", "duration", "streamdetails",
}

// Library-only. Asking these on a live PVR item makes Kodi reject the whole GetItem.
var itemPropsLibrary = []string{
	"album", "artist", "season", "episode", "showtitle", "tvshowid",
	"studio", "director", "premiered", "firstaired", "dateadded",
	"playcount", "uniqueid",
}

var itemPropsPVR = []string{
	"channel", "channelnumber", "channeltype",
}

func (c *Client) enrichFromLibrary(ctx context.Context, item *Item) {
	if item == nil || item.File == "" {
		return
	}
	if item.Type == "movie" || item.Type == "episode" {
		if item.Thumbnail != "" || item.Title != "" && !strings.Contains(item.Title, ".") {
			return
		}
	}
	name := item.File
	if i := strings.LastIndex(strings.ReplaceAll(name, "\\", "/"), "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return
	}
	var movies struct {
		Movies []struct {
			Title     string `json:"title"`
			Year      int    `json:"year"`
			Thumbnail string `json:"thumbnail"`
			Fanart    string `json:"fanart"`
			Art       ArtMap `json:"art"`
			File      string `json:"file"`
		} `json:"movies"`
	}
	_ = c.Call(ctx, "VideoLibrary.GetMovies", map[string]any{
		"properties": []string{"title", "year", "thumbnail", "fanart", "art", "file"},
		"filter": map[string]any{
			"field": "filename", "operator": "is", "value": name,
		},
		"limits": map[string]int{"start": 0, "end": 3},
	}, &movies)
	if len(movies.Movies) == 0 {
		_ = c.Call(ctx, "VideoLibrary.GetMovies", map[string]any{
			"properties": []string{"title", "year", "thumbnail", "fanart", "art", "file"},
			"filter": map[string]any{
				"field": "filename", "operator": "contains", "value": strings.TrimSuffix(name, filepathExt(name)),
			},
			"limits": map[string]int{"start": 0, "end": 3},
		}, &movies)
	}
	if len(movies.Movies) > 0 {
		m := movies.Movies[0]
		if m.Title != "" {
			item.Title = m.Title
			item.Type = "movie"
		}
		if m.Year > 0 {
			item.Year = m.Year
		}
		if item.Thumbnail == "" {
			item.Thumbnail = m.Thumbnail
		}
		if item.Fanart == "" {
			item.Fanart = m.Fanart
		}
		if len(item.Art) == 0 {
			item.Art = m.Art
		}
		return
	}
	var eps struct {
		Episodes []struct {
			Title     string `json:"title"`
			ShowTitle string `json:"showtitle"`
			Season    int    `json:"season"`
			Episode   int    `json:"episode"`
			Thumbnail string `json:"thumbnail"`
			Fanart    string `json:"fanart"`
			Art       ArtMap `json:"art"`
		} `json:"episodes"`
	}
	_ = c.Call(ctx, "VideoLibrary.GetEpisodes", map[string]any{
		"properties": []string{"title", "showtitle", "season", "episode", "thumbnail", "fanart", "art"},
		"filter": map[string]any{
			"field": "filename", "operator": "is", "value": name,
		},
		"limits": map[string]int{"start": 0, "end": 3},
	}, &eps)
	if len(eps.Episodes) == 0 {
		return
	}
	e := eps.Episodes[0]
	item.Type = "episode"
	if e.Title != "" {
		item.Title = e.Title
	}
	item.ShowTitle = e.ShowTitle
	item.Season = e.Season
	item.Episode = e.Episode
	if item.Thumbnail == "" {
		item.Thumbnail = e.Thumbnail
	}
	if item.Fanart == "" {
		item.Fanart = e.Fanart
	}
	if len(item.Art) == 0 {
		item.Art = e.Art
	}
}

func filepathExt(name string) string {
	i := strings.LastIndex(name, ".")
	if i <= 0 {
		return ""
	}
	return name[i:]
}

func (c *Client) FriendlyName(ctx context.Context) string {
	var labels map[string]string
	if err := c.Call(ctx, "XBMC.GetInfoLabels", map[string]any{
		"labels": []string{"System.FriendlyName", "System.Hostname"},
	}, &labels); err == nil {
		if n := strings.TrimSpace(labels["System.FriendlyName"]); n != "" {
			return n
		}
		if n := strings.TrimSpace(labels["System.Hostname"]); n != "" {
			return n
		}
	}
	var props struct {
		Name string `json:"name"`
	}
	if err := c.Call(ctx, "Application.GetProperties", map[string]any{
		"properties": []string{"name"},
	}, &props); err == nil && strings.TrimSpace(props.Name) != "" {
		return strings.TrimSpace(props.Name)
	}
	return c.Config().KodiHost
}

func (c *Client) Ping(ctx context.Context) error {
	var pong string
	if err := c.Call(ctx, "JSONRPC.Ping", nil, &pong); err != nil {
		return err
	}
	return nil
}

func (c *Client) getItem(ctx context.Context, playerID int, props []string) (*Item, error) {
	var wrap struct {
		Item Item `json:"item"`
	}
	err := c.Call(ctx, "Player.GetItem", map[string]any{
		"playerid":   playerID,
		"properties": props,
	}, &wrap)
	if err != nil {
		return nil, err
	}
	return &wrap.Item, nil
}

func mergeItemProps(sets ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, set := range sets {
		for _, p := range set {
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func (c *Client) Snapshot(ctx context.Context) (*Playing, error) {
	p := &Playing{Connected: true}
	var players []ActivePlayer
	if err := c.Call(ctx, "Player.GetActivePlayers", nil, &players); err != nil {
		return nil, err
	}
	if len(players) == 0 {
		p.Idle = true
		return p, nil
	}
	// Prefer video player (PVR/IPTV still reports as video).
	pl := players[0]
	for _, cand := range players {
		if cand.Type == "video" {
			pl = cand
			break
		}
	}
	p.Player = &pl

	item, err := c.getItem(ctx, pl.PlayerID, mergeItemProps(itemPropsBase, itemPropsLibrary, itemPropsPVR))
	if err != nil {
		// Live TV / IPTV Simple often rejects library fields. Fall back.
		item, err = c.getItem(ctx, pl.PlayerID, mergeItemProps(itemPropsBase, itemPropsPVR))
	}
	if err != nil {
		item, err = c.getItem(ctx, pl.PlayerID, itemPropsBase)
	}
	if err != nil {
		item, err = c.getItem(ctx, pl.PlayerID, []string{"title", "file", "thumbnail"})
	}
	if err != nil {
		// Player is up; metadata is not. Stay online and still log a session.
		item = &Item{Type: "channel", Label: "Live TV", Title: "Live TV"}
	}
	p.Item = item
	c.enrichFromLibrary(ctx, p.Item)

	var props PlayerProps
	if err := c.Call(ctx, "Player.GetProperties", map[string]any{
		"playerid":   pl.PlayerID,
		"properties": []string{"speed", "time", "totaltime", "live", "type"},
	}, &props); err == nil {
		p.Props = &props
	} else {
		p.Props = &PlayerProps{Speed: 1, Live: looksLikeLiveStream(item)}
	}
	if !p.Props.Live && looksLikeLiveStream(item) && p.Props.TotalTime.SecondsTotal() == 0 {
		p.Props.Live = true
	}
	return p, nil
}

func looksLikeLiveStream(item *Item) bool {
	if item == nil {
		return false
	}
	if strings.EqualFold(item.Type, "channel") || item.Channel != "" || item.ChannelNumber > 0 {
		return true
	}
	file := strings.ToLower(item.File)
	switch {
	case strings.HasPrefix(file, "pvr://"):
		return true
	case strings.Contains(file, ".m3u8"), strings.Contains(file, ".m3u"):
		return true
	case strings.HasPrefix(file, "rtp://"), strings.HasPrefix(file, "rtsp://"),
		strings.HasPrefix(file, "udp://"), strings.HasPrefix(file, "rtmp://"):
		return true
	}
	return false
}

type PreparedDownload struct {
	Mode     string `json:"mode"`
	Protocol string `json:"protocol"`
	Details  struct {
		Path string `json:"path"`
	} `json:"details"`
}

func (c *Client) FetchImage(ctx context.Context, kodiPath string) ([]byte, string, error) {
	kodiPath = strings.TrimSpace(kodiPath)
	if kodiPath == "" {
		return nil, "", fmt.Errorf("empty image path")
	}
	var urlStr string
	var prepared PreparedDownload
	cfg := c.Config()
	if err := c.Call(ctx, "Files.PrepareDownload", map[string]any{"path": kodiPath}, &prepared); err == nil && prepared.Details.Path != "" {
		urlStr = strings.TrimRight(cfg.HTTPBase(), "/") + "/" + strings.TrimLeft(prepared.Details.Path, "/")
	} else {
		urlStr = strings.TrimRight(cfg.HTTPBase(), "/") + "/image/" + url.PathEscape(kodiPath)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, "", err
	}
	if cfg.KodiUser != "" {
		req.SetBasicAuth(cfg.KodiUser, cfg.KodiPass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("image fetch %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	return data, ct, err
}

func (c *Client) ListenNotifications(ctx context.Context, onEvent func(method string, params json.RawMessage)) error {
	cfg := c.Config()
	dialer := websocket.Dialer{HandshakeTimeout: 6 * time.Second}
	candidates := []string{cfg.WSURL()}
	if cfg.KodiWSPort != cfg.KodiHTTPPort {
		scheme := "ws"
		if cfg.KodiTLS {
			scheme = "wss"
		}
		candidates = append(candidates, fmt.Sprintf("%s://%s:%d/jsonrpc", scheme, cfg.KodiHost, cfg.KodiHTTPPort))
	}
	var conn *websocket.Conn
	var err error
	for _, wsURL := range candidates {
		hdr := http.Header{}
		if cfg.KodiUser != "" {
			req, _ := http.NewRequest(http.MethodGet, wsURL, nil)
			req.SetBasicAuth(cfg.KodiUser, cfg.KodiPass)
			hdr = req.Header
		}
		conn, _, err = dialer.DialContext(ctx, wsURL, hdr)
		if err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var rr rpcResponse
			if json.Unmarshal(raw, &rr) != nil {
				continue
			}
			if rr.Method != "" && onEvent != nil {
				onEvent(rr.Method, rr.Params)
			}
		}
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			return ctx.Err()
		case <-done:
			return fmt.Errorf("kodi websocket closed")
		case <-ticker.C:
			_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(2*time.Second))
		}
	}
}

func StringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return []string{s}
	}
	return nil
}
