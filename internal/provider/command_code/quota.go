package command_code

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
)

// Quota semantics of the upstream, which are not the standard ones:
//
//   - 402 means the account's credit is gone. That is durable: the credential is
//     marked exhausted and must be reloaded or replaced.
//   - 429 with a usage window means a rate window is exhausted until a known
//     reset. That is temporary and lives in memory only.
//   - any other 429 is a transient throttle. It must not trigger rotation:
//     switching credentials switches device identity, which is only worth doing
//     when the account truly cannot serve.

// classify inspects an upstream refusal.
func classify(status int, body string) gateway.QuotaVerdict {
	switch status {
	case 402:
		return gateway.QuotaVerdict{
			Kind:    gateway.QuotaCredit,
			Message: upstreamMessage(body),
		}
	case 429:
		window, until, transient := rateLimitWindow(body)
		if window == "" && !transient && strings.Contains(strings.ToUpper(body), "USAGE_EXCEEDED") {
			return gateway.QuotaVerdict{
				Kind:    gateway.QuotaWindow,
				Window:  "usage",
				Until:   until,
				Message: upstreamMessage(body),
			}
		}
		if window != "" {
			return gateway.QuotaVerdict{
				Kind:    gateway.QuotaWindow,
				Window:  window,
				Until:   until,
				Message: upstreamMessage(body),
			}
		}
		return gateway.QuotaVerdict{
			Kind:    gateway.QuotaRateLimit,
			Message: upstreamMessage(body),
		}
	default:
		return gateway.QuotaVerdict{Kind: gateway.QuotaNone, Message: upstreamMessage(body)}
	}
}

// rateLimitWindow reads a rateLimit block from an error body. transient reports
// an explicit "this will pass" hint, which keeps a 429 out of the window bucket.
func rateLimitWindow(body string) (window string, until int64, transient bool) {
	if body == "" {
		return "", 0, false
	}
	var parsed struct {
		Error struct {
			RateLimit   *RateLimit `json:"rateLimit"`
			IsRetryable *bool      `json:"isRetryable"`
		} `json:"error"`
		RateLimit *RateLimit `json:"rateLimit"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return "", 0, false
	}
	rl := parsed.Error.RateLimit
	if rl == nil {
		rl = parsed.RateLimit
	}
	if rl == nil {
		return "", 0, false
	}
	return strings.TrimSpace(rl.Window), normalizeReset(rl.Reset), false
}

// normalizeReset converts a reset hint into Unix milliseconds. The upstream
// reports either epoch milliseconds or a delay in seconds; anything below 1e11
// is treated as a delay.
func normalizeReset(reset *float64) int64 {
	if reset == nil || *reset <= 0 {
		return 0
	}
	v := *reset
	if v >= 1e11 {
		return int64(v)
	}
	return time.Now().UnixMilli() + int64(v*1000)
}

// upstreamMessage extracts the human-readable message from an error body.
func upstreamMessage(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		if parsed.Error.Message != "" {
			return truncate(parsed.Error.Message, 300)
		}
		if parsed.Message != "" {
			return truncate(parsed.Message, 300)
		}
	}
	return truncate(trimmed, 200)
}

// Classify implements gateway.QuotaPolicy.
func (a *Adapter) Classify(status int, body []byte) gateway.QuotaVerdict {
	return classify(status, string(body))
}

// RotateOn implements gateway.QuotaPolicy. Only a durable credit exhaustion or a
// known window reset justifies spending another credential's identity.
func (a *Adapter) RotateOn(kind gateway.QuotaKind) bool {
	switch kind {
	case gateway.QuotaCredit, gateway.QuotaWindow:
		return true
	default:
		return false
	}
}
