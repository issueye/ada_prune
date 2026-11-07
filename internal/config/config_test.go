package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestFirstRunWritesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config.json was not created: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var file map[string]any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("defaults are not valid JSON: %v", err)
	}
	if got := file["port"]; got != float64(3050) {
		t.Fatalf("default port = %v, want 3050", got)
	}
	if s.Get().Host != "127.0.0.1" {
		t.Fatalf("default host = %q", s.Get().Host)
	}
}

func TestHostAndPortArePinnedByEnv(t *testing.T) {
	t.Setenv("ADP_PORT", "4111")
	t.Setenv("ADP_HOST", "0.0.0.0")

	s := openTemp(t)
	if got := s.Get().Port; got != 4111 {
		t.Fatalf("port = %d, want the env value 4111", got)
	}
	if got := s.Get().Host; got != "0.0.0.0" {
		t.Fatalf("host = %q, want the env value", got)
	}

	pinned := s.PinnedKeys()
	if !contains(pinned, "port") || !contains(pinned, "host") {
		t.Fatalf("pinned keys = %v, want port and host", pinned)
	}

	// A save that disagrees with the environment is reported and dropped.
	res, err := s.Save(map[string]any{"port": 9999})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !contains(res.Ignored, "port") {
		t.Fatalf("ignored = %v, want port", res.Ignored)
	}
	if got := s.Get().Port; got != 4111 {
		t.Fatalf("port after save = %d, want 4111", got)
	}
}

func TestSavePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	res, err := s.Save(map[string]any{
		"logLevel":       "debug",
		"maxInflight":    7,
		"allowAnonymous": true,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(res.RestartRequired) != 0 {
		t.Fatalf("unexpected restart requirement: %v", res.RestartRequired)
	}

	// A second store must see the values, which proves they reached the file.
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := again.Get()
	if got.LogLevel != "debug" || got.MaxInflight != 7 || !got.AllowAnonymous {
		t.Fatalf("reloaded snapshot = %+v", got)
	}
}

func TestSaveReportsRestartForPortChange(t *testing.T) {
	s := openTemp(t)
	res, err := s.Save(map[string]any{"port": 4555})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !contains(res.RestartRequired, "port") {
		t.Fatalf("restartRequired = %v, want port", res.RestartRequired)
	}
}

func TestUnknownKeysArePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := `{"port":3050,"customFlag":"keep-me"}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Save(map[string]any{"logLevel": "warn"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var file map[string]any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if file["customFlag"] != "keep-me" {
		t.Fatalf("unknown key was dropped: %v", file["customFlag"])
	}
}

func TestSaveIgnoresReadOnlyAndUnknownKeys(t *testing.T) {
	s := openTemp(t)
	res, err := s.Save(map[string]any{"notARealKey": 1, "port": 3051})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := res.Saved["notARealKey"]; ok {
		t.Fatal("unknown key was reported as saved")
	}
	if got := s.Get().Port; got != 3051 {
		t.Fatalf("port = %d, want 3051", got)
	}
}

func TestValidate(t *testing.T) {
	base := Defaults()
	if err := base.Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}

	cases := map[string]func(*Snapshot){
		"port":        func(s *Snapshot) { s.Port = 0 },
		"host":        func(s *Snapshot) { s.Host = "  " },
		"maxBodyMB":   func(s *Snapshot) { s.MaxBodyMB = 0 },
		"streamIdle":  func(s *Snapshot) { s.StreamIdleMs = 0 },
		"nonStream":   func(s *Snapshot) { s.NonStreamIdleMs = 0 },
		"maxInflight": func(s *Snapshot) { s.MaxInflight = -1 },
		"keepAlive":   func(s *Snapshot) { s.KeepAliveTimeoutMs = 0 },
		"retention":   func(s *Snapshot) { s.LogRetentionDays = 0 },
	}
	for name, mutate := range cases {
		s := base
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestFileViewHidesNothing(t *testing.T) {
	s := openTemp(t)
	view := s.FileView()
	if view["port"] != float64(3050) {
		t.Fatalf("FileView port = %v", view["port"])
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
