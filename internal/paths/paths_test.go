package paths

import (
	"path/filepath"
	"testing"
)

func TestResolveUsesEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADP_DATA_DIR", dir)

	got := Resolve("ignored")
	if got.Data != dir {
		t.Fatalf("Data = %q, want %q", got.Data, dir)
	}
	if got.DB != filepath.Join(dir, "adaproxy.db") {
		t.Fatalf("DB = %q", got.DB)
	}
	if got.SecretKey != filepath.Join(dir, ".secret.key") {
		t.Fatalf("SecretKey = %q", got.SecretKey)
	}
	if got.Config != filepath.Join(dir, "config.json") {
		t.Fatalf("Config = %q", got.Config)
	}
	if got.Logs != filepath.Join(dir, "logs") {
		t.Fatalf("Logs = %q", got.Logs)
	}
	if got.LogFile != filepath.Join(dir, "logs", "adaproxy.log") {
		t.Fatalf("LogFile = %q", got.LogFile)
	}
}

func TestResolvePrefersArgumentOverExecutable(t *testing.T) {
	t.Setenv("ADP_DATA_DIR", "")
	dir := t.TempDir()

	got := Resolve(dir)
	if got.Data != dir {
		t.Fatalf("Data = %q, want %q", got.Data, dir)
	}
}

func TestExecutableDirIsAbsolute(t *testing.T) {
	dir := ExecutableDir()
	if dir == "" {
		t.Fatal("ExecutableDir returned an empty string")
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("ExecutableDir = %q, want an absolute path", dir)
	}
}
