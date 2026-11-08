package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/store"
)

// TestDeleteProviderRequiresConfirmation pins the destructive-action flow: the
// first click only opens the confirmation, Cancel keeps the row, confirming
// removes it.
func TestDeleteProviderRequiresConfirmation(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)
	// A provider with enabled models is refused by the store's delete guard,
	// so the delete target is a bare one.
	ctx := context.Background()
	if _, err := state.app.Admin.CreateProvider(ctx, admin.ProviderInput{
		Name:    "待删供应商",
		Kind:    "openai_compatible",
		BaseURL: "https://api.example.com",
	}); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	state.page = "/providers"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	state.deleteProvider(1)
	tt.Frame()
	if !state.confirmOpen {
		t.Fatal("delete should open a confirmation first")
	}
	if !tt.HasText("删除供应商 待删供应商？") {
		t.Fatalf("the confirmation should name the provider; texts: %v", tt.Texts())
	}

	// Cancel keeps everything.
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	tt.Frame()
	if state.confirmOpen {
		t.Fatal("cancel should close the confirmation")
	}
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(state.providers) != 2 {
		t.Fatalf("cancel must not delete; providers = %d", len(state.providers))
	}

	// Confirming runs the delete.
	state.deleteProvider(1)
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.providers) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("confirming 删除 should remove the provider")
}

// TestClientKeyTokenDismissesAndClears pins the one-time plaintext's lifecycle:
// the banner has a close button, and leaving the page wipes the token even
// without closing it.
func TestClientKeyTokenDismissesAndClears(t *testing.T) {
	state, tt := newTester(t)
	const token = "sk-prune-0123456789abcdef0123456789abcdef"
	state.createdToken = token
	state.page = "/keys"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if !tt.HasText(token) {
		t.Fatalf("the plaintext banner is missing; texts: %v", tt.Texts())
	}

	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("close the banner: %v", err)
	}
	tt.Frame()
	if state.createdToken != "" {
		t.Fatal("closing the banner should wipe the plaintext")
	}

	// Leaving the page clears a fresh token too, banner closed or not.
	state.createdToken = token
	state.page = "/providers"
	state.onPageChange()
	if state.createdToken != "" {
		t.Fatal("leaving /keys should clear the one-time plaintext")
	}
}

// TestOnboardingCardGuidesFirstRun pins the first-run guide: an empty gateway
// shows the three steps, and the primary action opens the provider form.
func TestOnboardingCardGuidesFirstRun(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if !tt.HasText("三步接入") {
		t.Fatalf("the onboarding card is missing; texts: %v", tt.Texts())
	}
	if err := tt.Click("新建供应商"); err != nil {
		t.Fatalf("click the guide's action: %v", err)
	}
	tt.Frame()
	if !state.formOpen {
		t.Fatal("the guide's primary action should open the provider form")
	}
}

// TestLogSelectionSurvivesRefresh pins the poll contract: the row the user is
// reading keeps its selection across a data refresh.
func TestLogSelectionSurvivesRefresh(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/logs"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(state.logs.Rows) < 3 {
		t.Fatalf("seeded rows = %d, want at least 3", len(state.logs.Rows))
	}
	state.logSel = 2
	want := state.logs.Rows[2].ID

	// The 5s poll lands as another refresh; the selection follows the row id.
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh again: %v", err)
	}
	if state.logSel < 0 || state.logs.Rows[state.logSel].ID != want {
		t.Fatalf("selection lost across refresh: sel = %d, want row %d", state.logSel, want)
	}
	tt.Frame()
}

// TestLogDetailPanelShowsRow pins the row detail: selecting a row surfaces the
// routing and failure fields the table cannot afford.
func TestLogDetailPanelShowsRow(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/logs"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// The seeded row with a failure: error type and finish reason both set.
	state.logSel = 4
	tt.Frame()

	for _, text := range []string{"请求 ID", "供应商", "客户端密钥", "耗时 / TTFT", "完成原因", "错误类型", "upstream_stream_error"} {
		if !tt.HasText(text) {
			t.Fatalf("the log detail is missing %q; texts: %v", text, tt.Texts())
		}
	}
}

// TestLogsPagination pins the pager: 20 rows per page by default, flips land
// on the matching slice of history, the last page holds the remainder, an
// out-of-range page collapses onto the last one, and a new page size reopens
// on the first page.
func TestLogsPagination(t *testing.T) {
	state, tt := newTester(t)
	// A tall viewport keeps the pager inside the window: the tester refuses
	// clicks on elements whose center lies outside it.
	tt.SetSize(1280, 2400)
	now := time.Now().UnixMilli()
	// 250 rows, one second apart: 13 pages of 20, the last one holding 10.
	for i := 0; i < 250; i++ {
		rec := store.RequestRecord{
			TS:         now - int64(i)*1000,
			Endpoint:   "/v1/chat/completions",
			StatusCode: 200,
		}
		if err := state.app.Store.InsertRequest(rec); err != nil {
			t.Fatalf("seed request: %v", err)
		}
	}

	state.page = "/logs"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if len(state.logs.Rows) != 20 {
		t.Fatalf("rows = %d, want one page of 20", len(state.logs.Rows))
	}
	if !tt.HasText("第 1 / 13 页") || !tt.HasText("共 250 条") || !tt.HasText("每页") {
		t.Fatalf("the pager is missing; texts: %v", tt.Texts())
	}
	newest := state.logs.Rows[0].ID

	// 下一页 reads the next slice of history.
	if err := tt.Click("下一页"); err != nil {
		t.Fatalf("click next: %v", err)
	}
	if state.logPage != 2 {
		t.Fatalf("logPage = %d, want 2", state.logPage)
	}
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh page 2: %v", err)
	}
	tt.Frame()
	if len(state.logs.Rows) != 20 || state.logs.Rows[0].ID == newest {
		t.Fatalf("page 2 = %d rows, first id %d; want 20 different rows",
			len(state.logs.Rows), state.logs.Rows[0].ID)
	}
	if !tt.HasText("第 2 / 13 页") {
		t.Fatalf("the indicator stayed on page 1; texts: %v", tt.Texts())
	}

	// 末页 reaches the oldest rows — the 10-row remainder.
	if err := tt.Click("末页"); err != nil {
		t.Fatalf("click last: %v", err)
	}
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh last page: %v", err)
	}
	tt.Frame()
	if state.logPage != 13 || len(state.logs.Rows) != 10 {
		t.Fatalf("last page = %d with %d rows, want 13 with 10", state.logPage, len(state.logs.Rows))
	}
	if err := tt.Click("首页"); err != nil {
		t.Fatalf("click first: %v", err)
	}
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh first page: %v", err)
	}
	if state.logPage != 1 || state.logs.Rows[0].ID != newest {
		t.Fatalf("first page = %d, first id %d; want back to the newest", state.logPage, state.logs.Rows[0].ID)
	}

	// A page past the end (rows deleted under the reader) collapses onto the
	// last page that still has rows instead of showing an empty table.
	state.logPage = 99
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh out of range: %v", err)
	}
	if state.logPage != 13 || len(state.logs.Rows) != 10 {
		t.Fatalf("out-of-range page = %d with %d rows; want clamp to 13 with 10",
			state.logPage, len(state.logs.Rows))
	}

	// A new page size reopens on the first page with the wider window.
	state.setLogPageSize(50)
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh after resize: %v", err)
	}
	tt.Frame()
	if state.logPage != 1 || len(state.logs.Rows) != 50 || !tt.HasText("第 1 / 5 页") {
		t.Fatalf("after resize: page %d with %d rows; want page 1 of 50 rows over 5 pages",
			state.logPage, len(state.logs.Rows))
	}
}

// TestCommandPaletteNavigates pins Cmd+K: it opens the palette, typing filters
// locally, and choosing an entry navigates and closes the palette.
func TestCommandPaletteNavigates(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !state.paletteOpen {
		t.Fatal("Cmd+K should open the palette")
	}

	tt.Type("日志")
	tt.Frame()
	items := state.paletteItems()
	if len(items) != 1 || items[0].Label != "跳转 · 请求日志" {
		t.Fatalf("filtered items = %+v, want exactly the logs page", items)
	}
	if err := tt.Click("跳转 · 请求日志"); err != nil {
		t.Fatalf("choose the entry: %v", err)
	}
	tt.Frame()
	if state.paletteOpen {
		t.Fatal("choosing an entry should close the palette")
	}
	if state.page != "/logs" {
		t.Fatalf("page = %q, want /logs", state.page)
	}
}

// TestShortcutsJumpPages pins Cmd+1..9: they navigate, and they yield while a
// dialog covers the window.
func TestShortcutsJumpPages(t *testing.T) {
	state, tt := newTester(t)

	tt.Frame()
	tt.Key(ui.Cmd, ui.Key2) // the second nav item: 请求日志
	tt.Frame()
	if state.page != "/logs" {
		t.Fatalf("Cmd+2 page = %q, want /logs", state.page)
	}

	// With the provider form open, shortcuts must not act underneath it.
	state.openNewProvider()
	tt.Frame()
	tt.Key(ui.Cmd, ui.Key1)
	tt.Frame()
	if state.page != "/logs" {
		t.Fatalf("a shortcut moved under an open dialog: page = %q", state.page)
	}
	if !state.formOpen {
		t.Fatal("the dialog should stay open")
	}
	state.closeProviderForm()
}

// TestSettingsValidationRejectsBadPort pins the settings guard: an invalid port
// surfaces in the page and never reaches the store; a valid one saves.
func TestSettingsValidationRejectsBadPort(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/settings"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	state.portText = "70000"
	state.saveSettings()
	if state.settingsError == "" {
		t.Fatal("an out-of-range port must surface as a page error")
	}

	state.portText = "3051"
	state.saveSettings()
	if state.settingsError != "" {
		t.Fatalf("a valid port should save; error = %q", state.settingsError)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view, err := state.app.Admin.Settings(context.Background())
		if err != nil {
			t.Fatalf("settings: %v", err)
		}
		if view.Port == 3051 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the valid port was not persisted")
}

// TestHealthRowNavigatesToProvider pins the dashboard link: clicking a health
// row opens the provider page with that row selected.
func TestHealthRowNavigatesToProvider(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if err := tt.Click("OpenAI 主号"); err != nil {
		t.Fatalf("click the health row: %v", err)
	}
	tt.Frame()
	if state.page != "/providers" {
		t.Fatalf("page = %q, want /providers", state.page)
	}
	if state.providerSel != 0 {
		t.Fatalf("providerSel = %d, want 0", state.providerSel)
	}
	if !tt.HasText("供应商 · OpenAI 主号") {
		t.Fatalf("the provider detail should be selected; texts: %v", tt.Texts())
	}
}

// TestSettingsWatchdogSectionAndSave pins the watchdog settings: the section
// shows both timeouts, a valid save lands in the config as milliseconds, an
// invalid one surfaces in the page, and the left/right layout switches
// sections on click.
func TestSettingsWatchdogSectionAndSave(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/settings"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	// The layout: categories on the left, 服务 first on the right.
	if !tt.HasText("监听地址") {
		t.Fatalf("the default section should be 服务; texts: %v", tt.Texts())
	}
	if err := tt.Click("看门狗"); err != nil {
		t.Fatalf("open the watchdog section: %v", err)
	}
	tt.Frame()
	for _, text := range []string{"流式空闲超时（秒）", "非流式空闲超时（秒）", "保存后立即对新的上游请求生效"} {
		if !tt.HasText(text) {
			t.Fatalf("the watchdog section is missing %q; texts: %v", text, tt.Texts())
		}
	}

	// An invalid value stays in the page and never reaches the store.
	state.streamIdleText = "0"
	state.saveSettings()
	if state.settingsError == "" {
		t.Fatal("a zero watchdog must surface as a page error")
	}

	// A valid save persists as milliseconds and clears the dirty flag.
	state.streamIdleText = "45"
	state.nonStreamIdleText = "120"
	state.saveSettings()
	if state.settingsError != "" || state.settingsDirty {
		t.Fatalf("valid save failed: error %q dirty %v", state.settingsError, state.settingsDirty)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view, err := state.app.Admin.Settings(context.Background())
		if err == nil && view.StreamIdleMs == 45000 && view.NonStreamIdleMs == 120000 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the watchdog values were not persisted")
}

// TestEndpointPrefersTheBoundAddress pins the address display: the actually
// bound host/port wins over the configured overview values, so the UI never
// advertises an address that does not answer.
func TestEndpointPrefersTheBoundAddress(t *testing.T) {
	state, _ := newTester(t)

	// No bind info yet: the overview's port, then the loopback default host.
	state.overview.Host = ""
	state.overview.Port = 3050
	if got := state.endpoint(); got != "http://127.0.0.1:3050" {
		t.Fatalf("endpoint = %q, want the loopback default", got)
	}
	state.overview.Host = "0.0.0.0"
	if got := state.endpoint(); got != "http://0.0.0.0:3050" {
		t.Fatalf("endpoint = %q, want the overview host", got)
	}
	// The desktop shell scanned to another port: the bound port wins.
	state.boundHost = "127.0.0.1"
	state.boundPort = 3053
	if got := state.endpoint(); got != "http://127.0.0.1:3053" {
		t.Fatalf("endpoint = %q, want the bound port", got)
	}
}

// TestSidebarCollapsesAndPersists pins the navigation rail: the toggle folds
// it to icons, the choice lands in the settings store, Cmd+\ flips it, and the
// rail still navigates while folded.
func TestSidebarCollapsesAndPersists(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	// The shortcut folds it and the choice persists.
	tt.Key(ui.Cmd, ui.KeyBackslash)
	tt.Frame()
	if !state.sidebarCollapsed {
		t.Fatal("Cmd+\\ should fold the sidebar")
	}
	if v, err := state.app.Store.GetSetting(settingSidebarCollapsed); err != nil || v != "1" {
		t.Fatalf("sidebar setting = %q, %v; want persisted 1", v, err)
	}

	// The rail still navigates: the labeled icon of the first gateway page.
	if err := tt.Click("供应商"); err != nil {
		t.Fatalf("click the folded nav icon: %v", err)
	}
	if state.page != "/providers" {
		t.Fatalf("page = %q, want /providers from the rail", state.page)
	}

	// The shortcut must yield while a dialog covers the window.
	state.openNewProvider()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyBackslash)
	tt.Frame()
	if !state.sidebarCollapsed {
		t.Fatal("Cmd+\\ should not act under an open dialog")
	}
	state.closeProviderForm()
	tt.Frame()

	// The button unfolds it again, and the store agrees.
	if err := tt.Click("展开侧栏"); err != nil {
		t.Fatalf("click expand: %v", err)
	}
	tt.Frame()
	if state.sidebarCollapsed {
		t.Fatal("展开侧栏 should unfold the sidebar")
	}
	if v, err := state.app.Store.GetSetting(settingSidebarCollapsed); err != nil || v != "0" {
		t.Fatalf("sidebar setting = %q, %v; want persisted 0", v, err)
	}
}

// TestRoutableReasonRendersLocalized pins the display-layer mapping: the
// service's English reason never leaks into the interface.
func TestRoutableReasonRendersLocalized(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)
	// A provider without any credential cannot route: the health column and
	// the reason are what this test watches.
	ctx := context.Background()
	if _, err := state.app.Admin.CreateProvider(ctx, admin.ProviderInput{
		Name:    "无凭据供应商",
		Kind:    "command_code",
		BaseURL: "https://api.commandcode.ai",
	}); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	state.page = "/providers"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if tt.HasText("provider has no active credential") {
		t.Fatal("the English reason leaked into the interface")
	}
	if !tt.HasText("供应商无可用凭据") {
		t.Fatalf("the localized reason is missing; texts: %v", tt.Texts())
	}
}
