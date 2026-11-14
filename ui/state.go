package ui

import (
	"github.com/egoist/mygo"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/app"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/provider"
	"github.com/learningdog1/ada_prune/internal/store"
	"time"
)

// uiState is the window's state. The view reads it on the main thread only;
// background work posts changes through Window.Update.
// uiState is the window's state. The view reads it on the main thread only;
// background work posts changes through Window.Update.
// uiState is the window's state. The view reads it on the main thread only;
// background work posts changes through Window.Update.
// uiState is the window's state. The view reads it on the main thread only;
// background work posts changes through Window.Update.
type uiState struct {
	app *app.App
	win *mygo.Window

	page    string
	loading bool
	status  string
	isErr   bool

	overview admin.Overview
	status2  admin.Status
	pageErr  string

	providers   []admin.ProviderView
	models      []admin.ModelView
	routeModels []store.RouteModel
	credentials []store.Credential
	clientKeys  []store.ClientKey
	endpoints   []store.Endpoint
	logs        store.LogsPage
	usage       store.UsagePage
	usageDaily  store.UsagePage
	settings    admin.SettingsView
	adapters    []provider.Descriptor

	// Selection per table, -1 for none.
	providerSel int
	credSel     int
	keySel      int
	logSel      int

	// Table state (scroll, column widths, chosen row).
	logTable      gui.ListState
	usageTable    gui.ListState
	providerTable gui.ListState
	credTable     gui.ListState
	keyTable      gui.ListState
	endpointTable gui.ListState

	// Filters.
	logEndpoint string
	logStatus   string
	usageGroup  string
	usageRange  string
	modelQuery  string

	// Forms.
	newKeyName    string
	createdToken  string
	settingsDirty bool
	settingsError string

	// Destructive-action confirmation. confirmAction runs only when the
	// dialog's confirm button is chosen; Escape or Cancel drops it.
	confirmOpen    bool
	confirmTitle   string
	confirmMessage string
	confirmLabel   string
	confirmAction  func()

	// Command palette (Cmd+K): one search box over pages and global actions.
	paletteOpen  bool
	paletteQuery string

	// Request log paging and the live tail the background poll honors. The
	// tail only follows page 1: an older page must not shift under the reader
	// as new requests land.
	logPage     int
	logPageSize int
	liveTail    bool

	// The whole model catalogue, for the search box's local filtering.
	modelsAll []admin.ModelView

	// Monotonic counter of background loads; only the newest one may apply,
	// so a slow fetch of a page the user already left cannot win.
	loadSeq uint64

	// The address the gateway actually bound at startup. The configured port
	// can drift: the desktop shell scans forward when the default is taken.
	boundHost string
	boundPort int

	// sidebarCollapsed folds the navigation to an icon rail. The choice
	// persists across restarts in the settings store.
	sidebarCollapsed bool

	// dark selects the night palette ("值班模式"). It is a rendering choice,
	// persisted next to the sidebar preference.
	dark bool

	// Provider form. formOpen shows the panel; formIsNew decides whether saving
	// creates or updates.
	formOpen           bool
	formIsNew          bool
	formProviderID     int64
	formKind           string
	formAccessMode     gateway.AccessMode
	formProtocol       string
	formName           string
	formBaseURL        string
	formProxy          string
	formSecret         string
	formHeaders        string
	formEnabled        bool
	formError          string
	formCredentialID   int64
	formCredentialMask string

	// Model form: add one model by hand, or edit an existing one. The dialog
	// previews the public id — {provider}/{upstream} — the client sends in the
	// model field; editing keeps that id stable.
	modelFormOpen        bool
	modelFormIsNew       bool
	modelFormID          int64
	modelFormProviderID  int64
	modelFormUpstream    string
	modelFormDisplay     string
	modelFormPublicID    string
	modelFormProtocol    string
	modelFormContext     string
	modelFormMaxTokens   string
	modelFormInputPrice  string
	modelFormCachePrice  string
	modelFormOutputPrice string
	modelFormVision      bool
	modelFormTools       bool
	modelFormEnabled     bool
	modelFormError       string
	modelFormSaving      bool

	// Route model form: a public name plus the models it aggregates. Members
	// may come from different providers; the order is set per member.
	routeFormOpen     bool
	routeFormIsNew    bool
	routeFormID       int64
	routeFormName     string
	routeFormStrategy string
	routeFormRemark   string
	routeFormEnabled  bool
	routeFormMembers  map[int64]*routeMemberDraft
	routeFormError    string
	routeFormSaving   bool

	// Model discovery dialog: pick a provider, list its upstream catalogue,
	// choose what to import.
	discoverOpen       bool
	discoverProviderID int64
	discoverLoading    bool
	discoverImporting  bool
	discoverError      string
	discoverSummary    string
	discoverRows       []admin.DiscoveredModelView
	discoverChecked    map[string]bool

	portText       string
	hostText       string
	levelText      string
	retentionText  string
	maxInflightTxt string
	defaultModel   string
	// Watchdog timeouts in seconds; the config stores milliseconds.
	streamIdleText    string
	nonStreamIdleText string
	// settingsSection is the left-hand category the settings page shows.
	settingsSection string
}

// routeMemberDraft is one model row of the route form. Priority stays text so
// invalid input is reported before it reaches the service; weight is preserved
// from an existing member because the desktop form does not edit it.
// routeMemberDraft is one model row of the route form. Priority stays text so
// invalid input is reported before it reaches the service; weight is preserved
// from an existing member because the desktop form does not edit it.
// routeMemberDraft is one model row of the route form. Priority stays text so
// invalid input is reported before it reaches the service; weight is preserved
// from an existing member because the desktop form does not edit it.
// routeMemberDraft is one model row of the route form. Priority stays text so
// invalid input is reported before it reaches the service; weight is preserved
// from an existing member because the desktop form does not edit it.
type routeMemberDraft struct {
	Selected bool
	Priority string
	Weight   int
	Enabled  bool
}

// logPageSizeOptions are the page sizes the log pager offers, smallest first.
var logPageSizeOptions = []string{"20", "50", "100", "200"}

func newUIState(a *app.App) *uiState {
	s := &uiState{
		app:         a,
		page:        "/",
		logEndpoint: "全部",
		logStatus:   "全部",
		logPage:     1,
		logPageSize: 20,
		liveTail:    true,
		usageGroup:  "day",
		usageRange:  "30d",
		providerSel: -1,
		credSel:     -1,
		keySel:      -1,
		logSel:      -1,
	}
	// The collapsed sidebar survives restarts; a missing or unreadable
	// setting just means the default (expanded).
	if v, err := a.Store.GetSetting(settingSidebarCollapsed); err == nil && v == "1" {
		s.sidebarCollapsed = true
	}
	if v, err := a.Store.GetSetting(settingTheme); err == nil && v == "dark" {
		s.dark = true
	}
	return s
}

// settingTheme stores the light/dark choice between runs.
const settingTheme = "gui.theme"

// toggleTheme flips between the paper and night palettes and remembers it.
func (s *uiState) toggleTheme() {
	s.dark = !s.dark
	value := "light"
	if s.dark {
		value = "dark"
	}
	if err := s.app.Store.SetSetting(settingTheme, value); err != nil {
		s.flash("无法保存界面偏好: "+err.Error(), true)
	}
	if s.win != nil {
		s.win.Update(func() {})
	}
}

// settingSidebarCollapsed stores the sidebar's folded state between runs.
const settingSidebarCollapsed = "gui.sidebarCollapsed"

// toggleSidebar folds or unfolds the navigation rail and remembers the choice.
func (s *uiState) toggleSidebar() {
	s.sidebarCollapsed = !s.sidebarCollapsed
	value := "0"
	if s.sidebarCollapsed {
		value = "1"
	}
	if err := s.app.Store.SetSetting(settingSidebarCollapsed, value); err != nil {
		s.flash("无法保存界面偏好: "+err.Error(), true)
	}
}

// pageTitles is the header of each page.
type pageTitle struct{ Kicker, Title, Subtitle string }

var pageTitles = map[string]pageTitle{
	"/":             {"监控 · 总览", "总览", "流量、费用与供应商健康度"},
	"/logs":         {"监控 · 请求日志", "请求日志", "最近的网关调用"},
	"/usage":        {"监控 · 用量统计", "用量统计", "按天 / 模型 / 供应商 / 密钥的消耗"},
	"/providers":    {"网关 · 上游", "供应商", "上游服务、探活与适配器配置"},
	"/models":       {"网关 · 模型目录", "模型", "对外模型、上游映射与可路由性"},
	"/route-models": {"网关 · 路由模型", "路由模型", "把多家供应商的模型聚合成一个对外 ID"},
	"/credentials":  {"网关 · 上游凭据", "凭据", "上游密钥池与额度状态"},
	"/keys":         {"网关 · 访问", "客户端密钥", "下游工具使用的 sk-prune-* 密钥"},
	"/endpoints":    {"系统 · 端点", "端点", "数据面入口"},
	"/settings":     {"系统 · 设置", "设置", "服务与协议参数"},
	"/about":        {"系统 · 关于", "关于", "版本、数据目录与运行状态"},
}

// navItem is one sidebar entry. Icon is the stroke glyph beside the label;
// it is a lazy parse so the SVGs build on first draw, not at init.
// navItem is one sidebar entry. Icon is the stroke glyph beside the label;
// it is a lazy parse so the SVGs build on first draw, not at init.
// navItem is one sidebar entry. Icon is the stroke glyph beside the label;
// it is a lazy parse so the SVGs build on first draw, not at init.
// navItem is one sidebar entry. Icon is the stroke glyph beside the label;
// it is a lazy parse so the SVGs build on first draw, not at init.
type navItem struct {
	Path  string
	Label string
	Icon  func() *gui.SVG
}

// navGroups is the sidebar structure.
var navGroups = []struct {
	Title string
	Items []navItem
}{
	{"监控", []navItem{
		{"/", "总览", iconDashboard},
		{"/logs", "请求日志", iconLogs},
		{"/usage", "用量统计", iconUsage},
	}},
	{"网关", []navItem{
		{"/providers", "供应商", iconProviders},
		{"/models", "模型", iconModels},
		{"/route-models", "路由模型", iconRoutes},
		{"/credentials", "凭据", iconCredentials},
		{"/keys", "客户端密钥", iconKeys},
	}},
	{"系统", []navItem{
		{"/endpoints", "端点", iconEndpoints},
		{"/settings", "设置", iconSettings},
		{"/about", "关于", iconAbout},
	}},
}

// fetched is one page's data, assembled off the main thread.
type fetched struct {
	overview *admin.Overview
	status   *admin.Status
	lists    *lists
	settings *admin.SettingsView
	adapters []provider.Descriptor
	// logPageFix carries a corrected log page when the fetched offset fell
	// past the end of the log (rows deleted under the reader).
	logPageFix int
}

type lists struct {
	providers   []admin.ProviderView
	models      []admin.ModelView
	routeModels []store.RouteModel
	credentials []store.Credential
	clientKeys  []store.ClientKey
	endpoints   []store.Endpoint
	logs        store.LogsPage
	usage       store.UsagePage
	usageDaily  store.UsagePage
}

// reload fetches the current page with the header spinner.

// reloadQuiet refreshes without touching the spinner: a poll that blinks reads as
// flicker rather than progress.
// reloadQuiet refreshes without touching the spinner: a poll that blinks reads as
// flicker rather than progress.

// refresh loads synchronously. The view test calls it without a window.

// onPageChange loads the page the user just chose.
func (s *uiState) onPageChange() {
	s.status = ""
	s.isErr = false
	// The log tail restarts at its newest page on every fresh visit.
	s.logPage = 1
	if s.page != "/keys" {
		// The one-time plaintext never outlives its page: leaving /keys wipes
		// it, so a secret cannot sit unnoticed in a panel the user forgot.
		s.createdToken = ""
	}
	s.usage = store.UsagePage{}
	s.usageDaily = store.UsagePage{}
	s.logs = store.LogsPage{}
	s.reload()
}

// navigate switches pages programmatically — shortcuts, the palette, in-page
// links — and loads the target, the same path a sidebar click takes.
// navigate switches pages programmatically — shortcuts, the palette, in-page
// links — and loads the target, the same path a sidebar click takes.
// navigate switches pages programmatically — shortcuts, the palette, in-page
// links — and loads the target, the same path a sidebar click takes.
// navigate switches pages programmatically — shortcuts, the palette, in-page
// links — and loads the target, the same path a sidebar click takes.
func (s *uiState) navigate(path string) {
	if path == s.page {
		return
	}
	s.page = path
	s.onPageChange()
}

// navItems flattens the sidebar groups in display order; the number shortcuts
// and the palette both address pages by this order.
// navItems flattens the sidebar groups in display order; the number shortcuts
// and the palette both address pages by this order.
// navItems flattens the sidebar groups in display order; the number shortcuts
// and the palette both address pages by this order.
// navItems flattens the sidebar groups in display order; the number shortcuts
// and the palette both address pages by this order.
func navItems() []navItem {
	var out []navItem
	for _, group := range navGroups {
		out = append(out, group.Items...)
	}
	return out
}

// dialogOpen reports whether a modal or confirmation covers the window: window
// shortcuts must not act on the page underneath it.
// dialogOpen reports whether a modal or confirmation covers the window: window
// shortcuts must not act on the page underneath it.
// dialogOpen reports whether a modal or confirmation covers the window: window
// shortcuts must not act on the page underneath it.
// dialogOpen reports whether a modal or confirmation covers the window: window
// shortcuts must not act on the page underneath it.
func (s *uiState) dialogOpen() bool {
	return s.formOpen || s.modelFormOpen || s.discoverOpen || s.routeFormOpen ||
		s.paletteOpen || s.confirmOpen
}

// confirm asks before a destructive action runs. The label names the confirm
// button (删除, 轮换, …); Escape or Cancel drops the action.
// confirm asks before a destructive action runs. The label names the confirm
// button (删除, 轮换, …); Escape or Cancel drops the action.
// confirm asks before a destructive action runs. The label names the confirm
// button (删除, 轮换, …); Escape or Cancel drops the action.
// confirm asks before a destructive action runs. The label names the confirm
// button (删除, 轮换, …); Escape or Cancel drops the action.
func (s *uiState) confirm(title, message, label string, action func()) {
	s.confirmOpen = true
	s.confirmTitle = title
	s.confirmMessage = message
	s.confirmLabel = label
	s.confirmAction = action
}

// filterModels applies the search box locally: the page holds the whole
// catalogue, so typing filters in memory with no round trip.

// logTotalPages is the page count of the current log filter and page size, at
// least one.

// setLogPageSize adopts a chosen page size and returns to the first page, so
// the new window always opens on the newest rows.

// moveLogPage flips to the given log page (1-based) and reloads. The pager
// disables out-of-range moves, but the clamp stays as a guard.

// usageFrom is the inclusive start day of the selected window; empty means all
// time. The store buckets by UTC day, so the window is computed in UTC too.

// usageRangeDays is the window length in days, zero for all time.

// flash shows a transient status pill in the header.
func (s *uiState) flash(message string, isErr bool) {
	s.status = message
	s.isErr = isErr
	if s.win == nil {
		return
	}
	win := s.win
	go func() {
		time.Sleep(3 * time.Second)
		win.Update(func() {
			if s.status == message {
				s.status = ""
			}
		})
	}()
}

// ---- actions ----
//
// Each action runs off the main thread and re-applies its result on the main
// thread. They all report through flash so a failure is never silent.

func (s *uiState) run(label string, fn func() error) {
	go func() {
		err := fn()
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.flash(label+"失败: "+err.Error(), true)
				return
			}
			s.flash(label+"成功", false)
			s.reloadQuiet()
		})
	}()
}

// ---- model form ----

// openModelForm resets and opens the manual add dialog, preselecting the
// provider the dialog used last (or the first one).

// openEditModel fills the dialog from the chosen card. The public id is
// preserved on save, so clients holding that id keep routing to the model.

// modelFormProvider returns the provider chosen in the dialog.

// modelFormProviderName names the provider chosen in the dialog.

// modelFormPublicName is the id the dialog previews: the provider-namespaced
// upstream name, exactly what a client sends back in the model field. Editing
// keeps the stored id instead of recomputing it.

// saveModelForm creates a new model or updates the edited one.

// ---- route model form ----

// openRouteForm starts a new route model. Every model starts unselected so the
// operator sees the whole catalogue and picks deliberately.

// openEditRoute fills the form from an existing route, preserving member order
// priority, weight and each member's enabled flag.

// routeMemberCount counts the selected members of the open form.

// saveRouteForm validates the draft and creates or updates the route model.

// routeInput rebuilds an update payload from a stored route, changing only the
// enabled flag. Member enabled states and weights are preserved.

// routeRoutable reports whether any enabled member can serve right now: the
// model is enabled, its provider is enabled and it has an active credential.

// parseOptionalInt reads a blank-or-number field; empty means zero.

// parseOptionalPrice reads a blank-or-number price; empty means zero.

// numberText renders a number for an editable text field: zero is blank.

// ---- model discovery ----

// discoverProviderName names the dialog's selected provider, for the public-id
// preview of each catalogue row.

// discoverableProviders lists providers whose adapter can enumerate models.

// openDiscover resets the dialog and picks the first provider that can list
// models.

// fetchDiscovered asks the chosen provider's adapter for its upstream catalogue.

// importProtocol is the upstream protocol imported models inherit: the
// provider's configured one, or empty so the service falls back to the
// adapter's native protocol.

// importDiscovered creates the checked models under the chosen provider.

// copyClientKey decrypts the stored plaintext onto the clipboard. It runs on
// the main thread on purpose: the reveal is one indexed read plus a decrypt,
// and the clipboard write needs the render context.

// ---- provider form ----

// Protocols a provider can speak. The form offers these instead of adapter kinds:
// an operator thinks in protocols, and the kind is an implementation detail the
// gateway picks.
// Protocols a provider can speak. The form offers these instead of adapter kinds:
// an operator thinks in protocols, and the kind is an implementation detail the
// gateway picks.
// Protocols a provider can speak. The form offers these instead of adapter kinds:
// an operator thinks in protocols, and the kind is an implementation detail the
// gateway picks.
// Protocols a provider can speak. The form offers these instead of adapter kinds:
// an operator thinks in protocols, and the kind is an implementation detail the
// gateway picks.
const (
	protocolAnthropic       = "anthropic"
	protocolOpenAIChat      = "openai_chat"
	protocolOpenAIResponses = "openai_responses"
)

// providerProtocols is the form's option list, in display order.
var providerProtocols = []string{protocolAnthropic, protocolOpenAIChat, protocolOpenAIResponses}

// protocolLabel names a protocol for the operator.
func protocolLabel(protocol string) string {
	switch protocol {
	case protocolAnthropic:
		return "Anthropic Messages"
	case protocolOpenAIChat:
		return "OpenAI Chat"
	case protocolOpenAIResponses:
		return "OpenAI Responses"
	default:
		return protocol
	}
}

// protocolForLabel reverses protocolLabel, for the select's display values.

// protocolLabels returns the display names for the form's select.

// kindForProtocol maps a protocol onto the adapter that speaks it.

// protocolOf reads the protocol a provider speaks.
//
// Anthropic has its own adapter; the OpenAI family shares one adapter and records
// the configured protocol in adapter_config.

// providerProtocolLabel is the protocol column's text: the protocol's display
// name for the three form protocols, the registry label for vendor adapters.

// editableProtocol reports whether the form may change this provider's protocol.

// accessModeLabel names an access mode for the operator.

// reverseProxyAdapters lists the registered adapters classified as reverse
// proxies, for the form's type-grouped choice.

// setFormAccessMode switches the form's type group. Reverse proxies pick their
// adapter directly (the first registered one by default); API providers derive
// the kind from the protocol at save time.

// openNewProvider resets the form for a new provider.

// openEditProvider fills the form from a provider and its primary credential.
// The form remembers the provider by id, and the table selection follows it:
// a row action can be used without clicking the row first, and the rest of the
// dialog must still describe the row whose menu was opened.

// saveProviderForm validates the form and writes the provider and its key.

// formProviderView returns the provider the open form edits, if any. The form
// holds an id rather than the table index, so a background refresh that
// reorders the list cannot make it edit the wrong row.

// renderHeaders turns a header map into the form's "Key: Value" lines.

// parseHeaders reads "Key: Value" lines into a map.

// secretboxLooksMasked reports whether a value is a mask rather than a new key.

// sortStrings sorts in place without pulling sort into the view code.

// router is exposed for the about page.
func (s *uiState) router() *gateway.Router { return s.app.Router }
