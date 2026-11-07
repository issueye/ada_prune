package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/app"
)

// newTester boots a real app on a temp directory and gives a headless tester for
// its view. No network and no window are involved.
func newTester(t *testing.T) (*uiState, *ui.Tester) {
	t.Helper()
	a, err := app.New(app.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(a.Close)

	state := newUIState(a)
	return state, ui.NewTester(state.view, 1280, 860)
}

func TestDashboardRenders(t *testing.T) {
	state, tt := newTester(t)

	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if !tt.HasText("总览") {
		t.Fatalf("page title is missing; texts: %v", tt.Texts())
	}
	if !tt.HasText("今日请求") {
		t.Fatalf("KPI label is missing; texts: %v", tt.Texts())
	}
	if !tt.HasText("最近 24 小时") {
		t.Fatal("the chart panel is missing")
	}
}

func TestNavigationSwitchesPages(t *testing.T) {
	state, tt := newTester(t)

	for _, tc := range []struct {
		label string
		page  string
	}{
		{"供应商", "/providers"},
		{"模型", "/models"},
		{"凭据", "/credentials"},
		{"客户端密钥", "/keys"},
		{"端点", "/endpoints"},
		{"设置", "/settings"},
		{"关于", "/about"},
		{"总览", "/"},
	} {
		if err := tt.Click(tc.label); err != nil {
			t.Fatalf("click %q: %v", tc.label, err)
		}
		if state.page != tc.page {
			t.Fatalf("after clicking %q page = %q, want %q", tc.label, state.page, tc.page)
		}
		// Rendering the page must not panic with empty data.
		tt.Frame()
	}
}

func TestEmptyStatesGuideTheUser(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/providers"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if !tt.HasText("还没有供应商") {
		t.Fatalf("empty state is missing; texts: %v", tt.Texts())
	}

	state.page = "/keys"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if !tt.HasText("还没有客户端密钥") {
		t.Fatalf("key empty state is missing; texts: %v", tt.Texts())
	}
}

func TestClientKeyCopyToClipboard(t *testing.T) {
	state, tt := newTester(t)

	const token = "sk-prune-0123456789abcdef0123456789abcdef"
	state.createdToken = token
	state.page = "/keys"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if !tt.HasText(token) {
		t.Fatalf("one-time token banner is missing; texts: %v", tt.Texts())
	}
	if err := tt.Click("复制"); err != nil {
		t.Fatalf("click copy: %v", err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != token {
		t.Fatalf("clipboard = %q, want the created token", got)
	}
	if state.status != "已复制到剪贴板" {
		t.Fatalf("status = %q, want copy confirmation", state.status)
	}
}

func TestProviderListShowsAccessMode(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state) // an openai_compatible provider: API
	if _, err := state.app.Admin.CreateProvider(mustContext(t), admin.ProviderInput{
		Name:    "CC 反代",
		Kind:    "command_code",
		BaseURL: "https://api.commandcode.ai",
	}); err != nil {
		t.Fatalf("create reverse proxy provider: %v", err)
	}

	state.page = "/providers"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if !tt.HasText("API") {
		t.Fatalf("API badge is missing; texts: %v", tt.Texts())
	}
	if !tt.HasText("反代") {
		t.Fatalf("reverse proxy badge is missing; texts: %v", tt.Texts())
	}

	// The detail panel of the reverse proxy row carries the same badge.
	state.providerSel = len(state.providers) - 1
	tt.Frame()
	if !tt.HasText("供应商 · CC 反代") {
		t.Fatalf("detail panel is missing; texts: %v", tt.Texts())
	}
}

func TestSettingsPageShowsPinnedKeys(t *testing.T) {
	t.Setenv("PRUNE_PORT", "4555")
	state, tt := newTester(t)

	state.page = "/settings"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if !tt.HasText("设置") {
		t.Fatal("settings title is missing")
	}
	// The port comes from the environment, so the page must say it is pinned.
	if !tt.HasText("被环境变量钉住的键") {
		t.Fatalf("pinned-key notice is missing; texts: %v", tt.Texts())
	}
}

func TestAboutPageListsAdapters(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/about"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if !tt.HasText("已注册适配器") {
		t.Fatal("adapter panel is missing")
	}
	if !tt.HasText("Command Code") {
		t.Fatalf("adapter label is missing; texts: %v", tt.Texts())
	}
}

func TestFormattingHelpers(t *testing.T) {
	if got := fmtInt(1234567); got != "1,234,567" {
		t.Errorf("fmtInt = %q", got)
	}
	if got := fmtTokens(1500); got != "1.5K" {
		t.Errorf("fmtTokens = %q", got)
	}
	if got := fmtDuration(1500); got != "1.5s" {
		t.Errorf("fmtDuration = %q", got)
	}
	if got := mask("user_0123456789abcdef"); got != "user_012…cdef" {
		t.Errorf("mask = %q", got)
	}
}
