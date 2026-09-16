package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Media struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	ShowTitle  string          `json:"show_title,omitempty"`
	Season     *int            `json:"season,omitempty"`
	Episode    *int            `json:"episode,omitempty"`
	Year       *int            `json:"year,omitempty"`
	File       string          `json:"file,omitempty"`
	SourceURL  string          `json:"source_url,omitempty"`
	UniqueKey  string          `json:"unique_key"`
	PosterPath string          `json:"poster_path,omitempty"`
	Extra      json.RawMessage `json:"extra,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Watch struct {
	ID              int64      `json:"id"`
	MediaID         int64      `json:"media_id"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	WatchedSeconds  int        `json:"watched_seconds"`
	PositionSeconds int        `json:"position_seconds"`
	RuntimeSeconds  int        `json:"runtime_seconds"`
	ProgressPercent float64    `json:"progress_percent"`
	PlayerType      string     `json:"player_type"`
	Completed       bool       `json:"completed"`
	Active          bool       `json:"active"`
	BoxHost         string     `json:"box_host,omitempty"`
	BoxName         string     `json:"box_name,omitempty"`
	BoxPort         int        `json:"box_port,omitempty"`
	Media           *Media     `json:"media,omitempty"`
}

type IdlePeriod struct {
	ID              int64      `json:"id"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	DurationSeconds int        `json:"duration_seconds"`
	Active          bool       `json:"active"`
	BoxHost         string     `json:"box_host,omitempty"`
	BoxName         string     `json:"box_name,omitempty"`
	BoxPort         int        `json:"box_port,omitempty"`
}

type Box struct {
	Host  string `json:"host"`
	Name  string `json:"name"`
	Port  int    `json:"port"`
	Count int    `json:"count"`
}

type Stats struct {
	TotalWatches      int     `json:"total_watches"`
	TotalHours        float64 `json:"total_hours"`
	IdleHours         float64 `json:"idle_hours"`
	Movies            int     `json:"movies"`
	Episodes          int     `json:"episodes"`
	Plugins           int     `json:"plugins"`
	UniqueTitles      int     `json:"unique_titles"`
	ThisWeekHours     float64 `json:"this_week_hours"`
	ThisWeekIdleHours float64 `json:"this_week_idle_hours"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS media (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  show_title TEXT NOT NULL DEFAULT '',
  season INTEGER,
  episode INTEGER,
  year INTEGER,
  file TEXT NOT NULL DEFAULT '',
  source_url TEXT NOT NULL DEFAULT '',
  unique_key TEXT NOT NULL UNIQUE,
  poster_path TEXT NOT NULL DEFAULT '',
  extra_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS watches (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  media_id INTEGER NOT NULL REFERENCES media(id),
  started_at TEXT NOT NULL,
  ended_at TEXT,
  watched_seconds INTEGER NOT NULL DEFAULT 0,
  position_seconds INTEGER NOT NULL DEFAULT 0,
  runtime_seconds INTEGER NOT NULL DEFAULT 0,
  progress_percent REAL NOT NULL DEFAULT 0,
  player_type TEXT NOT NULL DEFAULT 'video',
  completed INTEGER NOT NULL DEFAULT 0,
  active INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_watches_started ON watches(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_watches_media ON watches(media_id);
CREATE INDEX IF NOT EXISTS idx_media_kind ON media(kind);
CREATE INDEX IF NOT EXISTS idx_media_title ON media(title);
CREATE TABLE IF NOT EXISTS idle_periods (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT NOT NULL,
  ended_at TEXT,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  active INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_idle_started ON idle_periods(started_at DESC);
`)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`ALTER TABLE watches ADD COLUMN box_host TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE watches ADD COLUMN box_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE watches ADD COLUMN box_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE idle_periods ADD COLUMN box_host TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE idle_periods ADD COLUMN box_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE idle_periods ADD COLUMN box_port INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_watches_box ON watches(box_host)`)
	return nil
}

func (s *Store) UpsertMedia(m *Media) error {
	if m.Extra == nil {
		m.Extra = json.RawMessage(`{}`)
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	var season, episode, year any
	if m.Season != nil {
		season = *m.Season
	}
	if m.Episode != nil {
		episode = *m.Episode
	}
	if m.Year != nil {
		year = *m.Year
	}
	res, err := s.db.Exec(`
INSERT INTO media (kind, title, show_title, season, episode, year, file, source_url, unique_key, poster_path, extra_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(unique_key) DO UPDATE SET
  kind=excluded.kind,
  title=excluded.title,
  show_title=excluded.show_title,
  season=excluded.season,
  episode=excluded.episode,
  year=excluded.year,
  file=CASE WHEN excluded.file != '' THEN excluded.file ELSE media.file END,
  source_url=CASE WHEN excluded.source_url != '' THEN excluded.source_url ELSE media.source_url END,
  extra_json=excluded.extra_json
`, m.Kind, m.Title, m.ShowTitle, season, episode, year, m.File, m.SourceURL, m.UniqueKey, m.PosterPath, string(m.Extra), m.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if m.ID == 0 {
		id, err := res.LastInsertId()
		if err == nil && id > 0 {
			// LastInsertId is 0 on conflict for sqlite sometimes; look up.
			m.ID = id
		}
	}
	row := s.db.QueryRow(`SELECT id, poster_path, created_at FROM media WHERE unique_key = ?`, m.UniqueKey)
	var created string
	if err := row.Scan(&m.ID, &m.PosterPath, &created); err != nil {
		return err
	}
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		m.CreatedAt = t
	}
	return nil
}

func (s *Store) SetPoster(mediaID int64, path string) error {
	_, err := s.db.Exec(`UPDATE media SET poster_path = ? WHERE id = ?`, path, mediaID)
	return err
}

func (s *Store) StartWatch(mediaID int64, playerType string, runtime int, boxHost, boxName string, boxPort int) (*Watch, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`
INSERT INTO watches (media_id, started_at, watched_seconds, position_seconds, runtime_seconds, player_type, active, box_host, box_name, box_port)
VALUES (?, ?, 0, 0, ?, ?, 1, ?, ?, ?)
`, mediaID, now.Format(time.RFC3339Nano), runtime, playerType, boxHost, boxName, boxPort)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Watch{
		ID:             id,
		MediaID:        mediaID,
		StartedAt:      now,
		RuntimeSeconds: runtime,
		PlayerType:     playerType,
		Active:         true,
		BoxHost:        boxHost,
		BoxName:        boxName,
		BoxPort:        boxPort,
	}, nil
}

func (s *Store) UpdateWatch(w *Watch) error {
	var ended any
	if w.EndedAt != nil {
		ended = w.EndedAt.UTC().Format(time.RFC3339Nano)
	}
	completed := 0
	if w.Completed {
		completed = 1
	}
	active := 0
	if w.Active {
		active = 1
	}
	_, err := s.db.Exec(`
UPDATE watches SET
  ended_at = ?,
  watched_seconds = ?,
  position_seconds = ?,
  runtime_seconds = ?,
  progress_percent = ?,
  completed = ?,
  active = ?
WHERE id = ?
`, ended, w.WatchedSeconds, w.PositionSeconds, w.RuntimeSeconds, w.ProgressPercent, completed, active, w.ID)
	return err
}

func (s *Store) FinishWatch(w *Watch) error {
	now := time.Now().UTC()
	w.EndedAt = &now
	w.Active = false
	if w.RuntimeSeconds > 0 && w.ProgressPercent >= 90 {
		w.Completed = true
	}
	return s.UpdateWatch(w)
}

func (s *Store) DeleteWatch(id int64) error {
	_, err := s.db.Exec(`DELETE FROM watches WHERE id = ?`, id)
	return err
}

func (s *Store) StartIdle(boxHost, boxName string, boxPort int) (*IdlePeriod, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO idle_periods (started_at, duration_seconds, active, box_host, box_name, box_port) VALUES (?, 0, 1, ?, ?, ?)`, now.Format(time.RFC3339Nano), boxHost, boxName, boxPort)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &IdlePeriod{ID: id, StartedAt: now, Active: true, BoxHost: boxHost, BoxName: boxName, BoxPort: boxPort}, nil
}

func (s *Store) UpdateIdle(p *IdlePeriod) error {
	var ended any
	if p.EndedAt != nil {
		ended = p.EndedAt.UTC().Format(time.RFC3339Nano)
	}
	active := 0
	if p.Active {
		active = 1
	}
	_, err := s.db.Exec(`UPDATE idle_periods SET ended_at=?, duration_seconds=?, active=? WHERE id=?`, ended, p.DurationSeconds, active, p.ID)
	return err
}

func (s *Store) FinishIdle(p *IdlePeriod) error {
	now := time.Now().UTC()
	p.EndedAt = &now
	p.Active = false
	return s.UpdateIdle(p)
}

func (s *Store) DeleteIdle(id int64) error {
	_, err := s.db.Exec(`DELETE FROM idle_periods WHERE id = ?`, id)
	return err
}

func (s *Store) ActiveWatch() (*Watch, error) {
	row := s.db.QueryRow(`
SELECT w.id, w.media_id, w.started_at, w.ended_at, w.watched_seconds, w.position_seconds,
       w.runtime_seconds, w.progress_percent, w.player_type, w.completed, w.active,
       w.box_host, w.box_name, w.box_port
FROM watches w WHERE w.active = 1 ORDER BY w.id DESC LIMIT 1`)
	w, err := scanWatch(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := s.GetMedia(w.MediaID)
	if err != nil {
		return nil, err
	}
	w.Media = m
	return w, nil
}

func (s *Store) GetMedia(id int64) (*Media, error) {
	row := s.db.QueryRow(`
SELECT id, kind, title, show_title, season, episode, year, file, source_url, unique_key, poster_path, extra_json, created_at
FROM media WHERE id = ?`, id)
	return scanMedia(row)
}

type ListFilter struct {
	Query  string
	Kind   string
	Box    string
	From   string
	To     string
	Limit  int
	Offset int
}

func (s *Store) ListWatches(f ListFilter) ([]Watch, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := []string{"w.active = 0"}
	args := []any{}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, `(m.title LIKE ? OR m.show_title LIKE ? OR m.file LIKE ? OR m.source_url LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like, like)
	}
	if k := strings.TrimSpace(f.Kind); k != "" && k != "all" {
		where = append(where, `m.kind = ?`)
		args = append(args, k)
	}
	if f.From != "" {
		where = append(where, `w.started_at >= ?`)
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, `w.started_at <= ?`)
		args = append(args, f.To)
	}
	if box := strings.TrimSpace(f.Box); box != "" && box != "all" {
		where = append(where, `(w.box_host = ? OR w.box_name = ?)`)
		args = append(args, box, box)
	}
	clause := strings.Join(where, " AND ")
	includeIdle := f.Kind == "" || f.Kind == "all" || f.Kind == "idle"
	if q := strings.TrimSpace(f.Query); q != "" && !strings.Contains(strings.ToLower(q), "idle") {
		includeIdle = includeIdle && f.Kind == "idle"
	}
	onlyIdle := f.Kind == "idle"

	var watchTotal int
	if !onlyIdle {
		countArgs := append([]any{}, args...)
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM watches w JOIN media m ON m.id = w.media_id WHERE `+clause, countArgs...).Scan(&watchTotal); err != nil {
			return nil, 0, err
		}
	}
	idleWhere := []string{"active = 0"}
	idleArgs := []any{}
	if f.From != "" {
		idleWhere = append(idleWhere, "started_at >= ?")
		idleArgs = append(idleArgs, f.From)
	}
	if f.To != "" {
		idleWhere = append(idleWhere, "started_at <= ?")
		idleArgs = append(idleArgs, f.To)
	}
	if box := strings.TrimSpace(f.Box); box != "" && box != "all" {
		idleWhere = append(idleWhere, `(box_host = ? OR box_name = ?)`)
		idleArgs = append(idleArgs, box, box)
	}
	var idleTotal int
	if includeIdle {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM idle_periods WHERE `+strings.Join(idleWhere, " AND "), idleArgs...).Scan(&idleTotal); err != nil {
			return nil, 0, err
		}
	}
	total := watchTotal + idleTotal

	out := []Watch{}
	if !onlyIdle {
		qargs := append([]any{}, args...)
		qargs = append(qargs, f.Limit+f.Offset)
		rows, err := s.db.Query(`
SELECT w.id, w.media_id, w.started_at, w.ended_at, w.watched_seconds, w.position_seconds,
       w.runtime_seconds, w.progress_percent, w.player_type, w.completed, w.active,
       w.box_host, w.box_name, w.box_port,
       m.id, m.kind, m.title, m.show_title, m.season, m.episode, m.year, m.file, m.source_url,
       m.unique_key, m.poster_path, m.extra_json, m.created_at
FROM watches w
JOIN media m ON m.id = w.media_id
WHERE `+clause+`
ORDER BY w.started_at DESC
LIMIT ?`, qargs...)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			w, err := scanWatchJoin(rows)
			if err != nil {
				rows.Close()
				return nil, 0, err
			}
			out = append(out, *w)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
	}
	if includeIdle {
		iargs := append([]any{}, idleArgs...)
		iargs = append(iargs, f.Limit+f.Offset)
		rows, err := s.db.Query(`
SELECT id, started_at, ended_at, duration_seconds, active, box_host, box_name, box_port
FROM idle_periods WHERE `+strings.Join(idleWhere, " AND ")+`
ORDER BY started_at DESC LIMIT ?`, iargs...)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var p IdlePeriod
			var started string
			var ended sql.NullString
			var active int
			if err := rows.Scan(&p.ID, &started, &ended, &p.DurationSeconds, &active, &p.BoxHost, &p.BoxName, &p.BoxPort); err != nil {
				rows.Close()
				return nil, 0, err
			}
			if t, err := time.Parse(time.RFC3339Nano, started); err == nil {
				p.StartedAt = t
			}
			if ended.Valid {
				if t, err := time.Parse(time.RFC3339Nano, ended.String); err == nil {
					p.EndedAt = &t
				}
			}
			p.Active = active == 1
			out = append(out, idleAsWatch(p))
		}
		rows.Close()
	}

	sortWatchesDesc(out)
	if f.Offset > len(out) {
		return []Watch{}, total, nil
	}
	end := f.Offset + f.Limit
	if end > len(out) {
		end = len(out)
	}
	return out[f.Offset:end], total, nil
}

func idleAsWatch(p IdlePeriod) Watch {
	return Watch{
		ID:             p.ID,
		StartedAt:      p.StartedAt,
		EndedAt:        p.EndedAt,
		WatchedSeconds: p.DurationSeconds,
		Active:         p.Active,
		PlayerType:     "idle",
		BoxHost:        p.BoxHost,
		BoxName:        p.BoxName,
		BoxPort:        p.BoxPort,
		Media: &Media{
			Kind:      "idle",
			Title:     "Kodi idle",
			UniqueKey: "idle",
		},
	}
}

func (s *Store) ListBoxes() ([]Box, error) {
	rows, err := s.db.Query(`
SELECT box_host, MAX(box_name), MAX(box_port), COUNT(*) FROM (
  SELECT box_host, box_name, box_port FROM watches WHERE box_host != ''
  UNION ALL
  SELECT box_host, box_name, box_port FROM idle_periods WHERE box_host != ''
) GROUP BY box_host ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Box
	for rows.Next() {
		var b Box
		if err := rows.Scan(&b.Host, &b.Name, &b.Port, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func sortWatchesDesc(items []Watch) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].StartedAt.After(items[i].StartedAt) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func (s *Store) Stats() (*Stats, error) {
	st := &Stats{}
	_ = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(watched_seconds),0) FROM watches WHERE active = 0`).Scan(&st.TotalWatches, new(int))
	var secs int
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(watched_seconds),0) FROM watches WHERE active = 0`).Scan(&st.TotalWatches, &secs); err != nil {
		return nil, err
	}
	st.TotalHours = float64(secs) / 3600.0
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM watches w JOIN media m ON m.id=w.media_id WHERE w.active=0 AND m.kind='movie'`).Scan(&st.Movies)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM watches w JOIN media m ON m.id=w.media_id WHERE w.active=0 AND m.kind='episode'`).Scan(&st.Episodes)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM watches w JOIN media m ON m.id=w.media_id WHERE w.active=0 AND m.kind IN ('youtube','plugin','web')`).Scan(&st.Plugins)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM media`).Scan(&st.UniqueTitles)
	weekAgo := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
	var weekSecs int
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(watched_seconds),0) FROM watches WHERE active=0 AND started_at >= ?`, weekAgo).Scan(&weekSecs)
	st.ThisWeekHours = float64(weekSecs) / 3600.0
	var idleSecs, weekIdle int
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(duration_seconds),0) FROM idle_periods WHERE active=0`).Scan(&idleSecs)
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(duration_seconds),0) FROM idle_periods WHERE active=0 AND started_at >= ?`, weekAgo).Scan(&weekIdle)
	st.IdleHours = float64(idleSecs) / 3600.0
	st.ThisWeekIdleHours = float64(weekIdle) / 3600.0
	return st, nil
}

type SeriesRollup struct {
	ShowTitle      string `json:"show_title"`
	Sessions       int    `json:"sessions"`
	Episodes       int    `json:"episodes"`
	Completed      int    `json:"completed"`
	WatchedSeconds int    `json:"watched_seconds"`
	LastWatched    string `json:"last_watched"`
	PosterPath     string `json:"poster_path,omitempty"`
	Seasons        []int  `json:"seasons,omitempty"`
}

type HeatDay struct {
	Date           string `json:"date"`
	WatchedSeconds int    `json:"watched_seconds"`
	IdleSeconds    int    `json:"idle_seconds"`
}

type Export struct {
	App        string         `json:"app"`
	ExportedAt string         `json:"exported_at"`
	Stats      *Stats         `json:"stats"`
	Watches    []Watch        `json:"watches"`
	Idle       []IdlePeriod   `json:"idle"`
	Series     []SeriesRollup `json:"series"`
	Heatmap    []HeatDay      `json:"heatmap"`
}

func (s *Store) SeriesRollup() ([]SeriesRollup, error) {
	rows, err := s.db.Query(`
SELECT
  m.show_title,
  COUNT(*) AS sessions,
  COUNT(DISTINCT printf('%d-%d', COALESCE(m.season,0), COALESCE(m.episode,0))) AS episodes,
  SUM(CASE WHEN w.completed = 1 THEN 1 ELSE 0 END) AS completed,
  COALESCE(SUM(w.watched_seconds),0) AS watched,
  MAX(w.started_at) AS last_watched,
  COALESCE((
    SELECT m2.poster_path FROM media m2
    WHERE m2.show_title = m.show_title AND m2.kind = 'episode' AND m2.poster_path != ''
    ORDER BY m2.id DESC LIMIT 1
  ), '') AS poster
FROM watches w
JOIN media m ON m.id = w.media_id
WHERE w.active = 0 AND m.kind = 'episode' AND m.show_title != ''
GROUP BY m.show_title
ORDER BY MAX(w.started_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesRollup{}
	for rows.Next() {
		var r SeriesRollup
		if err := rows.Scan(&r.ShowTitle, &r.Sessions, &r.Episodes, &r.Completed, &r.WatchedSeconds, &r.LastWatched, &r.PosterPath); err != nil {
			return nil, err
		}
		seasonRows, err := s.db.Query(`
SELECT DISTINCT m.season FROM watches w
JOIN media m ON m.id = w.media_id
WHERE w.active = 0 AND m.kind = 'episode' AND m.show_title = ? AND m.season IS NOT NULL
ORDER BY m.season`, r.ShowTitle)
		if err == nil {
			for seasonRows.Next() {
				var n int
				if seasonRows.Scan(&n) == nil {
					r.Seasons = append(r.Seasons, n)
				}
			}
			seasonRows.Close()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Heatmap(days int) ([]HeatDay, error) {
	if days <= 0 || days > 400 {
		days = 365
	}
	watchMap := map[string]int{}
	idleMap := map[string]int{}
	wrows, err := s.db.Query(`
SELECT substr(started_at,1,10), COALESCE(SUM(watched_seconds),0)
FROM watches WHERE active = 0 GROUP BY substr(started_at,1,10)`)
	if err != nil {
		return nil, err
	}
	for wrows.Next() {
		var d string
		var n int
		if wrows.Scan(&d, &n) == nil {
			watchMap[d] = n
		}
	}
	wrows.Close()
	irows, err := s.db.Query(`
SELECT substr(started_at,1,10), COALESCE(SUM(duration_seconds),0)
FROM idle_periods WHERE active = 0 GROUP BY substr(started_at,1,10)`)
	if err != nil {
		return nil, err
	}
	for irows.Next() {
		var d string
		var n int
		if irows.Scan(&d, &n) == nil {
			idleMap[d] = n
		}
	}
	irows.Close()

	out := make([]HeatDay, 0, days)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		out = append(out, HeatDay{
			Date:           key,
			WatchedSeconds: watchMap[key],
			IdleSeconds:    idleMap[key],
		})
	}
	return out, nil
}

func (s *Store) ListIdleAll() ([]IdlePeriod, error) {
	rows, err := s.db.Query(`
SELECT id, started_at, ended_at, duration_seconds, active
FROM idle_periods WHERE active = 0 ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IdlePeriod{}
	for rows.Next() {
		var p IdlePeriod
		var started string
		var ended sql.NullString
		var active int
		if err := rows.Scan(&p.ID, &started, &ended, &p.DurationSeconds, &active); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, started); err == nil {
			p.StartedAt = t
		}
		if ended.Valid {
			if t, err := time.Parse(time.RFC3339Nano, ended.String); err == nil {
				p.EndedAt = &t
			}
		}
		p.Active = active == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Export() (*Export, error) {
	st, err := s.Stats()
	if err != nil {
		return nil, err
	}
	watches, _, err := s.ListWatches(ListFilter{Kind: "all", Limit: 5000, Offset: 0})
	if err != nil {
		return nil, err
	}
	// ListWatches(kind=all) mixes idle in; split them.
	pure := []Watch{}
	for _, w := range watches {
		if w.Media != nil && w.Media.Kind == "idle" {
			continue
		}
		pure = append(pure, w)
	}
	idle, err := s.ListIdleAll()
	if err != nil {
		return nil, err
	}
	series, err := s.SeriesRollup()
	if err != nil {
		return nil, err
	}
	heat, err := s.Heatmap(365)
	if err != nil {
		return nil, err
	}
	return &Export{
		App:        "Kodi Kronicles",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Stats:      st,
		Watches:    pure,
		Idle:       idle,
		Series:     series,
		Heatmap:    heat,
	}, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMedia(row scanner) (*Media, error) {
	var m Media
	var season, episode, year sql.NullInt64
	var extra, created string
	if err := row.Scan(&m.ID, &m.Kind, &m.Title, &m.ShowTitle, &season, &episode, &year, &m.File, &m.SourceURL, &m.UniqueKey, &m.PosterPath, &extra, &created); err != nil {
		return nil, err
	}
	if season.Valid {
		v := int(season.Int64)
		m.Season = &v
	}
	if episode.Valid {
		v := int(episode.Int64)
		m.Episode = &v
	}
	if year.Valid {
		v := int(year.Int64)
		m.Year = &v
	}
	m.Extra = json.RawMessage(extra)
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		m.CreatedAt = t
	}
	return &m, nil
}

func scanWatch(row scanner) (*Watch, error) {
	var w Watch
	var started, ended sql.NullString
	var completed, active int
	if err := row.Scan(&w.ID, &w.MediaID, &started, &ended, &w.WatchedSeconds, &w.PositionSeconds, &w.RuntimeSeconds, &w.ProgressPercent, &w.PlayerType, &completed, &active, &w.BoxHost, &w.BoxName, &w.BoxPort); err != nil {
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339Nano, started.String); err == nil {
		w.StartedAt = t
	}
	if ended.Valid {
		if t, err := time.Parse(time.RFC3339Nano, ended.String); err == nil {
			w.EndedAt = &t
		}
	}
	w.Completed = completed == 1
	w.Active = active == 1
	return &w, nil
}

func scanWatchJoin(rows *sql.Rows) (*Watch, error) {
	var w Watch
	var m Media
	var started, ended, extra, created string
	var endedNull sql.NullString
	var season, episode, year sql.NullInt64
	var completed, active int
	if err := rows.Scan(
		&w.ID, &w.MediaID, &started, &endedNull, &w.WatchedSeconds, &w.PositionSeconds,
		&w.RuntimeSeconds, &w.ProgressPercent, &w.PlayerType, &completed, &active,
		&w.BoxHost, &w.BoxName, &w.BoxPort,
		&m.ID, &m.Kind, &m.Title, &m.ShowTitle, &season, &episode, &year, &m.File, &m.SourceURL,
		&m.UniqueKey, &m.PosterPath, &extra, &created,
	); err != nil {
		return nil, err
	}
	_ = ended
	if t, err := time.Parse(time.RFC3339Nano, started); err == nil {
		w.StartedAt = t
	}
	if endedNull.Valid {
		if t, err := time.Parse(time.RFC3339Nano, endedNull.String); err == nil {
			w.EndedAt = &t
		}
	}
	w.Completed = completed == 1
	w.Active = active == 1
	if season.Valid {
		v := int(season.Int64)
		m.Season = &v
	}
	if episode.Valid {
		v := int(episode.Int64)
		m.Episode = &v
	}
	if year.Valid {
		v := int(year.Int64)
		m.Year = &v
	}
	m.Extra = json.RawMessage(extra)
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		m.CreatedAt = t
	}
	w.Media = &m
	return &w, nil
}

func Ptr[T any](v T) *T { return &v }

func DisplayTitle(m *Media) string {
	if m == nil {
		return ""
	}
	if m.Kind == "episode" && m.ShowTitle != "" {
		se := ""
		if m.Season != nil && m.Episode != nil {
			se = fmt.Sprintf(" S%02dE%02d", *m.Season, *m.Episode)
		}
		if m.Title != "" {
			return m.ShowTitle + se + " – " + m.Title
		}
		return m.ShowTitle + se
	}
	if m.Kind == "iptv" && m.ShowTitle != "" && m.Title != "" && !strings.EqualFold(m.ShowTitle, m.Title) {
		return m.ShowTitle + " – " + m.Title
	}
	if m.Year != nil && *m.Year > 0 && m.Kind == "movie" {
		return fmt.Sprintf("%s (%d)", m.Title, *m.Year)
	}
	return m.Title
}
