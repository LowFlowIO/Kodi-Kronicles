package server

import (
	"fmt"
	"bytes"
	"encoding/base64"
	"context"
	"archive/zip"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/local/kodi-kronicles/internal/config"
	"github.com/local/kodi-kronicles/internal/kodi"
	"github.com/local/kodi-kronicles/internal/store"
	"github.com/disintegration/imaging"
)

type Server struct {
	store     *store.Store
	watcher   *kodi.Watcher
	web       fs.FS
	poster    http.Handler
	posterDir string
}

func New(st *store.Store, w *kodi.Watcher, web fs.FS, posterDir string) *Server {
	return &Server{
		store:     st,
		watcher:   w,
		web:       web,
		poster:    http.StripPrefix("/posters/", http.FileServer(http.Dir(posterDir))),
		posterDir: posterDir,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/now", s.handleNow)
	mux.HandleFunc("/embed.svg", s.handleEmbedSVG)
	mux.HandleFunc("/api/watches", s.handleWatches)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/series", s.handleSeries)
	mux.HandleFunc("/api/heatmap", s.handleHeatmap)
	mux.HandleFunc("/api/export.json", s.handleExport)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/discover", s.handleDiscover)
	mux.HandleFunc("/api/target", s.handleTarget)
	mux.HandleFunc("/api/boxes", s.handleBoxes)
	mux.HandleFunc("/api/backup.zip", s.handleBackup)
	mux.HandleFunc("/api/ignore", s.handleIgnore)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		now := s.watcher.Current()
		writeJSON(w, 200, map[string]any{"ok": true, "kodi": now.Connected})
	})
	mux.Handle("/posters/", s.poster)
	mux.Handle("/", http.FileServer(http.FS(s.web)))
	return withCORS(withLog(mux))
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	cfg := s.watcher.ClientConfig()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	saved, _ := config.ReadSaved(cfg.DataDir)
	boxes := kodi.Discover(ctx, cfg.KodiHost, cfg.KodiHTTPPort, cfg.KodiUser, cfg.KodiPass)
	for _, b := range boxes {
		if b.Reachable {
			_ = config.RememberDiscovered(cfg.DataDir, b.Host, b.Port, b.Name)
		}
	}
	for _, sb := range saved.Boxes {
		if sb.Host == "" {
			continue
		}
		port := sb.HTTPPort
		if port == 0 {
			port = 8080
		}
		seen := false
		for i := range boxes {
			if boxes[i].Host == sb.Host && boxes[i].Port == port {
				if sb.Name != "" && boxes[i].Name == "Kodi" {
					boxes[i].Name = sb.Name
				}
				seen = true
				break
			}
		}
		if !seen {
			boxes = append(boxes, kodi.FoundBox{
				Host:   sb.Host,
				Port:   port,
				Name:   sb.Name,
				Source: "saved",
			})
		}
	}
	now := s.watcher.Current()
	if cfg.KodiHost != "" {
		found := false
		for i := range boxes {
			if boxes[i].Host == cfg.KodiHost && boxes[i].Port == cfg.KodiHTTPPort {
				boxes[i].Active = true
				if now.Connected {
					boxes[i].Reachable = true
				}
				found = true
			}
		}
		if !found {
			boxes = append([]kodi.FoundBox{{
				Host:      cfg.KodiHost,
				Port:      cfg.KodiHTTPPort,
				Name:      "Configured",
				Reachable: now.Connected,
				Source:    "configured",
				Active:    true,
			}}, boxes...)
		}
	}
	writeJSON(w, 200, map[string]any{
		"items":      boxes,
		"connected":  now.Connected,
		"target": map[string]any{
			"host": cfg.KodiHost,
			"port": cfg.KodiHTTPPort,
			"user": cfg.KodiUser,
			"tls":  cfg.KodiTLS,
		},
	})
}

func (s *Server) handleTarget(w http.ResponseWriter, r *http.Request) {
	cfg := s.watcher.ClientConfig()
	switch r.Method {
	case http.MethodGet, "":
		writeJSON(w, 200, map[string]any{
			"host": cfg.KodiHost,
			"port": cfg.KodiHTTPPort,
			"ws_port": cfg.KodiWSPort,
			"user": cfg.KodiUser,
			"tls":  cfg.KodiTLS,
		})
	case http.MethodPost:
		var body struct {
			Host   string `json:"host"`
			Port   int    `json:"port"`
			WSPort int    `json:"ws_port"`
			User   string `json:"user"`
			Pass   string `json:"pass"`
			TLS    bool   `json:"tls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		if strings.TrimSpace(body.Host) == "" {
			http.Error(w, "host required", 400)
			return
		}
		if body.Port == 0 {
			body.Port = 8080
		}
		s.watcher.SetTarget(body.Host, body.Port, body.WSPort, body.User, body.Pass, body.TLS)
		cfg = s.watcher.ClientConfig()
		writeJSON(w, 200, map[string]any{
			"ok":   true,
			"host": cfg.KodiHost,
			"port": cfg.KodiHTTPPort,
			"user": cfg.KodiUser,
			"tls":  cfg.KodiTLS,
		})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleBoxes(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListBoxes()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
}

func (s *Server) handleIgnore(w http.ResponseWriter, r *http.Request) {
	cfg := s.watcher.ClientConfig()
	switch r.Method {
	case http.MethodGet, "":
		writeJSON(w, 200, config.LoadIgnore(cfg.DataDir))
	case http.MethodPost:
		var body config.IgnoreRules
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		if err := config.SaveIgnore(cfg.DataDir, body); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, body)
	default:
		http.Error(w, "method", 405)
	}
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	cfg := s.watcher.ClientConfig()
	name := "kodi-kronicles-backup-" + time.Now().Format("2006-01-02") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	addFile := func(src, dest string) {
		f, err := os.Open(src)
		if err != nil {
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			return
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return
		}
		hdr.Name = dest
		hdr.Method = zip.Deflate
		out, err := zw.CreateHeader(hdr)
		if err != nil {
			return
		}
		_, _ = io.Copy(out, f)
	}
	addFile(filepath.Join(cfg.DataDir, "kronicles.db"), "kronicles.db")
	addFile(filepath.Join(cfg.DataDir, "watchlog.db"), "watchlog.db")
	addFile(filepath.Join(cfg.DataDir, "kodi-targets.json"), "kodi-targets.json")
	addFile(filepath.Join(cfg.DataDir, "ignore-rules.json"), "ignore-rules.json")
	_ = filepath.Walk(s.posterDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.posterDir, path)
		if err != nil {
			return nil
		}
		addFile(path, filepath.ToSlash(filepath.Join("posters", rel)))
		return nil
	})
}


func wrapSVGTitle(s string, max int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{""}
	}
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		next := w
		if cur != "" {
			next = cur + " " + w
		}
		if cur != "" && len(next) > max {
			lines = append(lines, cur)
			cur = w
		} else {
			cur = next
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > 2 {
		tail := lines[1]
		if len(tail) > max-1 {
			tail = strings.TrimRight(tail[:max-1], " ")
		}
		return []string{lines[0], tail + "…"}
	}
	return lines
}

func fmtClock(secs int) string {
	if secs < 0 {
		secs = 0
	}
	return strconv.Itoa(secs/60) + ":" + fmt.Sprintf("%02d", secs%60)
}

func (s *Server) handleEmbedSVG(w http.ResponseWriter, r *http.Request) {
	now := s.watcher.Current()
	title := "Nothing playing"
	status := "IDLE"
	meta := "Kodi Kronicles"
	kind := ""
	poster := ""
	progress := 0.0
	if !now.Connected {
		status = "OFFLINE"
		title = "Kodi offline"
	} else if now.Playing {
		status = "NOW PLAYING"
	} else if now.Paused {
		status = "PAUSED"
	}
	if now.Media != nil {
		m := now.Media
		kind = m.Kind
		if m.ShowTitle != "" && m.Title != "" && m.Kind == "episode" {
			title = m.ShowTitle + " · " + m.Title
		} else if m.Title != "" {
			title = m.Title
		}
		bits := []string{}
		if m.Kind != "" {
			bits = append(bits, m.Kind)
		}
		if now.BoxName != "" {
			bits = append(bits, now.BoxName)
		} else if now.BoxHost != "" {
			bits = append(bits, now.BoxHost)
		}
		if now.Playing || now.Paused {
			bits = append(bits, fmtClock(now.WatchedSeconds))
		}
		meta = strings.Join(bits, " · ")
		progress = now.ProgressPercent
		if m.PosterPath != "" {
			poster = s.posterDataURI(m.PosterPath)
		}
	}
	wide := kind == "youtube" || kind == "web" || kind == "iptv" || kind == "musicvideo" || kind == "plugin"
	artW, artH := 118, 170
	if wide {
		artW, artH = 220, 124
	}
	textX := artW + 28
	textW := 520 - textX - 16
	maxChars := textW * 10 / 96
	if maxChars < 16 {
		maxChars = 16
	}
	lines := wrapSVGTitle(title, maxChars)
	esc := func(s string) string {
		s = strings.ReplaceAll(s, "&", "&amp;")
		s = strings.ReplaceAll(s, "<", "&lt;")
		s = strings.ReplaceAll(s, ">", "&gt;")
		s = strings.ReplaceAll(s, `"`, "&quot;")
		return s
	}
	art := `<rect x="12" y="12" width="` + strconv.Itoa(artW) + `" height="` + strconv.Itoa(artH) + `" fill="#8a7f70"/>`
	if poster != "" {
		fit := "xMidYMid meet"
		if wide {
			fit = "xMidYMid slice"
		}
		art = `<image href="` + poster + `" x="12" y="12" width="` + strconv.Itoa(artW) + `" height="` + strconv.Itoa(artH) + `" preserveAspectRatio="` + fit + `"/>`
	}
	titleSVG := ""
	y := 68
	for _, line := range lines {
		titleSVG += `<text x="` + strconv.Itoa(textX) + `" y="` + strconv.Itoa(y) + `" font-family="ui-monospace, Menlo, Consolas, monospace" font-size="16" font-weight="700" fill="#2e2720">` + esc(line) + `</text>`
		y += 20
	}
	barY := y + 14
	barW := 520 - textX - 16
	if barW < 40 {
		barW = 40
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	fillW := int(float64(barW) * progress / 100)
	dot := "#6b5f52"
	if now.Playing {
		dot = "#5b8a82"
	}
	svg := `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="520" height="194" viewBox="0 0 520 194">
  <rect width="520" height="194" fill="#cbc4b3"/>
  <rect x="1" y="1" width="518" height="192" fill="none" stroke="#5a5045" stroke-width="2"/>
  ` + art + `
  <circle cx="` + strconv.Itoa(textX+6) + `" cy="36" r="5" fill="` + dot + `"/>
  <text x="` + strconv.Itoa(textX+18) + `" y="40" font-family="ui-monospace, Menlo, Consolas, monospace" font-size="11" fill="#6b5f52">` + esc(status) + `</text>
  ` + titleSVG + `
  <text x="` + strconv.Itoa(textX) + `" y="` + strconv.Itoa(y+2) + `" font-family="ui-monospace, Menlo, Consolas, monospace" font-size="12" fill="#6b5f52">` + esc(meta) + `</text>
  <rect x="` + strconv.Itoa(textX) + `" y="` + strconv.Itoa(barY) + `" width="` + strconv.Itoa(barW) + `" height="4" fill="#b7b09f"/>
  <rect x="` + strconv.Itoa(textX) + `" y="` + strconv.Itoa(barY) + `" width="` + strconv.Itoa(fillW) + `" height="4" fill="#5b8a82"/>
  <text x="` + strconv.Itoa(textX) + `" y="176" font-family="ui-monospace, Menlo, Consolas, monospace" font-size="11" fill="#6b5f52">Live from my living room Kodi instance.</text>
</svg>`
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("ETag", `"`+fmt.Sprintf("%x", len(svg)+len(title)+int(progress))+`"`)
	w.WriteHeader(200)
	_, _ = w.Write([]byte(svg))
}

func (s *Server) posterDataURI(name string) string {

	name = filepath.Base(name)
	raw, err := os.ReadFile(filepath.Join(s.posterDir, name))
	if err != nil {
		return ""
	}
	img, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if err != nil {
		return ""
	}
	img = imaging.Fit(img, 220, 170, imaging.Lanczos)
	var buf bytes.Buffer
	if imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(70)) != nil {
		return ""
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func (s *Server) handleNow(w http.ResponseWriter, r *http.Request) {
	now := s.watcher.Current()
	writeJSON(w, 200, now)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.SeriesRollup()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
}

func (s *Server) handleHeatmap(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("range")
	if kind == "" {
		kind = "all"
	}
	mode, list, err := s.store.HeatmapRange(kind)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list, "mode": mode})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	exp, err := s.store.Export()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	name := "kodi-kronicles-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	writeJSON(w, 200, exp)
}

func (s *Server) handleWatches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := s.store.ListWatches(store.ListFilter{
		Query:  q.Get("q"),
		Kind:   q.Get("kind"),
		Box:    q.Get("box"),
		From:   q.Get("from"),
		To:     q.Get("to"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{
		"items":  list,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "sse unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	id, ch := s.watcher.Subscribe()
	defer s.watcher.Unsubscribe(id)

	now := s.watcher.Current()
	payload, _ := json.Marshal(kodi.Event{Type: "hello", Connected: now.Connected, Now: &now})
	_, _ = w.Write([]byte("data: " + string(payload) + "\n\n"))
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/health" {
			log.Printf("%s %s", r.Method, r.URL.RequestURI())
		}
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
