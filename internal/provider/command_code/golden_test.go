package command_code

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goldenPath resolves testdata/golden from this package.
func goldenPath(name string) string {
	return filepath.Join("..", "..", "..", "testdata", "golden", name)
}

// checkGolden compares got against testdata/golden/name. With UPDATE_GOLDEN=1 it
// rewrites the file instead, which is how a reviewed change lands.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := goldenPath(name)

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\nrun with UPDATE_GOLDEN=1 to create it", name, err)
	}
	// Allow exactly one trailing newline on either side; everything else is
	// significant, including key order and spacing.
	if trimOneNewline(string(want)) != trimOneNewline(string(got)) {
		t.Fatalf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func trimOneNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}

// jsonMarshalIndent renders a value the way the golden files store it.
func jsonMarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
