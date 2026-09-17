package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type IgnoreRules struct {
	MinSeconds    int      `json:"min_seconds"`
	Kinds         []string `json:"kinds"`
	TitleContains []string `json:"title_contains"`
	FileContains  []string `json:"file_contains"`
	Plugins       []string `json:"plugins"`
}

func ignorePath(dataDir string) string {
	return filepath.Join(dataDir, "ignore-rules.json")
}

func DefaultIgnore() IgnoreRules {
	return IgnoreRules{
		MinSeconds:    0,
		FileContains:  []string{"trailer", "sample"},
		TitleContains: []string{},
		Kinds:         []string{},
		Plugins:       []string{},
	}
}

func LoadIgnore(dataDir string) IgnoreRules {
	raw, err := os.ReadFile(ignorePath(dataDir))
	if err != nil {
		return DefaultIgnore()
	}
	var r IgnoreRules
	if json.Unmarshal(raw, &r) != nil {
		return DefaultIgnore()
	}
	return r
}

func SaveIgnore(dataDir string, r IgnoreRules) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ignorePath(dataDir), raw, 0o600)
}

func (r IgnoreRules) Skip(kind, title, show, file, source string, watched, fallbackMin int) (bool, string) {
	min := r.MinSeconds
	if min <= 0 {
		min = fallbackMin
	}
	if watched < min {
		return true, "shorter than min seconds"
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, k := range r.Kinds {
		if strings.ToLower(strings.TrimSpace(k)) == kind && k != "" {
			return true, "kind " + kind
		}
	}
	blob := strings.ToLower(title + " " + show)
	for _, n := range r.TitleContains {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && strings.Contains(blob, n) {
			return true, "title contains " + n
		}
	}
	path := strings.ToLower(file + " " + source)
	for _, n := range r.FileContains {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && strings.Contains(path, n) {
			return true, "file contains " + n
		}
	}
	for _, p := range r.Plugins {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && strings.Contains(path, p) {
			return true, "plugin " + p
		}
	}
	return false, ""
}
