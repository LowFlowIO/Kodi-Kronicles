package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	ListenAddr    string
	DataDir       string
	KodiHost      string
	KodiHTTPPort  int
	KodiWSPort    int
	KodiUser      string
	KodiPass      string
	KodiTLS       bool
	PollInterval  time.Duration
	MinWatchSecs  int
	MinIdleSecs   int
	PosterMaxW    int
	PosterMaxH    int
	PosterQuality int
}

type SavedBox struct {
	Host     string `json:"host"`
	Name     string `json:"name,omitempty"`
	HTTPPort int    `json:"http_port"`
	WSPort   int    `json:"ws_port,omitempty"`
	User     string `json:"user,omitempty"`
	Pass     string `json:"pass,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
	Active   bool   `json:"active,omitempty"`
}

type SavedState struct {
	Boxes []SavedBox `json:"boxes"`
}

func defaults() Config {
	return Config{
		ListenAddr:    ":8088",
		DataDir:       "./data",
		KodiHost:      "",
		KodiHTTPPort:  8080,
		KodiWSPort:    9090,
		KodiUser:      "",
		KodiPass:      "",
		KodiTLS:       false,
		PollInterval:  3 * time.Second,
		MinWatchSecs:  15,
		MinIdleSecs:   30,
		PosterMaxW:    400,
		PosterMaxH:    600,
		PosterQuality: 82,
	}
}

func Load() Config {
	cfg := defaults()
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "HTTP listen address for the web UI")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "SQLite + poster cache directory")
	fs.StringVar(&cfg.KodiHost, "kodi-host", cfg.KodiHost, "Kodi hostname or IP (empty = discover / last saved)")
	fs.IntVar(&cfg.KodiHTTPPort, "kodi-http-port", cfg.KodiHTTPPort, "Kodi JSON-RPC HTTP port")
	fs.IntVar(&cfg.KodiWSPort, "kodi-ws-port", cfg.KodiWSPort, "Kodi JSON-RPC WebSocket port")
	fs.StringVar(&cfg.KodiUser, "kodi-user", cfg.KodiUser, "Kodi HTTP basic-auth user")
	fs.StringVar(&cfg.KodiPass, "kodi-pass", cfg.KodiPass, "Kodi HTTP basic-auth password")
	fs.BoolVar(&cfg.KodiTLS, "kodi-tls", cfg.KodiTLS, "Talk to Kodi over https/wss")
	fs.DurationVar(&cfg.PollInterval, "poll-interval", cfg.PollInterval, "Now-playing poll interval")
	fs.IntVar(&cfg.MinWatchSecs, "min-watch-secs", cfg.MinWatchSecs, "Ignore plays shorter than this")
	fs.IntVar(&cfg.MinIdleSecs, "min-idle-secs", cfg.MinIdleSecs, "Ignore idle gaps shorter than this")
	fs.IntVar(&cfg.PosterMaxW, "poster-max-w", cfg.PosterMaxW, "Max stored poster width")
	fs.IntVar(&cfg.PosterMaxH, "poster-max-h", cfg.PosterMaxH, "Max stored poster height")
	fs.IntVar(&cfg.PosterQuality, "poster-quality", cfg.PosterQuality, "JPEG quality 1-100")
	_ = fs.Parse(os.Args[1:])

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	saved, _ := ReadSaved(cfg.DataDir)
	active := saved.Active()
	if !explicit["kodi-host"] && cfg.KodiHost == "" && active != nil {
		cfg.KodiHost = active.Host
		if !explicit["kodi-http-port"] && active.HTTPPort > 0 {
			cfg.KodiHTTPPort = active.HTTPPort
		}
		if !explicit["kodi-ws-port"] && active.WSPort > 0 {
			cfg.KodiWSPort = active.WSPort
		}
		if !explicit["kodi-tls"] {
			cfg.KodiTLS = active.TLS
		}
	}
	if cfg.KodiHost != "" {
		if box := saved.Find(cfg.KodiHost, cfg.KodiHTTPPort); box != nil {
			if !explicit["kodi-user"] && cfg.KodiUser == "" {
				cfg.KodiUser = box.User
			}
			if !explicit["kodi-pass"] && cfg.KodiPass == "" {
				cfg.KodiPass = box.Pass
			}
			if !explicit["kodi-http-port"] && box.HTTPPort > 0 && cfg.KodiHTTPPort == 8080 {
				cfg.KodiHTTPPort = box.HTTPPort
			}
		}
	}
	if explicit["kodi-host"] && cfg.KodiHost != "" {
		_ = Remember(cfg, "")
	}
	return cfg
}

func targetsPath(dataDir string) string {
	return filepath.Join(dataDir, "kodi-targets.json")
}

func ReadSaved(dataDir string) (SavedState, error) {
	var st SavedState
	raw, err := os.ReadFile(targetsPath(dataDir))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, err
	}
	return st, nil
}

func (s SavedState) Active() *SavedBox {
	for i := range s.Boxes {
		if s.Boxes[i].Active && s.Boxes[i].Host != "" {
			return &s.Boxes[i]
		}
	}
	if len(s.Boxes) == 1 && s.Boxes[0].Host != "" {
		return &s.Boxes[0]
	}
	return nil
}

func (s SavedState) Find(host string, port int) *SavedBox {
	host = strings.TrimSpace(host)
	for i := range s.Boxes {
		if s.Boxes[i].Host == host && (port == 0 || s.Boxes[i].HTTPPort == 0 || s.Boxes[i].HTTPPort == port) {
			return &s.Boxes[i]
		}
	}
	return nil
}

func Remember(cfg Config, name string) error {
	if strings.TrimSpace(cfg.KodiHost) == "" {
		return nil
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	st, _ := ReadSaved(cfg.DataDir)
	found := false
	for i := range st.Boxes {
		same := st.Boxes[i].Host == cfg.KodiHost && (st.Boxes[i].HTTPPort == 0 || st.Boxes[i].HTTPPort == cfg.KodiHTTPPort)
		if !same {
			st.Boxes[i].Active = false
			continue
		}
		found = true
		st.Boxes[i].Active = true
		st.Boxes[i].HTTPPort = cfg.KodiHTTPPort
		st.Boxes[i].WSPort = cfg.KodiWSPort
		st.Boxes[i].TLS = cfg.KodiTLS
		if name != "" {
			st.Boxes[i].Name = name
		}
		if cfg.KodiUser != "" {
			st.Boxes[i].User = cfg.KodiUser
		}
		if cfg.KodiPass != "" {
			st.Boxes[i].Pass = cfg.KodiPass
		}
	}
	if !found {
		for i := range st.Boxes {
			st.Boxes[i].Active = false
		}
		st.Boxes = append(st.Boxes, SavedBox{
			Host:     cfg.KodiHost,
			Name:     name,
			HTTPPort: cfg.KodiHTTPPort,
			WSPort:   cfg.KodiWSPort,
			User:     cfg.KodiUser,
			Pass:     cfg.KodiPass,
			TLS:      cfg.KodiTLS,
			Active:   true,
		})
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(targetsPath(cfg.DataDir), raw, 0o600)
}

func RememberDiscovered(dataDir string, host string, port int, name string) error {
	if host == "" {
		return nil
	}
	if port <= 0 {
		port = 8080
	}
	st, _ := ReadSaved(dataDir)
	for i := range st.Boxes {
		if st.Boxes[i].Host == host && (st.Boxes[i].HTTPPort == 0 || st.Boxes[i].HTTPPort == port) {
			if name != "" && st.Boxes[i].Name == "" {
				st.Boxes[i].Name = name
			}
			if st.Boxes[i].HTTPPort == 0 {
				st.Boxes[i].HTTPPort = port
			}
			raw, err := json.MarshalIndent(st, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(targetsPath(dataDir), raw, 0o600)
		}
	}
	st.Boxes = append(st.Boxes, SavedBox{
		Host:     host,
		Name:     name,
		HTTPPort: port,
	})
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(targetsPath(dataDir), raw, 0o600)
}

func (c Config) HTTPBase() string {
	scheme := "http"
	if c.KodiTLS {
		scheme = "https"
	}
	if c.KodiHost == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s:%d", scheme, c.KodiHost, c.KodiHTTPPort)
}

func (c Config) WSURL() string {
	scheme := "ws"
	if c.KodiTLS {
		scheme = "wss"
	}
	if c.KodiHost == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s:%d/jsonrpc", scheme, c.KodiHost, c.KodiWSPort)
}


