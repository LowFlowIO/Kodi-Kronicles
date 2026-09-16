package kodi

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/kodi-kronicles/internal/config"
	"github.com/local/kodi-kronicles/internal/posters"
	"github.com/local/kodi-kronicles/internal/store"
)

type Event struct {
	Type      string       `json:"type"`
	Connected bool         `json:"connected"`
	Now       *NowPlaying  `json:"now,omitempty"`
	Watch     *store.Watch `json:"watch,omitempty"`
}

type NowPlaying struct {
	Playing         bool         `json:"playing"`
	Paused          bool         `json:"paused"`
	Idle            bool         `json:"idle"`
	Connected       bool         `json:"connected"`
	PlayerType      string       `json:"player_type,omitempty"`
	Position        int          `json:"position_seconds"`
	Runtime         int          `json:"runtime_seconds"`
	Speed           int          `json:"speed"`
	WatchedSeconds  int          `json:"watched_seconds"`
	IdleSeconds     int          `json:"idle_seconds"`
	ProgressPercent float64      `json:"progress_percent"`
	StartedAt       string       `json:"started_at,omitempty"`
	BoxHost         string       `json:"box_host,omitempty"`
	BoxName         string       `json:"box_name,omitempty"`
	Media           *store.Media `json:"media,omitempty"`
}

type Watcher struct {
	cfg     config.Config
	client  *Client
	store   *store.Store
	posters *posters.Library

	mu       sync.Mutex
	session  *liveSession
	idle     *idleLive
	now      *NowPlaying
	connOK   bool
	misses   int
	lastSnap time.Time
	wsWarned bool
	boxName  string

	subsMu sync.Mutex
	subs   map[int]chan Event
	subID  int
}

type liveSession struct {
	watch    *store.Watch
	media    *store.Media
	key      string
	lastTick time.Time
	lastPos  int
	artTried bool
}

type idleLive struct {
	period   *store.IdlePeriod
	lastTick time.Time
}

func NewWatcher(cfg config.Config, client *Client, st *store.Store, p *posters.Library) *Watcher {
	return &Watcher{
		cfg:     cfg,
		client:  client,
		store:   st,
		posters: p,
		now:     &NowPlaying{Playing: false},
		subs:    map[int]chan Event{},
	}
}

func (w *Watcher) boxInfo() (host, name string, port int) {
	cfg := w.client.Config()
	host = cfg.KodiHost
	port = cfg.KodiHTTPPort
	name = w.boxName
	if name == "" {
		name = host
	}
	return host, name, port
}

func (w *Watcher) ClientConfig() config.Config {
	return w.client.Config()
}

func (w *Watcher) SetTarget(host string, httpPort, wsPort int, user, pass string, tls bool) {
	cfg := w.client.Config()
	if host != "" {
		cfg.KodiHost = host
	}
	if httpPort > 0 {
		cfg.KodiHTTPPort = httpPort
	}
	if wsPort > 0 {
		cfg.KodiWSPort = wsPort
	} else if httpPort > 0 {
		cfg.KodiWSPort = 9090
	}
	if user != "" {
		cfg.KodiUser = user
	}
	if pass != "" {
		cfg.KodiPass = pass
	}
	cfg.KodiTLS = tls
	w.client.SetConfig(cfg)
	if err := config.Remember(cfg, w.boxName); err != nil {
		log.Printf("save target: %v", err)
	}
	w.mu.Lock()
	w.cfg = cfg
	w.misses = 3
	w.connOK = false
	w.boxName = ""
	w.mu.Unlock()
	log.Printf("kodi target set to %s", cfg.HTTPBase())
}

func (w *Watcher) Current() NowPlaying {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.now == nil {
		return NowPlaying{Connected: w.connOK}
	}
	cp := *w.now
	return cp
}

func (w *Watcher) Subscribe() (int, <-chan Event) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	w.subID++
	ch := make(chan Event, 8)
	w.subs[w.subID] = ch
	return w.subID, ch
}

func (w *Watcher) Unsubscribe(id int) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	if ch, ok := w.subs[id]; ok {
		delete(w.subs, id)
		close(ch)
	}
}

func (w *Watcher) publish(ev Event) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	for id, ch := range w.subs {
		select {
		case ch <- ev:
		default:
			// drop if subscriber is slow
			_ = id
		}
	}
}

func (w *Watcher) Run(ctx context.Context) {
	go w.wsLoop(ctx)
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	w.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			w.finalize("shutdown")
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Watcher) wsLoop(ctx context.Context) {
	backoff := time.Second
	for {
		if w.client.Config().KodiHost == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if ctx.Err() != nil {
			return
		}
		err := w.client.ListenNotifications(ctx, func(method string, params json.RawMessage) {
			switch method {
			case "Player.OnPlay", "Player.OnAVStart", "Player.OnResume",
				"Player.OnPause", "Player.OnStop", "Player.OnSeek", "Player.OnSpeedChanged":
				w.poll(ctx)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil && !w.wsWarned {
			w.wsWarned = true
			log.Printf("kodi websocket unavailable (%v); using HTTP polling only", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}
}

func (w *Watcher) poll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if w.client.Config().KodiHost == "" {
		w.mu.Lock()
		w.connOK = false
		w.now = &NowPlaying{Connected: false}
		w.mu.Unlock()
		return
	}
	snap, err := w.client.Snapshot(ctx)
	var learnedName string
	if err == nil && w.boxName == "" {
		learnedName = w.client.FriendlyName(ctx)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.misses++
		if w.misses < 3 {
			log.Printf("kodi poll miss %d/3: %v", w.misses, err)
			return
		}
		if w.connOK {
			log.Printf("kodi unreachable: %v", err)
		}
		w.connOK = false
		w.finishIdleLocked("offline")
		if w.now != nil {
			w.now.Connected = false
			w.now.Idle = false
		}
		w.publish(Event{Type: "status", Connected: false, Now: w.now})
		return
	}
	w.misses = 0
	if !w.connOK {
		log.Printf("kodi connected at %s", w.client.Config().HTTPBase())
		if learnedName != "" {
			w.boxName = learnedName
			log.Printf("kodi name %s", learnedName)
			_ = config.Remember(w.client.Config(), learnedName)
		}
	} else if learnedName != "" {
		w.boxName = learnedName
	}
	w.connOK = true
	w.lastSnap = time.Now()
	if snap.Idle || snap.Item == nil {
		if w.session != nil {
			w.finishLocked("stop")
		}
		w.tickIdleLocked()
		idleSecs := 0
		started := ""
		if w.idle != nil {
			idleSecs = w.idle.period.DurationSeconds
			started = w.idle.period.StartedAt.Format(time.RFC3339)
		}
		host, name, _ := w.boxInfo()
		w.now = &NowPlaying{Connected: true, Playing: false, Idle: true, IdleSeconds: idleSecs, StartedAt: started, BoxHost: host, BoxName: name}
		w.publish(Event{Type: "idle", Connected: true, Now: w.now})
		return
	}
	w.finishIdleLocked("playing")

	art := posters.PickArt(guessKind(snap.Item), snap.Item.Art, snap.Item.Thumbnail, snap.Item.Fanart)
	cls := Classify(snap.Item, art)
	runtime := 0
	pos := 0
	speed := 0
	live := false
	if snap.Props != nil {
		runtime = snap.Props.TotalTime.SecondsTotal()
		pos = snap.Props.Time.SecondsTotal()
		speed = snap.Props.Speed
		live = snap.Props.Live
	}
	if !live {
		live = looksLikeLiveStream(snap.Item) && runtime == 0
	}
	if runtime == 0 && snap.Item.Runtime > 0 {
		runtime = snap.Item.Runtime
	}
	if runtime == 0 && snap.Item.Duration > 0 {
		runtime = snap.Item.Duration
	}

	if w.session == nil || w.session.key != cls.UniqueKey {
		if w.session != nil {
			w.finishLocked("changed")
		}
		media := classifiedToMedia(cls)
		if err := w.store.UpsertMedia(media); err != nil {
			log.Printf("upsert media: %v", err)
			return
		}
		playerType := "video"
		if snap.Player != nil {
			playerType = snap.Player.Type
		}
		host, name, port := w.boxInfo()
		watch, err := w.store.StartWatch(media.ID, playerType, runtime, host, name, port)
		if err != nil {
			log.Printf("start watch: %v", err)
			return
		}
		watch.PositionSeconds = pos
		watch.RuntimeSeconds = runtime
		w.session = &liveSession{
			watch:    watch,
			media:    media,
			key:      cls.UniqueKey,
			lastTick: time.Now(),
			lastPos:  0,
		}
		log.Printf("now playing: %s [%s]", store.DisplayTitle(media), cls.Kind)
		go w.ensurePoster(cls, media)
	}

	sess := w.session
	now := time.Now()
	wall := int(now.Sub(sess.lastTick).Seconds())
	if wall < 0 {
		wall = 0
	}
	// IPTV/DASH often reports 0:00 on stop or has no playhead at all.
	if pos == 0 && sess.lastPos > 0 && !live {
		pos = sess.lastPos
	}
	posDelta := pos - sess.lastPos
	switch {
	case live && speed != 0 && wall > 0 && wall < 30:
		sess.watch.WatchedSeconds += wall
	case posDelta > 0 && posDelta <= wall+12:
		sess.watch.WatchedSeconds += posDelta
	case posDelta > 0 && posDelta <= 15 && sess.lastPos == 0:
		sess.watch.WatchedSeconds += posDelta
	case speed != 0 && wall > 0 && wall < 30:
		sess.watch.WatchedSeconds += wall
	}
	sess.lastTick = now
	if pos > 0 {
		sess.lastPos = pos
		sess.watch.PositionSeconds = pos
	}
	if live && sess.watch.PositionSeconds < sess.watch.WatchedSeconds {
		sess.watch.PositionSeconds = sess.watch.WatchedSeconds
	}
	sess.watch.RuntimeSeconds = runtime
	if runtime > 0 && sess.watch.WatchedSeconds > runtime {
		sess.watch.WatchedSeconds = runtime
	}
	if runtime > 0 && sess.watch.PositionSeconds > 0 {
		sess.watch.ProgressPercent = float64(sess.watch.PositionSeconds) / float64(runtime) * 100
		if sess.watch.ProgressPercent > 100 {
			sess.watch.ProgressPercent = 100
		}
	}
	_ = w.store.UpdateWatch(sess.watch)

	np := &NowPlaying{
		Playing:         speed != 0,
		Paused:          speed == 0,
		Connected:       true,
		PlayerType:      sess.watch.PlayerType,
		Position:        pos,
		Runtime:         runtime,
		Speed:           speed,
		WatchedSeconds:  sess.watch.WatchedSeconds,
		ProgressPercent: sess.watch.ProgressPercent,
		StartedAt:       sess.watch.StartedAt.Format(time.RFC3339),
		BoxHost:         sess.watch.BoxHost,
		BoxName:         sess.watch.BoxName,
		Media:           sess.media,
	}
	w.now = np
	w.publish(Event{Type: "now", Connected: true, Now: np, Watch: sess.watch})
}

func (w *Watcher) tickIdleLocked() {
	now := time.Now()
	if w.idle == nil {
		host, name, port := w.boxInfo()
		p, err := w.store.StartIdle(host, name, port)
		if err != nil {
			log.Printf("start idle: %v", err)
			return
		}
		w.idle = &idleLive{period: p, lastTick: now}
		log.Printf("kodi idle started")
		return
	}
	delta := int(now.Sub(w.idle.lastTick).Seconds())
	if delta > 0 && delta < 30 {
		w.idle.period.DurationSeconds += delta
	}
	w.idle.lastTick = now
	_ = w.store.UpdateIdle(w.idle.period)
}

func (w *Watcher) finishIdleLocked(reason string) {
	if w.idle == nil {
		return
	}
	p := w.idle.period
	w.idle = nil
	if p.DurationSeconds < w.cfg.MinIdleSecs {
		if err := w.store.DeleteIdle(p.ID); err != nil {
			log.Printf("drop short idle: %v", err)
		}
		return
	}
	if err := w.store.FinishIdle(p); err != nil {
		log.Printf("finish idle: %v", err)
		return
	}
	log.Printf("logged idle: %ds (%s)", p.DurationSeconds, reason)
	w.publish(Event{Type: "logged", Connected: w.connOK})
}

func (w *Watcher) finalize(reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.finishLocked(reason)
	w.finishIdleLocked(reason)
}

func (w *Watcher) finishLocked(reason string) {
	if w.session == nil {
		return
	}
	sess := w.session
	w.session = nil
	if sess.watch.PositionSeconds == 0 && sess.lastPos > 0 {
		sess.watch.PositionSeconds = sess.lastPos
	}
	if sess.watch.PositionSeconds == 0 && sess.watch.WatchedSeconds > 0 {
		sess.watch.PositionSeconds = sess.watch.WatchedSeconds
	}
	if gap := sess.watch.PositionSeconds - sess.watch.WatchedSeconds; gap > 0 && gap <= 20 {
		sess.watch.WatchedSeconds = sess.watch.PositionSeconds
	}
	if runtime := sess.watch.RuntimeSeconds; runtime > 0 && sess.watch.WatchedSeconds > runtime {
		sess.watch.WatchedSeconds = runtime
	}
	if sess.watch.WatchedSeconds < w.cfg.MinWatchSecs {
		if err := w.store.DeleteWatch(sess.watch.ID); err != nil {
			log.Printf("drop short watch: %v", err)
		} else {
			log.Printf("ignored short play (%ds) of %s", sess.watch.WatchedSeconds, store.DisplayTitle(sess.media))
		}
		return
	}
	if err := w.store.FinishWatch(sess.watch); err != nil {
		log.Printf("finish watch: %v", err)
		return
	}
	log.Printf("logged watch: %s for %ds (%s)", store.DisplayTitle(sess.media), sess.watch.WatchedSeconds, reason)
	w.publish(Event{Type: "logged", Connected: w.connOK, Watch: sess.watch})
}

func (w *Watcher) ensurePoster(cls Classified, media *store.Media) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	key := posters.HashKey(cls.UniqueKey)
	if cls.Kind == "episode" && media.ShowTitle != "" && media.Season != nil {
		key = posters.HashKey("season", strings.ToLower(media.ShowTitle), strconv.Itoa(*media.Season))
	}
	if w.posters.Exists(key) {
		_ = w.store.SetPoster(media.ID, key+".jpg")
		media.PosterPath = key + ".jpg"
		return
	}
	var filename string
	var err error
	if cls.Kind == "youtube" && cls.YouTubeID != "" {
		filename, err = w.posters.SaveFromURL(ctx, key, "https://i.ytimg.com/vi/"+cls.YouTubeID+"/hqdefault.jpg")
		if err != nil {
			filename, err = w.posters.SaveFromURL(ctx, key, "https://i.ytimg.com/vi/"+cls.YouTubeID+"/mqdefault.jpg")
		}
	}
	if filename == "" && cls.ArtPath != "" {
		filename, err = w.posters.SaveFromKodi(ctx, key, cls.ArtPath)
	}
	if err != nil {
		log.Printf("poster for %s: %v", store.DisplayTitle(media), err)
		return
	}
	if filename == "" {
		return
	}
	if err := w.store.SetPoster(media.ID, filename); err != nil {
		log.Printf("set poster: %v", err)
		return
	}
	media.PosterPath = filename
	w.mu.Lock()
	if w.now != nil && w.now.Media != nil && w.now.Media.ID == media.ID {
		w.now.Media.PosterPath = filename
	}
	np := w.now
	w.mu.Unlock()
	w.publish(Event{Type: "poster", Connected: true, Now: np})
}

func classifiedToMedia(cls Classified) *store.Media {
	return &store.Media{
		Kind:      cls.Kind,
		Title:     cls.Title,
		ShowTitle: cls.ShowTitle,
		Season:    cls.Season,
		Episode:   cls.Episode,
		Year:      cls.Year,
		File:      cls.File,
		SourceURL: cls.SourceURL,
		UniqueKey: cls.UniqueKey,
		Extra:     ExtraJSON(cls),
	}
}

func guessKind(item *Item) string {
	if item.Type == "episode" || item.ShowTitle != "" {
		return "episode"
	}
	if item.Type == "movie" {
		return "movie"
	}
	return item.Type
}
