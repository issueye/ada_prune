package ui

import (
	"testing"
	"time"
)

// TestUsageTokenStats pins the token module: the summary leads with token
// tiles, the daily trend stacks input under output, and the table adds the
// input+output total column.
func TestUsageTokenStats(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/usage"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	for _, want := range []string{
		"请求", "输入 token", "输出 token", "缓存 token", "合计 token", "费用",
		"Token 趋势", "近 30 天", "合计 9,930 token",
	} {
		if !tt.HasText(want) {
			t.Fatalf("usage page is missing %q; texts: %v", want, tt.Texts())
		}
	}

	// The tiles show compact values with the exact token counts as hints, so a
	// client key with a token quota can be compared without mental arithmetic.
	for _, want := range []string{"8,700", "1,230", "4.8K", "9,930", "平均 1,655 token", "占输入 55%"} {
		if !tt.HasText(want) {
			t.Fatalf("token summary is missing %q; texts: %v", want, tt.Texts())
		}
	}

	// The default window is 30 days, and picking another range updates it.
	if state.usageRange != "30d" {
		t.Fatalf("default range = %q, want 30d", state.usageRange)
	}
	if err := tt.Click("近 7 天"); err != nil {
		t.Fatalf("pick 7 days: %v", err)
	}
	tt.Frame()
	if state.usageRange != "7d" {
		t.Fatalf("range = %q, want 7d", state.usageRange)
	}
}

// TestUsageTableTotalColumn pins the table's 合计 column: it shows each row's
// input+output tokens, distinct from either side alone.
func TestUsageTableTotalColumn(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	// A second model with a small, exact total so the row is unambiguous.
	if err := state.app.Store.RecordUsage(time.Now().UnixMilli(), 0, "gpt-5.6-luna", 0, 300, 200, 0, 0.001); err != nil {
		t.Fatalf("seed second model usage: %v", err)
	}

	state.page = "/usage"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if err := tt.Click("按模型"); err != nil {
		t.Fatalf("group by model: %v", err)
	}
	tt.Frame()
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if !tt.HasText("500") {
		t.Fatalf("the 合计 column should show the row's input+output; texts: %v", tt.Texts())
	}
	rows := state.usage.Rows
	for _, row := range rows {
		if row.Model == "gpt-5.6-luna" && row.InputTokens+row.OutputTokens != 500 {
			t.Fatalf("row tokens = %d+%d, want 500 total", row.InputTokens, row.OutputTokens)
		}
	}
}

// TestUsageRangeWindow checks that the range selector actually bounds the
// numbers and the trend: usage older than the window is hidden until 全部.
func TestUsageRangeWindow(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	old := time.Now().AddDate(0, 0, -40).UTC()
	if err := state.app.Store.RecordUsage(old.UnixMilli(), 0, "old-model", 0, 100, 50, 0, 0.01); err != nil {
		t.Fatalf("seed old usage: %v", err)
	}

	state.page = "/usage"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if got := state.usage.Summary.InputTokens; got != 8700 {
		t.Fatalf("30-day input tokens = %d, want the seeded 8700 without the old row", got)
	}
	if tt.HasText(old.Format("2006-01-02")) {
		t.Fatalf("the 40-day-old row should be outside the 30-day window; texts: %v", tt.Texts())
	}

	if err := tt.Click("全部"); err != nil {
		t.Fatalf("pick all time: %v", err)
	}
	tt.Frame()
	if state.usageRange != "all" {
		t.Fatalf("range = %q, want all", state.usageRange)
	}
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if got := state.usage.Summary.InputTokens; got != 8800 {
		t.Fatalf("all-time input tokens = %d, want 8800 with the old row", got)
	}
	if !tt.HasText(old.Format("2006-01-02")) {
		t.Fatalf("the old day should show under 全部; texts: %v", tt.Texts())
	}
}
