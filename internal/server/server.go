package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/local/kodi-kronicles/internal/config"
	"github.com/local/kodi-kronicles/internal/kodi"
	"github.com/local/kodi-kronicles/internal/store"
)

type Server struct {
	store   *store.Store
	watcher *kodi.Watcher
	web     fs.FS
	poster  http.Handler
}

func New(st *store.Store, w *kodi.Watcher, web fs.FS, posterDir string) *Server {
	return &Server{
		store:   st,
		watcher: w,
		web:     web,
		poster:  http.StripPrefix("/posters/", http.FileServer(http.Dir(posterDir))),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/now", s.handleNow)
	mux.HandleFunc("/api/watches", s.handleWatches)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/series", s.handleSeries)
	mux.HandleFunc("/api/heatmap", s.handleHeatmap)
	mux.HandleFunc("/api/export.json", s.handleExport)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/discover", s.handleDiscover)
	mux.HandleFunc("/api/target", s.handleTarget)
	mux.HandleFunc("/api/boxes", s.handleBoxes)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		now := s.watcher.Current()
		writeJSON(w, 200, map[string]any{"ok": true, "kodi": now.Connected})
	})
	mux.Handle("/posters/", s.poster)
	mux.Handle("/", http.FileServer(http.FS(s.web)))
	return withLog(mux)
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
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	list, err := s.store.Heatmap(days)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
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
