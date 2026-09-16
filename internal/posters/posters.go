package posters

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/local/kodi-kronicles/internal/config"
)

type ImageFetcher interface {
	FetchImage(ctx context.Context, kodiPath string) ([]byte, string, error)
}

type Library struct {
	dir  string
	cfg  config.Config
	k    ImageFetcher
	http *http.Client
}

func New(cfg config.Config, k ImageFetcher) (*Library, error) {
	dir := filepath.Join(cfg.DataDir, "posters")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Library{
		dir:  dir,
		cfg:  cfg,
		k:    k,
		http: &http.Client{},
	}, nil
}

func (l *Library) Dir() string { return l.dir }

func HashKey(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])[:16]
}

func (l *Library) PathFor(key string) string {
	return filepath.Join(l.dir, key+".jpg")
}

func (l *Library) Exists(key string) bool {
	_, err := os.Stat(l.PathFor(key))
	return err == nil
}

func (l *Library) SaveFromKodi(ctx context.Context, key, kodiArt string) (string, error) {
	if key == "" || kodiArt == "" {
		return "", fmt.Errorf("missing art")
	}
	if l.Exists(key) {
		return key + ".jpg", nil
	}
	data, _, err := l.k.FetchImage(ctx, kodiArt)
	if err != nil {
		return "", err
	}
	return l.writeScaled(key, data)
}

func (l *Library) SaveFromURL(ctx context.Context, key, rawURL string) (string, error) {
	if key == "" || rawURL == "" {
		return "", fmt.Errorf("missing url")
	}
	if l.Exists(key) {
		return key + ".jpg", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "kodi-kronicles/1.0")
	resp, err := l.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("thumb http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 12<<20))
	if err != nil {
		return "", err
	}
	return l.writeScaled(key, data)
}

func (l *Library) writeScaled(key string, data []byte) (string, error) {
	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		// try as raw jpeg fallback
		img, _, err = image.Decode(bytes.NewReader(data))
		if err != nil {
			return "", err
		}
	}
	w, h := l.cfg.PosterMaxW, l.cfg.PosterMaxH
	if w <= 0 {
		w = 400
	}
	if h <= 0 {
		h = 600
	}
	img = imaging.Fit(img, w, h, imaging.Lanczos)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: l.cfg.PosterQuality}); err != nil {
		return "", err
	}
	tmp := l.PathFor(key) + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, l.PathFor(key)); err != nil {
		return "", err
	}
	return key + ".jpg", nil
}

func PickArt(kind string, art map[string]string, thumbnail, fanart string) string {
	order := []string{}
	switch kind {
	case "episode":
		order = []string{"season.poster", "tvshow.poster", "poster", "season.banner", "tvshow.banner"}
	case "movie":
		order = []string{"poster", "set.poster"}
	default:
		order = []string{"poster", "thumb"}
	}
	for _, k := range order {
		if v := strings.TrimSpace(art[k]); v != "" && !isBlankArt(v) {
			return v
		}
	}
	if !isBlankArt(thumbnail) {
		return thumbnail
	}
	if !isBlankArt(fanart) {
		return fanart
	}
	return ""
}

func isBlankArt(s string) bool {
	s = strings.ToLower(s)
	return s == "" || strings.Contains(s, "defaultvideo.png") || strings.Contains(s, "defaultfolder.png")
}
