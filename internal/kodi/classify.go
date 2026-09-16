package kodi

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type Classified struct {
	Kind      string
	Title     string
	ShowTitle string
	Season    *int
	Episode   *int
	Year      *int
	File      string
	SourceURL string
	UniqueKey string
	ArtPath   string
	YouTubeID string
	Extra     map[string]any
}

var ytID = regexp.MustCompile(`(?i)(?:video_id=|videoid=|youtu\.be/|watch\?v=|/shorts/|/embed/|[?&]file=|/youtube/(?:manifest/)?dash(?:/|\?file=)?)([A-Za-z0-9_-]{11})`)

func Classify(item *Item, artPref string) Classified {
	c := Classified{
		File:  strings.TrimSpace(item.File),
		Title: firstNonEmpty(item.Title, item.Label),
		Extra: map[string]any{},
	}
	if item.Year > 0 {
		y := item.Year
		c.Year = &y
	}
	if item.Plot != "" {
		c.Extra["plot"] = item.Plot
	}
	if g := StringList(item.Genre); len(g) > 0 {
		c.Extra["genre"] = g
	}
	if s := StringList(item.Studio); len(s) > 0 {
		c.Extra["studio"] = s
	}
	if d := StringList(item.Director); len(d) > 0 {
		c.Extra["director"] = d
	}
	if a := StringList(item.Artist); len(a) > 0 {
		c.Extra["artist"] = a
	}
	if item.Album != "" {
		c.Extra["album"] = item.Album
	}
	if item.Premiered != "" {
		c.Extra["premiered"] = item.Premiered
	}
	if item.FirstAired != "" {
		c.Extra["firstaired"] = item.FirstAired
	}
	if len(item.UniqueID) > 0 {
		c.Extra["uniqueid"] = item.UniqueID
	}
	c.Extra["kodi_type"] = item.Type
	c.Extra["label"] = item.Label

	file := strings.ToLower(c.File)
	c.YouTubeID = extractYouTubeID(item.File + " " + item.Title + " " + item.Label)
	channel := strings.TrimSpace(item.Channel)
	if channel == "" && (strings.EqualFold(item.Type, "channel") || strings.HasPrefix(file, "pvr://")) {
		channel = strings.TrimSpace(item.Label)
	}
	switch {
	case c.YouTubeID != "":
		c.Kind = "youtube"
		c.SourceURL = "https://www.youtube.com/watch?v=" + c.YouTubeID
		c.UniqueKey = "youtube:" + c.YouTubeID
	case isIPTV(item):
		c.Kind = "iptv"
		if channel != "" {
			c.ShowTitle = channel
		}
		prog := strings.TrimSpace(item.Title)
		if prog != "" && !strings.EqualFold(prog, channel) && !strings.EqualFold(prog, item.Label) {
			c.Title = prog
		} else if channel != "" {
			c.Title = firstNonEmpty(prog, channel, item.Label)
		}
		c.SourceURL = item.File
		keyBit := strings.ToLower(firstNonEmpty(channel, c.Title, c.File))
		c.UniqueKey = "iptv:" + hash(keyBit)
		c.Extra["channel"] = channel
		if item.ChannelNumber > 0 {
			c.Extra["channel_number"] = item.ChannelNumber
		}
		if item.ChannelType != "" {
			c.Extra["channel_type"] = item.ChannelType
		}
	case strings.HasPrefix(file, "plugin://plugin.video.youtube"):
		c.Kind = "youtube"
		c.SourceURL = item.File
		c.UniqueKey = "plugin:" + hash(item.File)
	case strings.HasPrefix(file, "plugin://") || strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://"):
		c.Kind = "plugin"
		if strings.HasPrefix(file, "http") {
			c.Kind = "web"
		}
		c.SourceURL = item.File
		c.UniqueKey = c.Kind + ":" + hash(item.File)
	case item.Type == "episode" || (item.ShowTitle != "" && (item.Season > 0 || item.Episode > 0)):
		c.Kind = "episode"
		c.ShowTitle = item.ShowTitle
		if item.Season > 0 || item.Type == "episode" {
			s := item.Season
			c.Season = &s
		}
		if item.Episode > 0 || item.Type == "episode" {
			e := item.Episode
			c.Episode = &e
		}
		showKey := strings.ToLower(strings.TrimSpace(item.ShowTitle))
		c.UniqueKey = fmt.Sprintf("episode:%s:%d:%d:%s", showKey, item.Season, item.Episode, strings.ToLower(c.Title))
	case item.Type == "movie" || looksLikeLocalFile(item.File):
		parsed := parseReleaseName(firstNonEmpty(item.Title, item.Label, baseName(item.File)))
		if parsed.Title != "" {
			c.Title = parsed.Title
		}
		if c.Year == nil && parsed.Year > 0 {
			y := parsed.Year
			c.Year = &y
		}
		if parsed.Season > 0 {
			c.Kind = "episode"
			c.ShowTitle = parsed.Title
			s, e := parsed.Season, parsed.Episode
			c.Season, c.Episode = &s, &e
			if parsed.EpisodeTitle != "" {
				c.Title = parsed.EpisodeTitle
			} else {
				c.Title = fmt.Sprintf("S%02dE%02d", s, e)
			}
			c.UniqueKey = fmt.Sprintf("episode:%s:%d:%d:%s", strings.ToLower(c.ShowTitle), s, e, strings.ToLower(c.Title))
		} else {
			c.Kind = "movie"
			yr := 0
			if c.Year != nil {
				yr = *c.Year
			}
			c.UniqueKey = fmt.Sprintf("movie:%s:%d", strings.ToLower(c.Title), yr)
		}
	case item.Type == "musicvideo":
		c.Kind = "musicvideo"
		c.UniqueKey = "musicvideo:" + hash(c.Title+"|"+c.File)
	case item.Type == "song" || item.Type == "audio":
		c.Kind = "song"
		c.UniqueKey = "song:" + hash(c.Title+"|"+c.File)
	default:
		parsed := parseReleaseName(firstNonEmpty(item.Title, item.Label, baseName(item.File)))
		if parsed.Title != "" {
			c.Title = parsed.Title
		}
		if c.Year == nil && parsed.Year > 0 {
			y := parsed.Year
			c.Year = &y
		}
		if parsed.Year > 0 || looksLikeLocalFile(item.File) {
			c.Kind = "movie"
			yr := 0
			if c.Year != nil {
				yr = *c.Year
			}
			c.UniqueKey = fmt.Sprintf("movie:%s:%d", strings.ToLower(c.Title), yr)
		} else {
			c.Kind = "video"
			if c.File != "" {
				c.UniqueKey = "file:" + hash(c.File)
			} else {
				c.UniqueKey = "label:" + hash(c.Title)
			}
		}
	}
	if c.SourceURL == "" && (strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") || strings.HasPrefix(file, "plugin://")) {
		c.SourceURL = item.File
	}
	c.ArtPath = artPref
	if parsed, err := url.Parse(c.SourceURL); err == nil && parsed.Host != "" {
		c.Extra["host"] = parsed.Host
	}
	return c
}

var ytDashFile = regexp.MustCompile(`(?i)[?&]file=([A-Za-z0-9_-]{11})\.mpd`)

func extractYouTubeID(s string) string {
	s = strings.ReplaceAll(s, `\u0026`, "&")
	if m := ytDashFile.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	if m := ytID.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	if u, err := url.Parse(s); err == nil {
		if id := u.Query().Get("video_id"); len(id) == 11 {
			return id
		}
		if id := u.Query().Get("videoid"); len(id) == 11 {
			return id
		}
		if id := u.Query().Get("v"); len(id) == 11 {
			return id
		}
	}
	return ""
}

type parsedName struct {
	Title        string
	EpisodeTitle string
	Year         int
	Season       int
	Episode      int
}

var (
	yearRe   = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	seRe     = regexp.MustCompile(`(?i)\bS(\d{1,2})E(\d{1,3})\b`)
	junkRe   = regexp.MustCompile(`(?i)\b(1080p|720p|480p|2160p|4k|uhd|hdr10plus|hdr10|hdr|dv|dolby.?vision|web-?dl|webrip|bluray|blu-ray|x265|x264|h\.?265|h\.?264|hevc|avc|aac|dts|truehd|atmos|remux|proper|repack|extended|directors?\.?cut|unrated|multi|subs?|internal|amzn|nf|dsnp|hmax|remastered|criterion)\b`)
	sepRe    = regexp.MustCompile(`[._+\-]+`)
	spaceRe  = regexp.MustCompile(`\s+`)
	localSchemes = []string{"smb://", "nfs://", "ftp://", "sftp://", "file://", "upnp://"}
)

func looksLikeLocalFile(file string) bool {
	f := strings.ToLower(file)
	for _, s := range localSchemes {
		if strings.HasPrefix(f, s) {
			return true
		}
	}
	if strings.HasPrefix(f, "/") && !strings.HasPrefix(f, "//") {
		return true
	}
	switch {
	case strings.HasSuffix(f, ".mkv"), strings.HasSuffix(f, ".mp4"),
		strings.HasSuffix(f, ".avi"), strings.HasSuffix(f, ".iso"),
		strings.HasSuffix(f, ".m2ts"), strings.HasSuffix(f, ".ts"):
		return !strings.HasPrefix(f, "http://") && !strings.HasPrefix(f, "https://") && !strings.HasPrefix(f, "plugin://")
	}
	return false
}

func baseName(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	file = strings.ReplaceAll(file, "\\", "/")
	if i := strings.LastIndex(file, "/"); i >= 0 {
		file = file[i+1:]
	}
	if i := strings.LastIndex(file, "."); i > 0 {
		file = file[:i]
	}
	return file
}

func parseReleaseName(raw string) parsedName {
	s := strings.TrimSpace(raw)
	if s == "" {
		return parsedName{}
	}
	s = strings.ReplaceAll(s, "\\", "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "."); i > 0 && len(s[i:]) <= 5 {
		s = s[:i]
	}
	out := parsedName{}
	if m := seRe.FindStringSubmatch(s); len(m) == 3 {
		fmt.Sscanf(m[1], "%d", &out.Season)
		fmt.Sscanf(m[2], "%d", &out.Episode)
		parts := seRe.Split(s, 2)
		out.Title = cleanReleaseToken(parts[0])
		if len(parts) == 2 {
			out.EpisodeTitle = cleanReleaseToken(parts[1])
		}
	}
	if m := yearRe.FindStringSubmatch(s); len(m) == 2 {
		fmt.Sscanf(m[1], "%d", &out.Year)
		if out.Title == "" {
			out.Title = cleanReleaseToken(yearRe.Split(s, 2)[0])
		}
	}
	if out.Title == "" {
		out.Title = cleanReleaseToken(s)
	}
	return out
}

func cleanReleaseToken(s string) string {
	s = sepRe.ReplaceAllString(s, " ")
	s = junkRe.ReplaceAllString(s, " ")
	s = yearRe.ReplaceAllString(s, " ")
	s = seRe.ReplaceAllString(s, " ")
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) <= 2 && strings.ToUpper(w) == w {
			continue
		}
		lw := strings.ToLower(w)
		if i > 0 && (lw == "of" || lw == "the" || lw == "and" || lw == "a" || lw == "in") {
			words[i] = lw
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

func isIPTV(item *Item) bool {
	if item == nil {
		return false
	}
	if strings.EqualFold(item.Type, "channel") || strings.TrimSpace(item.Channel) != "" || item.ChannelNumber > 0 {
		return true
	}
	file := strings.ToLower(item.File)
	switch {
	case strings.HasPrefix(file, "pvr://"):
		return true
	case strings.Contains(file, ".m3u8"), strings.HasSuffix(file, ".m3u"):
		return true
	case strings.HasPrefix(file, "rtp://"), strings.HasPrefix(file, "rtsp://"),
		strings.HasPrefix(file, "udp://"), strings.HasPrefix(file, "rtmp://"):
		return true
	}
	return false
}

func hash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "Unknown"
}

func ExtraJSON(c Classified) json.RawMessage {
	b, err := json.Marshal(c.Extra)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
