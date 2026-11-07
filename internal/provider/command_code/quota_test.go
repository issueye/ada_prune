package command_code

import (
	"testing"

	"github.com/learningdog1/ada_prune/internal/gateway"
)

func TestClassifyCreditExhaustion(t *testing.T) {
	verdict := classify(402, `{"error":{"message":"payment required","code":"quota_exceeded"}}`)
	if verdict.Kind != gateway.QuotaCredit {
		t.Fatalf("kind = %q, want credit", verdict.Kind)
	}
	if verdict.Message != "payment required" {
		t.Fatalf("message = %q", verdict.Message)
	}
}

func TestClassifyWindowFromRateLimitBlock(t *testing.T) {
	body := `{"error":{"message":"window exceeded","rateLimit":{"window":"fiveHour","reset":3600}}}`
	verdict := classify(429, body)
	if verdict.Kind != gateway.QuotaWindow {
		t.Fatalf("kind = %q, want window", verdict.Kind)
	}
	if verdict.Window != "fiveHour" {
		t.Fatalf("window = %q", verdict.Window)
	}
	if verdict.Until == 0 {
		t.Fatal("window verdict has no reset time")
	}
}

func TestClassifyWindowFromUsageExceededCode(t *testing.T) {
	verdict := classify(429, `{"error":{"message":"USAGE_EXCEEDED"}}`)
	if verdict.Kind != gateway.QuotaWindow {
		t.Fatalf("kind = %q, want window", verdict.Kind)
	}
	if verdict.Window != "usage" {
		t.Fatalf("window = %q", verdict.Window)
	}
}

func TestClassifyTransientRateLimit(t *testing.T) {
	verdict := classify(429, `{"error":{"message":"slow down"}}`)
	if verdict.Kind != gateway.QuotaRateLimit {
		t.Fatalf("kind = %q, want rate_limit", verdict.Kind)
	}
}

func TestClassifyOtherStatuses(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500, 503} {
		if got := classify(status, ""); got.Kind != gateway.QuotaNone {
			t.Errorf("status %d classified as %q, want none", status, got.Kind)
		}
	}
}

func TestRotateOn(t *testing.T) {
	a := &Adapter{}
	if !a.RotateOn(gateway.QuotaCredit) {
		t.Fatal("credit exhaustion must rotate")
	}
	if !a.RotateOn(gateway.QuotaWindow) {
		t.Fatal("a known window must rotate")
	}
	if a.RotateOn(gateway.QuotaRateLimit) {
		t.Fatal("a transient throttle must not rotate: it would switch device identity")
	}
	if a.RotateOn(gateway.QuotaNone) {
		t.Fatal("a non-quota failure must not rotate")
	}
}

func TestNormalizeReset(t *testing.T) {
	// Epoch milliseconds pass through.
	if got := normalizeReset(ptrFloat(1_793_500_000_000)); got != 1_793_500_000_000 {
		t.Fatalf("epoch reset = %d", got)
	}
	// Small values are delays in seconds.
	if got := normalizeReset(ptrFloat(60)); got == 0 || got < 1_700_000_000_000 {
		t.Fatalf("delay reset = %d, want a future timestamp", got)
	}
	if got := normalizeReset(nil); got != 0 {
		t.Fatalf("nil reset = %d", got)
	}
}

func TestUpstreamMessage(t *testing.T) {
	cases := map[string]string{
		`{"error":{"message":"from error"}}`: "from error",
		`{"message":"from root"}`:            "from root",
		`plain text`:                         "plain text",
		``:                                   "",
	}
	for body, want := range cases {
		if got := upstreamMessage(body); got != want {
			t.Errorf("upstreamMessage(%q) = %q, want %q", body, got, want)
		}
	}
}

func ptrFloat(v float64) *float64 { return &v }
