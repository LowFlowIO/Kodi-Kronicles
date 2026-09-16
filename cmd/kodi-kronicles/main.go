package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/local/kodi-kronicles/internal/config"
	"github.com/local/kodi-kronicles/internal/kodi"
	"github.com/local/kodi-kronicles/internal/posters"
	"github.com/local/kodi-kronicles/internal/server"
	"github.com/local/kodi-kronicles/internal/store"
	"github.com/local/kodi-kronicles/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}

	st, err := store.Open(openDBPath(cfg.DataDir))
	if err != nil {
		log.Fatalf("sqlite: %v", err)
	}
	defer st.Close()

	client := kodi.New(cfg)
	lib, err := posters.New(cfg, client)
	if err != nil {
		log.Fatalf("posters: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	watcher := kodi.NewWatcher(cfg, client, st, lib)
	go watcher.Run(ctx)

	webFS, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatalf("web fs: %v", err)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(st, watcher, webFS, lib.Dir()).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("kodi kronicles listening on %s", cfg.ListenAddr)
		if cfg.KodiHost == "" {
			log.Printf("no kodi target yet — open the UI and pick a box")
		} else {
			log.Printf("kodi target %s  websocket %s", cfg.HTTPBase(), cfg.WSURL())
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutting down")
	case err := <-errCh:
		if err != nil {
			log.Printf("http: %v", err)
		}
	}
	stop()

	force := make(chan os.Signal, 1)
	signal.Notify(force, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-force
		log.Printf("second signal, exiting now")
		os.Exit(1)
	}()

	shutdownCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	_ = srv.Shutdown(shutdownCtx)
	log.Printf("bye")
}

func openDBPath(dir string) string {
	cur := filepath.Join(dir, "kronicles.db")
	legacy := filepath.Join(dir, "watchlog.db")
	if _, err := os.Stat(cur); err == nil {
		return cur
	}
	if _, err := os.Stat(legacy); err == nil {
		if err := os.Rename(legacy, cur); err == nil {
			log.Printf("renamed %s → %s", legacy, cur)
			return cur
		}
		return legacy
	}
	return cur
}
