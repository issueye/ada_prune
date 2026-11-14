package ui

import (
	"fmt"
)

// fmtInt renders an integer with thousands separators.
func fmtInt(v int64) string {
	negative := v < 0
	if negative {
		v = -v
	}
	raw := fmt.Sprintf("%d", v)
	out := make([]byte, 0, len(raw)+len(raw)/3)
	for i, digit := range []byte(raw) {
		if i > 0 && (len(raw)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digit)
	}
	if negative {
		return "-" + string(out)
	}
	return string(out)
}

// fmtMoney renders a cost with enough precision for small amounts.
func fmtMoney(v float64) string {
	if v == 0 {
		return "0"
	}
	if v < 0.01 {
		return fmt.Sprintf("%.4f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// fmtDuration renders a millisecond duration compactly.
func fmtDuration(ms int64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%dm%02ds", ms/60000, (ms%60000)/1000)
	}
}

// fmtTokens renders a token count compactly.
func fmtTokens(v int64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fK", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// mask renders a secret the way the store does, for display only.
func mask(secret string) string {
	runes := []rune(secret)
	switch {
	case len(runes) == 0:
		return ""
	case len(runes) <= 4:
		return "…"
	case len(runes) <= 14:
		return string(runes[:4]) + "…"
	default:
		return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
	}
}
