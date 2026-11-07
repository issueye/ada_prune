package command_code

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// decodeInto applies a JSON map onto a struct. It is how adapters read their own
// adapter_config without hand-rolling a key parser for every field.
func decodeInto(raw map[string]any, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("adapter config is not serialisable: %w", err)
	}
	if err := json.Unmarshal(buf, dst); err != nil {
		return fmt.Errorf("adapter config has an invalid field: %w", err)
	}
	return nil
}

// randomHex returns n random bytes as hex.
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(buf)
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// truncate shortens a string to at most n runes, for operator-facing excerpts.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
