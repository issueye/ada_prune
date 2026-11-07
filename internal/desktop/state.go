// Package desktop is the native management window.
//
// It draws with mygo's own UI toolkit: no webview, no front-end build, one
// binary. The window talks to the same admin.Service the HTTP API uses, so the
// two can never disagree about a rule.
package desktop

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_proxy/internal/admin"
	"github.com/learningdog1/ada_proxy/internal/app"
	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/provider"
	"github.com/learningdog1/ada_proxy/internal/store"
)

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
	logTable      ui.ListState
	usageTable    ui.ListState
	providerTable ui.ListState
	credTable     ui.ListState
	keyTable      ui.ListState
	endpointTable ui.ListState

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

	// Provider form. formOpen shows the panel; formIsNew decides whether saving
	// creates or updates.
	formOpen           bool
	formIsNew          bool
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
}

// routeMemberDraft is one model row of the route form. Priority stays text so
// invalid input is reported before it reaches the service; weight is preserved
// from an existing member because the desktop form does not edit it.
type routeMemberDraft struct {
	Selected bool
	Priority string
	Weight   int
	Enabled  bool
}

func newUIState(a *app.App) *uiState {
	s := &uiState{
		app:         a,
		page:        "/",
		logEndpoint: "全部",
		logStatus:   "全部",
		usageGroup:  "day",
		usageRange:  "30d",
		providerSel: -1,
		credSel:     -1,
		keySel:      -1,
		logSel:      -1,
	}
	return s
}

// pageTitles is the header of each page.
type pageTitle struct{ Title, Subtitle string }

var pageTitles = map[string]pageTitle{
	"/":             {"总览", "流量、费用与供应商健康度"},
	"/logs":         {"请求日志", "最近的网关调用"},
	"/usage":        {"用量统计", "按天 / 模型 / 供应商 / 密钥的消耗"},
	"/providers":    {"供应商", "上游服务、探活与适配器配置"},
	"/models":       {"模型", "对外模型、上游映射与可路由性"},
	"/route-models": {"路由模型", "把多家供应商的模型聚合成一个对外 ID"},
	"/credentials":  {"凭据", "上游密钥池与额度状态"},
	"/keys":         {"客户端密钥", "下游工具使用的 sk-adp-* 密钥"},
	"/endpoints":    {"端点", "数据面入口"},
	"/settings":     {"设置", "服务与协议参数"},
	"/about":        {"关于", "版本、数据目录与运行状态"},
}

// navItem is one sidebar entry.
type navItem struct {
	Path  string
	Label string
}

// navGroups is the sidebar structure.
var navGroups = []struct {
	Title string
	Items []navItem
}{
	{"监控", []navItem{
		{"/", "总览"},
		{"/logs", "请求日志"},
		{"/usage", "用量统计"},
	}},
	{"网关", []navItem{
		{"/providers", "供应商"},
		{"/models", "模型"},
		{"/route-models", "路由模型"},
		{"/credentials", "凭据"},
		{"/keys", "客户端密钥"},
	}},
	{"系统", []navItem{
		{"/endpoints", "端点"},
		{"/settings", "设置"},
		{"/about", "关于"},
	}},
}

// fetched is one page's data, assembled off the main thread.
type fetched struct {
	overview *admin.Overview
	status   *admin.Status
	lists    *lists
	settings *admin.SettingsView
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
func (s *uiState) reload() { s.load(true) }

// reloadQuiet refreshes without touching the spinner: a poll that blinks reads as
// flicker rather than progress.
func (s *uiState) reloadQuiet() { s.load(false) }

func (s *uiState) load(showSpinner bool) {
	if s.win == nil || s.loading {
		return
	}
	page := s.page
	if showSpinner {
		s.loading = true
	}
	go func() {
		f, err := s.fetch(page)
		// Every mutation of the state happens on the main thread.
		s.win.Update(func() {
			if showSpinner {
				s.loading = false
			}
			if err != nil {
				s.pageErr = err.Error()
				s.flash("加载失败", true)
				return
			}
			s.pageErr = ""
			s.apply(page, f)
		})
	}()
}

// refresh loads synchronously. The view test calls it without a window.
func (s *uiState) refresh() error {
	f, err := s.fetch(s.page)
	if err != nil {
		s.pageErr = err.Error()
		return err
	}
	s.pageErr = ""
	s.apply(s.page, f)
	return nil
}

func (s *uiState) fetch(page string) (fetched, error) {
	var f fetched
	ctx := context.Background()

	ov, err := s.app.Admin.Overview(ctx)
	if err != nil {
		return f, err
	}
	f.overview = &ov

	st, err := s.app.Admin.Status(ctx)
	if err == nil {
		f.status = &st
	}
	// The adapter catalogue drives the provider form's kind list, so every page
	// that can open the form needs it.
	if descriptors, adapterErr := s.app.Admin.Adapters(ctx); adapterErr == nil {
		s.adapters = descriptors
	}

	switch page {
	case "/":
		// The dashboard shows provider health as well as the aggregates.
		rows, err := s.app.Admin.ListProviders(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{providers: rows}

	case "/logs":
		q := admin.LogsQuery{Limit: 200}
		if s.logEndpoint != "" && s.logEndpoint != "全部" {
			q.Endpoint = s.logEndpoint
		}
		if s.logStatus != "" && s.logStatus != "全部" {
			q.Status = s.logStatus
		}
		pageData, err := s.app.Admin.Logs(ctx, q)
		if err != nil {
			return f, err
		}
		f.lists = &lists{logs: pageData}

	case "/usage":
		group := store.UsageGroup(s.usageGroup)
		from := s.usageFrom()
		result, err := s.app.Admin.Usage(ctx, admin.UsageQuery{From: from, GroupBy: group})
		if err != nil {
			return f, err
		}
		// The token trend always needs the daily buckets, whatever the table
		// groups by.
		daily, err := s.app.Admin.Usage(ctx, admin.UsageQuery{From: from, GroupBy: store.GroupByDay})
		if err != nil {
			return f, err
		}
		f.lists = &lists{usage: result, usageDaily: daily}

	case "/providers":
		rows, err := s.app.Admin.ListProviders(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{providers: rows}

	case "/models":
		rows, err := s.app.Admin.ListModels(ctx, admin.ModelFilterInput{Query: s.modelQuery})
		if err != nil {
			return f, err
		}
		// The discovery dialog picks among providers, so the page needs them too.
		provs, err := s.app.Admin.ListProviders(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{models: rows, providers: provs}

	case "/route-models":
		// The form picks members from every provider, so the page loads the
		// whole model catalogue alongside the routes.
		routes, err := s.app.Admin.ListRouteModels(ctx)
		if err != nil {
			return f, err
		}
		models, err := s.app.Admin.ListModels(ctx, admin.ModelFilterInput{})
		if err != nil {
			return f, err
		}
		f.lists = &lists{routeModels: routes, models: models}

	case "/credentials":
		rows, err := s.app.Admin.ListCredentials(ctx, 0)
		if err != nil {
			return f, err
		}
		f.lists = &lists{credentials: rows}

	case "/keys":
		rows, err := s.app.Admin.ListClientKeys(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{clientKeys: rows}

	case "/endpoints":
		rows, err := s.app.Admin.ListEndpoints(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{endpoints: rows}

	case "/settings":
		view, err := s.app.Admin.Settings(ctx)
		if err != nil {
			return f, err
		}
		f.settings = &view
	}
	return f, nil
}

func (s *uiState) apply(page string, f fetched) {
	if f.overview != nil {
		s.overview = *f.overview
	}
	if f.status != nil {
		s.status2 = *f.status
	}
	if f.settings != nil {
		s.settings = *f.settings
		s.portText = strconv.Itoa(f.settings.Port)
		s.hostText = f.settings.Host
		s.levelText = f.settings.LogLevel
		s.retentionText = strconv.Itoa(f.settings.LogRetentionDays)
		s.maxInflightTxt = strconv.FormatInt(f.settings.MaxInflight, 10)
		s.defaultModel = f.settings.DefaultModel
	}
	if f.lists == nil {
		return
	}
	switch page {
	case "/":
		s.providers = f.lists.providers
	case "/logs":
		s.logs = f.lists.logs
		s.logSel = -1
	case "/usage":
		s.usage = f.lists.usage
		s.usageDaily = f.lists.usageDaily
	case "/providers":
		s.providers = f.lists.providers
		if s.providerSel >= len(s.providers) {
			s.providerSel = -1
		}
	case "/models":
		s.models = f.lists.models
		s.providers = f.lists.providers
	case "/route-models":
		s.routeModels = f.lists.routeModels
		s.models = f.lists.models
	case "/credentials":
		s.credentials = f.lists.credentials
		if s.credSel >= len(s.credentials) {
			s.credSel = -1
		}
	case "/keys":
		s.clientKeys = f.lists.clientKeys
		if s.keySel >= len(s.clientKeys) {
			s.keySel = -1
		}
	case "/endpoints":
		s.endpoints = f.lists.endpoints
	}
}

// onPageChange loads the page the user just chose.
func (s *uiState) onPageChange() {
	s.status = ""
	s.isErr = false
	s.usage = store.UsagePage{}
	s.usageDaily = store.UsagePage{}
	s.logs = store.LogsPage{}
	s.reload()
}

// usageFrom is the inclusive start day of the selected window; empty means all
// time. The store buckets by UTC day, so the window is computed in UTC too.
func (s *uiState) usageFrom() string {
	days := s.usageRangeDays()
	if days == 0 {
		return ""
	}
	return time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
}

// usageRangeDays is the window length in days, zero for all time.
func (s *uiState) usageRangeDays() int {
	switch s.usageRange {
	case "7d":
		return 7
	case "30d":
		return 30
	default:
		return 0
	}
}

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

func (s *uiState) toggleProvider(index int, enabled bool) {
	rows := s.providers
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("更新供应商", func() error {
		return s.app.Admin.SetProviderEnabled(context.Background(), id, enabled)
	})
}

func (s *uiState) probeProvider(index int) {
	rows := s.providers
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := s.app.Admin.ProbeProvider(ctx, id)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			switch {
			case err != nil:
				s.flash("探活失败: "+err.Error(), true)
			case result.OK:
				s.flash(fmt.Sprintf("探活成功 (%dms)", result.DurationMs), false)
			default:
				s.flash("探活失败: "+result.Detail, true)
			}
		})
	}()
}

func (s *uiState) deleteProvider(index int) {
	rows := s.providers
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("删除供应商", func() error {
		return s.app.Admin.DeleteProvider(context.Background(), id)
	})
}

func (s *uiState) toggleModel(index int, enabled bool) {
	rows := s.models
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("更新模型", func() error {
		return s.app.Admin.SetModelEnabled(context.Background(), id, enabled)
	})
}

func (s *uiState) deleteModel(index int) {
	rows := s.models
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("删除模型", func() error {
		return s.app.Admin.DeleteModel(context.Background(), id)
	})
}

// ---- model form ----

// openModelForm resets and opens the manual add dialog, preselecting the
// provider the dialog used last (or the first one).
func (s *uiState) openModelForm() {
	s.modelFormOpen = true
	s.modelFormIsNew = true
	s.modelFormID = 0
	s.modelFormError = ""
	s.modelFormSaving = false
	s.modelFormUpstream = ""
	s.modelFormDisplay = ""
	s.modelFormPublicID = ""
	s.modelFormProtocol = ""
	s.modelFormContext = ""
	s.modelFormMaxTokens = ""
	s.modelFormInputPrice = ""
	s.modelFormCachePrice = ""
	s.modelFormOutputPrice = ""
	s.modelFormVision = false
	s.modelFormTools = true
	s.modelFormEnabled = true
	if len(s.providers) == 0 {
		return
	}
	for _, p := range s.providers {
		if p.ID == s.modelFormProviderID {
			return
		}
	}
	s.modelFormProviderID = s.providers[0].ID
}

// openEditModel fills the dialog from the chosen card. The public id is
// preserved on save, so clients holding that id keep routing to the model.
func (s *uiState) openEditModel(index int) {
	rows := s.models
	if index < 0 || index >= len(rows) {
		return
	}
	m := rows[index]
	s.modelFormOpen = true
	s.modelFormIsNew = false
	s.modelFormID = m.ID
	s.modelFormProviderID = m.ProviderID
	s.modelFormError = ""
	s.modelFormSaving = false
	s.modelFormUpstream = m.UpstreamName
	s.modelFormDisplay = m.DisplayName
	s.modelFormPublicID = m.PublicName
	s.modelFormProtocol = m.UpstreamProtocol
	s.modelFormContext = numberText(float64(m.ContextWindow))
	s.modelFormMaxTokens = numberText(float64(m.MaxTokens))
	s.modelFormInputPrice = numberText(m.InputPrice)
	s.modelFormCachePrice = numberText(m.CachedInputPrice)
	s.modelFormOutputPrice = numberText(m.OutputPrice)
	s.modelFormVision = m.SupportsVision
	s.modelFormTools = m.SupportsTools
	s.modelFormEnabled = m.Enabled
}

// modelFormProvider returns the provider chosen in the dialog.
func (s *uiState) modelFormProvider() *admin.ProviderView {
	for i := range s.providers {
		if s.providers[i].ID == s.modelFormProviderID {
			return &s.providers[i]
		}
	}
	return nil
}

// modelFormProviderName names the provider chosen in the dialog.
func (s *uiState) modelFormProviderName() string {
	if p := s.modelFormProvider(); p != nil {
		return p.Name
	}
	return ""
}

// modelFormPublicName is the id the dialog previews: the provider-namespaced
// upstream name, exactly what a client sends back in the model field. Editing
// keeps the stored id instead of recomputing it.
func (s *uiState) modelFormPublicName() string {
	if !s.modelFormIsNew && s.modelFormPublicID != "" {
		return s.modelFormPublicID
	}
	upstream := strings.TrimSpace(s.modelFormUpstream)
	p := s.modelFormProvider()
	if p == nil {
		return upstream
	}
	return p.Name + "/" + upstream
}

// saveModelForm creates a new model or updates the edited one.
func (s *uiState) saveModelForm() {
	if s.modelFormSaving {
		return
	}
	upstream := strings.TrimSpace(s.modelFormUpstream)
	if upstream == "" {
		s.modelFormError = "上游模型名不能为空"
		return
	}
	contextWindow, err := parseOptionalInt("上下文", s.modelFormContext)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	maxTokens, err := parseOptionalInt("最大输出", s.modelFormMaxTokens)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	inputPrice, err := parseOptionalPrice("输入价格", s.modelFormInputPrice)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	cachePrice, err := parseOptionalPrice("缓存价格", s.modelFormCachePrice)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	outputPrice, err := parseOptionalPrice("输出价格", s.modelFormOutputPrice)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}

	p := s.modelFormProvider()
	if p == nil {
		s.modelFormError = "还没有供应商，请先新建供应商"
		return
	}

	vision, tools, enabled := s.modelFormVision, s.modelFormTools, s.modelFormEnabled
	in := admin.ModelInput{
		ProviderID:       p.ID,
		DisplayName:      strings.TrimSpace(s.modelFormDisplay),
		UpstreamName:     upstream,
		ContextWindow:    contextWindow,
		MaxTokens:        maxTokens,
		InputPrice:       inputPrice,
		CachedInputPrice: cachePrice,
		OutputPrice:      outputPrice,
		SupportsVision:   &vision,
		SupportsTools:    &tools,
		Enabled:          &enabled,
	}
	if s.modelFormIsNew {
		// Only the three expressible protocols are sent; a vendor adapter leaves
		// it empty so the service falls back to vendor_native (same as import).
		switch proto := protocolOf(*p); proto {
		case protocolAnthropic, protocolOpenAIChat, protocolOpenAIResponses:
			in.UpstreamProtocol = proto
		}
	} else {
		// Preserve the id and the protocol the model was created with.
		in.PublicName = s.modelFormPublicID
		in.UpstreamProtocol = s.modelFormProtocol
	}

	isNew, id := s.modelFormIsNew, s.modelFormID
	s.modelFormError = ""
	s.modelFormSaving = true
	go func() {
		var publicName string
		var saveErr error
		if isNew {
			created, err := s.app.Admin.CreateModel(context.Background(), in)
			saveErr, publicName = err, created.PublicName
		} else {
			updated, err := s.app.Admin.UpdateModel(context.Background(), id, in)
			saveErr, publicName = err, updated.PublicName
		}
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.modelFormSaving = false
			if saveErr != nil {
				s.modelFormError = saveErr.Error()
				return
			}
			s.modelFormOpen = false
			label := "模型已添加："
			if !isNew {
				label = "模型已保存："
			}
			s.flash(label+publicName, false)
			s.modelFormUpstream = ""
			s.modelFormDisplay = ""
			s.reload()
		})
	}()
}

// ---- route model form ----

// openRouteForm starts a new route model. Every model starts unselected so the
// operator sees the whole catalogue and picks deliberately.
func (s *uiState) openRouteForm() {
	s.routeFormOpen = true
	s.routeFormIsNew = true
	s.routeFormID = 0
	s.routeFormName = ""
	s.routeFormStrategy = store.StrategyPriority
	s.routeFormRemark = ""
	s.routeFormEnabled = true
	s.routeFormError = ""
	s.routeFormSaving = false
	s.routeFormMembers = make(map[int64]*routeMemberDraft, len(s.models))
	for _, m := range s.models {
		s.routeFormMembers[m.ID] = &routeMemberDraft{Priority: "0", Weight: 1, Enabled: true}
	}
}

// openEditRoute fills the form from an existing route, preserving member order
// priority, weight and each member's enabled flag.
func (s *uiState) openEditRoute(index int) {
	rows := s.routeModels
	if index < 0 || index >= len(rows) {
		return
	}
	r := rows[index]
	s.routeFormOpen = true
	s.routeFormIsNew = false
	s.routeFormID = r.ID
	s.routeFormName = r.Name
	s.routeFormStrategy = r.Strategy
	s.routeFormRemark = r.Remark
	s.routeFormEnabled = r.Enabled
	s.routeFormError = ""
	s.routeFormSaving = false
	s.routeFormMembers = make(map[int64]*routeMemberDraft, len(s.models))
	for _, m := range s.models {
		s.routeFormMembers[m.ID] = &routeMemberDraft{Priority: "0", Weight: 1, Enabled: true}
	}
	for _, member := range r.Members {
		draft, ok := s.routeFormMembers[member.ModelID]
		if !ok {
			continue
		}
		draft.Selected = true
		draft.Priority = strconv.Itoa(member.Priority)
		draft.Weight = member.Weight
		draft.Enabled = member.Enabled
	}
}

// routeMemberCount counts the selected members of the open form.
func (s *uiState) routeMemberCount() int {
	count := 0
	for _, draft := range s.routeFormMembers {
		if draft.Selected {
			count++
		}
	}
	return count
}

// saveRouteForm validates the draft and creates or updates the route model.
func (s *uiState) saveRouteForm() {
	if s.routeFormSaving {
		return
	}
	name := strings.TrimSpace(s.routeFormName)
	if name == "" {
		s.routeFormError = "路由模型 ID 不能为空"
		return
	}

	members := make([]admin.RouteMemberInput, 0, len(s.routeFormMembers))
	for _, m := range s.models {
		draft := s.routeFormMembers[m.ID]
		if draft == nil || !draft.Selected {
			continue
		}
		priority, err := parseOptionalInt("优先级", draft.Priority)
		if err != nil {
			s.routeFormError = fmt.Sprintf("%s（%s）", err, m.PublicName)
			return
		}
		weight := draft.Weight
		if weight <= 0 {
			weight = 1
		}
		enabled := draft.Enabled
		members = append(members, admin.RouteMemberInput{
			ModelID:  m.ID,
			Priority: priority,
			Weight:   weight,
			Enabled:  &enabled,
		})
	}
	if len(members) == 0 {
		s.routeFormError = "至少选择一个成员模型"
		return
	}

	strategy := s.routeFormStrategy
	if strategy == "" {
		strategy = store.StrategyPriority
	}
	enabled := s.routeFormEnabled
	in := admin.RouteModelInput{
		Name:     name,
		Strategy: strategy,
		Enabled:  &enabled,
		Remark:   strings.TrimSpace(s.routeFormRemark),
		Members:  members,
	}

	isNew, id := s.routeFormIsNew, s.routeFormID
	s.routeFormError = ""
	s.routeFormSaving = true
	go func() {
		var saveErr error
		if isNew {
			_, saveErr = s.app.Admin.CreateRouteModel(context.Background(), in)
		} else {
			_, saveErr = s.app.Admin.UpdateRouteModel(context.Background(), id, in)
		}
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.routeFormSaving = false
			if saveErr != nil {
				s.routeFormError = saveErr.Error()
				return
			}
			s.routeFormOpen = false
			label := "路由模型已创建："
			if !isNew {
				label = "路由模型已保存："
			}
			s.flash(label+name, false)
			s.reload()
		})
	}()
}

// routeInput rebuilds an update payload from a stored route, changing only the
// enabled flag. Member enabled states and weights are preserved.
func (s *uiState) routeInput(r store.RouteModel, enabled bool) admin.RouteModelInput {
	members := make([]admin.RouteMemberInput, 0, len(r.Members))
	for _, m := range r.Members {
		memberEnabled := m.Enabled
		members = append(members, admin.RouteMemberInput{
			ModelID:  m.ModelID,
			Priority: m.Priority,
			Weight:   m.Weight,
			Enabled:  &memberEnabled,
		})
	}
	return admin.RouteModelInput{
		Name:     r.Name,
		Strategy: r.Strategy,
		Enabled:  &enabled,
		Remark:   r.Remark,
		Members:  members,
	}
}

func (s *uiState) toggleRoute(index int, enabled bool) {
	rows := s.routeModels
	if index < 0 || index >= len(rows) {
		return
	}
	in := s.routeInput(rows[index], enabled)
	id := rows[index].ID
	s.run("更新路由模型", func() error {
		_, err := s.app.Admin.UpdateRouteModel(context.Background(), id, in)
		return err
	})
}

func (s *uiState) deleteRoute(index int) {
	rows := s.routeModels
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("删除路由模型", func() error {
		return s.app.Admin.DeleteRouteModel(context.Background(), id)
	})
}

// routeRoutable reports whether any enabled member can serve right now: the
// model is enabled, its provider is enabled and it has an active credential.
func (s *uiState) routeRoutable(r store.RouteModel) bool {
	if !r.Enabled {
		return false
	}
	routable := make(map[int64]bool, len(s.models))
	for _, m := range s.models {
		routable[m.ID] = m.Routable
	}
	for _, member := range r.Members {
		if member.Enabled && routable[member.ModelID] {
			return true
		}
	}
	return false
}

// parseOptionalInt reads a blank-or-number field; empty means zero.
func parseOptionalInt(field, raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s必须是不小于 0 的整数", field)
	}
	return value, nil
}

// parseOptionalPrice reads a blank-or-number price; empty means zero.
func parseOptionalPrice(field, raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s必须是不小于 0 的数字", field)
	}
	return value, nil
}

// numberText renders a number for an editable text field: zero is blank.
func numberText(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ---- model discovery ----

// discoverProviderName names the dialog's selected provider, for the public-id
// preview of each catalogue row.
func (s *uiState) discoverProviderName() string {
	for _, p := range s.providers {
		if p.ID == s.discoverProviderID {
			return p.Name
		}
	}
	return ""
}

// discoverableProviders lists providers whose adapter can enumerate models.
func (s *uiState) discoverableProviders() []admin.ProviderView {
	kinds := make(map[string]bool, len(s.adapters))
	for _, d := range s.adapters {
		if d.Capabilities.ModelDiscovery {
			kinds[string(d.Kind)] = true
		}
	}
	var out []admin.ProviderView
	for _, p := range s.providers {
		if kinds[p.Kind] {
			out = append(out, p)
		}
	}
	return out
}

// openDiscover resets the dialog and picks the first provider that can list
// models.
func (s *uiState) openDiscover() {
	s.discoverOpen = true
	s.discoverLoading = false
	s.discoverImporting = false
	s.discoverError = ""
	s.discoverSummary = ""
	s.discoverRows = nil
	s.discoverChecked = map[string]bool{}
	if rows := s.discoverableProviders(); len(rows) > 0 {
		found := false
		for _, p := range rows {
			if p.ID == s.discoverProviderID {
				found = true
				break
			}
		}
		if !found {
			s.discoverProviderID = rows[0].ID
		}
	}
}

// fetchDiscovered asks the chosen provider's adapter for its upstream catalogue.
func (s *uiState) fetchDiscovered() {
	id := s.discoverProviderID
	if id == 0 || s.discoverLoading {
		return
	}
	s.discoverLoading = true
	s.discoverError = ""
	s.discoverSummary = ""
	s.discoverRows = nil
	s.discoverChecked = map[string]bool{}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		result, err := s.app.Admin.DiscoverModels(ctx, id)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.discoverLoading = false
			if err != nil {
				s.discoverError = err.Error()
				return
			}
			s.discoverRows = result.Rows
		})
	}()
}

// importProtocol is the upstream protocol imported models inherit: the
// provider's configured one, or empty so the service falls back to the
// adapter's native protocol.
func (s *uiState) importProtocol() string {
	for _, p := range s.providers {
		if p.ID != s.discoverProviderID {
			continue
		}
		switch proto := protocolOf(p); proto {
		case protocolAnthropic, protocolOpenAIChat, protocolOpenAIResponses:
			return proto
		}
		return ""
	}
	return ""
}

// importDiscovered creates the checked models under the chosen provider.
func (s *uiState) importDiscovered() {
	if s.discoverImporting {
		return
	}
	protocol := s.importProtocol()
	var rows []admin.ModelImportRow
	for _, r := range s.discoverRows {
		if r.Exists || !s.discoverChecked[r.UpstreamName] {
			continue
		}
		rows = append(rows, admin.ModelImportRow{
			UpstreamName:     r.UpstreamName,
			DisplayName:      r.DisplayName,
			UpstreamProtocol: protocol,
		})
	}
	if len(rows) == 0 {
		return
	}
	id := s.discoverProviderID
	s.discoverImporting = true
	s.discoverError = ""
	s.discoverSummary = ""
	go func() {
		result, err := s.app.Admin.ImportModels(context.Background(), id, rows)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.discoverImporting = false
			if err != nil {
				s.discoverError = err.Error()
				return
			}
			s.discoverSummary = fmt.Sprintf("新建 %d 个 · 跳过 %d 个 · 失败 %d 个",
				len(result.Created), len(result.Skipped), len(result.Failed))
			created := make(map[string]bool, len(result.Created))
			for _, m := range result.Created {
				created[m.UpstreamName] = true
			}
			for i := range s.discoverRows {
				if created[s.discoverRows[i].UpstreamName] {
					s.discoverRows[i].Exists = true
					delete(s.discoverChecked, s.discoverRows[i].UpstreamName)
				}
			}
			s.reloadQuiet()
		})
	}()
}

func (s *uiState) setCredentialStatus(index int, status string) {
	rows := s.credentials
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("更新凭据状态", func() error {
		return s.app.Admin.SetCredentialStatus(context.Background(), id, status)
	})
}

func (s *uiState) pinCredential(index int) {
	rows := s.credentials
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	pinned := rows[index].Pinned
	s.run("更新固定凭据", func() error {
		if pinned {
			return s.app.Admin.PinCredential(context.Background(), 0)
		}
		return s.app.Admin.PinCredential(context.Background(), id)
	})
}

func (s *uiState) deleteCredential(index int) {
	rows := s.credentials
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("删除凭据", func() error {
		return s.app.Admin.DeleteCredential(context.Background(), id)
	})
}

func (s *uiState) createClientKey() {
	name := strings.TrimSpace(s.newKeyName)
	if name == "" {
		s.flash("请先填写密钥名称", true)
		return
	}
	go func() {
		created, err := s.app.Admin.CreateClientKey(context.Background(), admin.ClientKeyInput{Name: name})
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.flash("创建失败: "+err.Error(), true)
				return
			}
			s.createdToken = created.Token
			s.newKeyName = ""
			s.flash("已创建，明文只显示这一次", false)
			s.reloadQuiet()
		})
	}()
}

func (s *uiState) rotateClientKey(index int) {
	rows := s.clientKeys
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	go func() {
		token, err := s.app.Admin.RotateClientKey(context.Background(), id)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.flash("轮换失败: "+err.Error(), true)
				return
			}
			s.createdToken = token
			s.flash("已轮换，旧密钥立即失效", false)
			s.reloadQuiet()
		})
	}()
}

func (s *uiState) deleteClientKey(index int) {
	rows := s.clientKeys
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("删除密钥", func() error {
		return s.app.Admin.DeleteClientKey(context.Background(), id)
	})
}

func (s *uiState) saveSettings() {
	patch := map[string]any{
		"logLevel":         s.levelText,
		"defaultModel":     strings.TrimSpace(s.defaultModel),
		"host":             strings.TrimSpace(s.hostText),
		"logRetentionDays": atoiDefault(s.retentionText, s.settings.LogRetentionDays),
	}
	if port := atoiDefault(s.portText, s.settings.Port); port > 0 {
		patch["port"] = port
	}
	if inflight, err := strconv.ParseInt(strings.TrimSpace(s.maxInflightTxt), 10, 64); err == nil {
		patch["maxInflight"] = inflight
	}
	go func() {
		result, err := s.app.Admin.SaveSettings(context.Background(), patch)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.flash("保存失败: "+err.Error(), true)
				return
			}
			if len(result.RestartRequired) > 0 {
				s.flash("已保存，需重启生效: "+strings.Join(result.RestartRequired, ", "), true)
				return
			}
			s.flash("设置已保存", false)
			s.reloadQuiet()
		})
	}()
}

// ---- provider form ----

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
func protocolForLabel(label string) string {
	for _, p := range providerProtocols {
		if protocolLabel(p) == label {
			return p
		}
	}
	return label
}

// protocolLabels returns the display names for the form's select.
func protocolLabels(protocols []string) []string {
	out := make([]string, 0, len(protocols))
	for _, p := range protocols {
		out = append(out, protocolLabel(p))
	}
	return out
}

// kindForProtocol maps a protocol onto the adapter that speaks it.
func kindForProtocol(protocol string) string {
	switch protocol {
	case protocolAnthropic:
		return "anthropic"
	case protocolOpenAIChat, protocolOpenAIResponses:
		return "openai_compatible"
	default:
		return ""
	}
}

// protocolOf reads the protocol a provider speaks.
//
// Anthropic has its own adapter; the OpenAI family shares one adapter and records
// the configured protocol in adapter_config.
func protocolOf(p admin.ProviderView) string {
	switch p.Kind {
	case "anthropic":
		return protocolAnthropic
	case "openai_compatible":
		if v, ok := p.AdapterConfig["protocol"].(string); ok {
			switch v {
			case protocolOpenAIChat, protocolOpenAIResponses:
				return v
			}
		}
		return protocolOpenAIChat
	default:
		// Other adapter kinds (a vendor-specific one, say) have no protocol the
		// form can express; show the kind itself.
		return p.Kind
	}
}

// providerProtocolLabel is the protocol column's text: the protocol's display
// name for the three form protocols, the registry label for vendor adapters.
func (s *uiState) providerProtocolLabel(p admin.ProviderView) string {
	proto := protocolOf(p)
	switch proto {
	case protocolAnthropic, protocolOpenAIChat, protocolOpenAIResponses:
		return protocolLabel(proto)
	}
	for _, d := range s.adapters {
		if string(d.Kind) == p.Kind {
			return d.Label
		}
	}
	return proto
}

// editableProtocol reports whether the form may change this provider's protocol.
func editableProtocol(p admin.ProviderView) bool {
	// Staying inside the OpenAI adapter keeps stored models valid; crossing to
	// another adapter would not.
	return p.Kind == "openai_compatible"
}

// accessModeLabel names an access mode for the operator.
func accessModeLabel(mode gateway.AccessMode) string {
	if mode == gateway.AccessReverseProxy {
		return "反代"
	}
	return "API"
}

// reverseProxyAdapters lists the registered adapters classified as reverse
// proxies, for the form's type-grouped choice.
func (s *uiState) reverseProxyAdapters() []provider.Descriptor {
	var out []provider.Descriptor
	for _, d := range s.adapters {
		if d.AccessMode == gateway.AccessReverseProxy {
			out = append(out, d)
		}
	}
	return out
}

// setFormAccessMode switches the form's type group. Reverse proxies pick their
// adapter directly (the first registered one by default); API providers derive
// the kind from the protocol at save time.
func (s *uiState) setFormAccessMode(mode gateway.AccessMode) {
	s.formAccessMode = mode
	if mode == gateway.AccessReverseProxy {
		s.formKind = ""
		if adapters := s.reverseProxyAdapters(); len(adapters) > 0 {
			s.formKind = string(adapters[0].Kind)
		}
	}
}

// openNewProvider resets the form for a new provider.
func (s *uiState) openNewProvider() {
	s.formOpen = true
	s.formIsNew = true
	s.formError = ""
	s.formName = ""
	s.formBaseURL = ""
	s.formProxy = ""
	s.formSecret = ""
	s.formHeaders = ""
	s.formEnabled = true
	s.formCredentialID = 0
	s.formCredentialMask = ""
	s.formAccessMode = gateway.AccessAPI
	s.formProtocol = protocolAnthropic
	s.formKind = kindForProtocol(s.formProtocol)
}

// openEditProvider fills the form from a provider and its primary credential.
func (s *uiState) openEditProvider(index int) {
	if index < 0 || index >= len(s.providers) {
		return
	}
	p := s.providers[index]
	s.formOpen = true
	s.formIsNew = false
	s.formError = ""
	s.formKind = p.Kind
	s.formAccessMode = p.AccessMode
	s.formName = p.Name
	s.formBaseURL = p.BaseURL
	s.formProxy = p.ProxyURL
	s.formEnabled = p.Enabled
	s.formSecret = ""
	s.formCredentialID = 0
	s.formCredentialMask = ""
	s.formHeaders = renderHeaders(p.ExtraHeaders)

	// The form edits the credential that routing would use first.
	go func() {
		rows, err := s.app.Admin.ListCredentials(context.Background(), p.ID)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				return
			}
			for _, row := range rows {
				if row.Status == store.CredentialActive && row.Enabled {
					s.formCredentialID = row.ID
					s.formCredentialMask = row.SecretMask
					return
				}
			}
			if len(rows) > 0 {
				s.formCredentialID = rows[0].ID
				s.formCredentialMask = rows[0].SecretMask
			}
		})
	}()
}

func (s *uiState) closeProviderForm() {
	s.formOpen = false
	s.formError = ""
}

// saveProviderForm validates the form and writes the provider and its key.
func (s *uiState) saveProviderForm() {
	name := strings.TrimSpace(s.formName)
	baseURL := strings.TrimSpace(s.formBaseURL)
	protocol := strings.TrimSpace(s.formProtocol)
	proxy := strings.TrimSpace(s.formProxy)

	if name == "" {
		s.formError = "名称不能为空"
		return
	}

	// The adapter is derived from the protocol. A provider created with another
	// adapter (a vendor-specific one) keeps its kind and skips the protocol
	// choice: its wire format is not one of the three.
	var (
		kind         string
		existing     *admin.ProviderView
		keepProtocol bool
	)
	if s.formIsNew {
		if s.formAccessMode == gateway.AccessReverseProxy {
			kind = s.formKind
			if kind == "" {
				s.formError = "请选择适配器"
				return
			}
		} else {
			kind = kindForProtocol(protocol)
			if kind == "" {
				s.formError = "请选择协议"
				return
			}
		}
	} else {
		existing = s.selectedProviderView()
		if existing == nil {
			s.formError = "未选中供应商"
			return
		}
		kind = existing.Kind
		if !editableProtocol(*existing) {
			// Nothing to choose: the protocol is whatever the adapter speaks.
			protocol = protocolOf(*existing)
			keepProtocol = true
		}
	}
	headers, headerErr := parseHeaders(s.formHeaders)
	if headerErr != nil {
		s.formError = headerErr.Error()
		return
	}
	if s.formIsNew && strings.TrimSpace(s.formSecret) == "" {
		s.formError = "新建供应商需要填写上游密钥"
		return
	}

	// Keep the adapter's other settings; only the protocol key is owned by the
	// form.
	adapterConfig := map[string]any{}
	if existing != nil {
		for k, v := range existing.AdapterConfig {
			adapterConfig[k] = v
		}
	}
	if kind == "openai_compatible" && !keepProtocol {
		adapterConfig["protocol"] = protocol
	}

	enabled := s.formEnabled
	input := admin.ProviderInput{
		Name:          name,
		Kind:          kind,
		BaseURL:       baseURL,
		Enabled:       &enabled,
		ProxyURL:      proxy,
		ExtraHeaders:  headers,
		AdapterConfig: adapterConfig,
	}

	secret := strings.TrimSpace(s.formSecret)
	credentialID := s.formCredentialID
	isNew := s.formIsNew
	providerID := int64(0)
	if !isNew {
		// The kind is immutable; the service rejects a change anyway.
		providerID = s.selectedProviderID()
	}

	go func() {
		ctx := context.Background()
		var err error
		if isNew {
			created, createErr := s.app.Admin.CreateProvider(ctx, input)
			if createErr != nil {
				err = createErr
			} else {
				_, err = s.app.Admin.CreateCredential(ctx, admin.CredentialInput{
					ProviderID: created.ID,
					Name:       "默认",
					Secret:     secret,
				})
			}
		} else {
			if providerID <= 0 {
				err = fmt.Errorf("no provider selected")
			} else {
				_, err = s.app.Admin.UpdateProvider(ctx, providerID, input)
				if err == nil && secret != "" && !secretboxLooksMasked(secret) {
					if credentialID > 0 {
						_, err = s.app.Admin.UpdateCredential(ctx, credentialID, admin.CredentialInput{
							Name:   "默认",
							Secret: secret,
						})
					} else {
						_, err = s.app.Admin.CreateCredential(ctx, admin.CredentialInput{
							ProviderID: providerID,
							Name:       "默认",
							Secret:     secret,
						})
					}
				}
			}
		}

		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.formError = err.Error()
				return
			}
			s.formOpen = false
			s.formError = ""
			s.flash("供应商已保存", false)
			s.reload()
		})
	}()
}

// selectedProviderView returns the provider chosen in the table, if any.
func (s *uiState) selectedProviderView() *admin.ProviderView {
	if s.providerSel >= 0 && s.providerSel < len(s.providers) {
		return &s.providers[s.providerSel]
	}
	return nil
}

// selectedProviderID returns the id of the provider chosen in the table.
func (s *uiState) selectedProviderID() int64 {
	if s.providerSel >= 0 && s.providerSel < len(s.providers) {
		return s.providers[s.providerSel].ID
	}
	return 0
}

// renderHeaders turns a header map into the form's "Key: Value" lines.
func renderHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(headers[k])
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseHeaders reads "Key: Value" lines into a map.
func parseHeaders(text string) (map[string]string, error) {
	out := map[string]string{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("第 %d 行缺少冒号，应为 “Key: Value”", i+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("第 %d 行缺少 Header 名称", i+1)
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// secretboxLooksMasked reports whether a value is a mask rather than a new key.
func secretboxLooksMasked(v string) bool {
	return strings.Contains(v, "…")
}

// sortStrings sorts in place without pulling sort into the view code.
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// router is exposed for the about page.
func (s *uiState) router() *gateway.Router { return s.app.Router }

func atoiDefault(text string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return fallback
	}
	return n
}
