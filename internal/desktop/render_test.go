package desktop

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/store"
)

// TestRenderPages writes a PNG of each page to build/ui/ so the layout can be
// inspected without opening a window. It asserts nothing about pixels; it exists
// so "the UI looks wrong" can be answered by looking.
//
// Runs only when RENDER_UI=1, because it writes files.
func TestRenderPages(t *testing.T) {
	if os.Getenv("RENDER_UI") == "" {
		t.Skip("set RENDER_UI=1 to render page screenshots")
	}

	state, tt := newTester(t)
	// The desktop window hides its title bar, so the controls take room on the
	// right (140 DIPs for three buttons on Windows).
	tt.SetTitleBar(ui.TitleBar{Height: 40, Left: 0, Right: 140})

	seedDemoData(t, state)
	// One route model so the aggregation page has a card to lay out.
	models, err := state.app.Admin.ListModels(context.Background(), admin.ModelFilterInput{})
	if err != nil {
		t.Fatalf("seed models: %v", err)
	}
	if _, err := state.app.Admin.CreateRouteModel(context.Background(), admin.RouteModelInput{
		Name:     "fast",
		Strategy: "priority",
		Members: []admin.RouteMemberInput{
			{ModelID: models[0].ID, Priority: 2},
			{ModelID: models[1].ID, Priority: 1},
		},
	}); err != nil {
		t.Fatalf("seed route model: %v", err)
	}
	// A reverse-proxy provider so the list shows both access-mode badges.
	if _, err := state.app.Admin.CreateProvider(context.Background(), admin.ProviderInput{
		Name:    "Command Code 主号",
		Kind:    "command_code",
		BaseURL: "https://api.commandcode.ai",
	}); err != nil {
		t.Fatalf("seed reverse proxy provider: %v", err)
	}

	out := filepath.Join("..", "..", "build", "ui")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("create output: %v", err)
	}

	pages := map[string]string{
		"/":             "dashboard",
		"/logs":         "logs",
		"/usage":        "usage",
		"/providers":    "providers",
		"/models":       "models",
		"/route-models": "route-models",
		"/credentials":  "credentials",
		"/keys":         "keys",
		"/endpoints":    "endpoints",
		"/settings":     "settings",
		"/about":        "about",
	}
	for path, name := range pages {
		state.page = path
		if err := state.refresh(); err != nil {
			t.Fatalf("%s: refresh: %v", path, err)
		}
		tt.Frame()

		file, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatalf("%s: create: %v", name, err)
		}
		if err := png.Encode(file, tt.Image()); err != nil {
			_ = file.Close()
			t.Fatalf("%s: encode: %v", name, err)
		}
		_ = file.Close()
	}
	t.Logf("rendered %d pages to %s", len(pages), out)

	// The provider form and detail panel are states, not pages: render them too.
	state.page = "/providers"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh providers: %v", err)
	}
	state.providerSel = 0
	state.formOpen = false
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "providers-detail.png"))

	// The row action dropdown is a window overlay: render it open too.
	if err := tt.Click("更多"); err != nil {
		t.Fatalf("open row actions: %v", err)
	}
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "providers-actions.png"))
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	state.openNewProvider()
	state.formProtocol = protocolOpenAIChat
	state.formName = "新供应商"
	state.formBaseURL = "https://api.example.com"
	state.formProxy = "http://127.0.0.1:7890"
	state.formHeaders = "x-org: my-team\nx-custom: 1"
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "providers-form.png"))

	// A field hint shown as a tooltip: hover the info icon past the delay.
	if r, ok := tt.Find("名称说明"); ok {
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Advance(700 * time.Millisecond)
		writePNG(t, tt, filepath.Join(out, "providers-form-tooltip.png"))
		tt.Move(0, 0)
		tt.Advance(700 * time.Millisecond)
	}
	state.closeProviderForm()

	// The same form with the reverse-proxy group selected.
	state.openNewProvider()
	state.setFormAccessMode(gateway.AccessReverseProxy)
	state.formName = "Command Code 主号"
	state.formBaseURL = "https://api.commandcode.ai"
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "providers-form-reverse.png"))
	state.closeProviderForm()

	// The discovery dialog with a fetched catalogue: seed the rows directly so
	// the render never touches the network.
	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh models: %v", err)
	}
	state.openDiscover()
	state.discoverRows = []admin.DiscoveredModelView{
		{DiscoveredModel: gateway.DiscoveredModel{UpstreamName: "deepseek/deepseek-v4-flash", DisplayName: "deepseek/deepseek-v4-flash"}, Exists: true},
		{DiscoveredModel: gateway.DiscoveredModel{UpstreamName: "gpt-5.6-luna", DisplayName: "gpt-5.6-luna"}, Exists: true},
		{DiscoveredModel: gateway.DiscoveredModel{UpstreamName: "gpt-5.6-terra", DisplayName: "gpt-5.6-terra"}},
		{DiscoveredModel: gateway.DiscoveredModel{UpstreamName: "gpt-5.6-sol", DisplayName: "gpt-5.6-sol"}},
	}
	state.discoverChecked = map[string]bool{"gpt-5.6-terra": true}
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "models-discover.png"))
	state.discoverOpen = false

	// The add and edit dialogs share one form: render both.
	state.openModelForm()
	state.modelFormUpstream = "gpt-5.6-terra"
	state.modelFormDisplay = "GPT-5.6 Terra"
	state.modelFormContext = "128000"
	state.modelFormMaxTokens = "64000"
	state.modelFormInputPrice = "0.5"
	state.modelFormCachePrice = "0.1"
	state.modelFormOutputPrice = "1.5"
	state.modelFormVision = true
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "models-form.png"))
	state.modelFormOpen = false

	state.openEditModel(0)
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "models-edit.png"))
	state.modelFormOpen = false

	// The route form with two checked members and their priorities.
	state.page = "/route-models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh routes: %v", err)
	}
	state.openRouteForm()
	state.routeFormName = "fast-mixture"
	if len(state.models) > 1 {
		state.routeFormMembers[state.models[0].ID].Selected = true
		state.routeFormMembers[state.models[0].ID].Priority = "2"
		state.routeFormMembers[state.models[1].ID].Selected = true
		state.routeFormMembers[state.models[1].ID].Priority = "1"
	}
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "route-form.png"))
	state.routeFormOpen = false

	// The one-time token banner with its copy button; the token is fake.
	state.page = "/keys"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh keys: %v", err)
	}
	state.createdToken = "sk-prune-0123456789abcdef0123456789abcdef"
	tt.Frame()
	writePNG(t, tt, filepath.Join(out, "keys-created.png"))
}

// writePNG saves the current frame.
func writePNG(t *testing.T, tt *ui.Tester, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	if err := png.Encode(file, tt.Image()); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// seedDemoData fills the temp database so tables have rows to lay out.
func seedDemoData(t *testing.T, state *uiState) {
	t.Helper()
	ctx := context.Background()
	svc := state.app.Admin

	provider, err := svc.CreateProvider(ctx, admin.ProviderInput{
		Name:    "OpenAI 主号",
		Kind:    "openai_compatible",
		BaseURL: "https://api.openai.com",
		AdapterConfig: map[string]any{
			"protocol": protocolOpenAIChat,
		},
	})
	if err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := svc.CreateCredential(ctx, admin.CredentialInput{
		ProviderID: provider.ID,
		Name:       "主号",
		Secret:     "sk-demo-0123456789abcdef0123456789",
	}); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	model, err := svc.CreateModel(ctx, admin.ModelInput{
		ProviderID:       provider.ID,
		DisplayName:      "DeepSeek V4 Flash",
		UpstreamName:     "deepseek/deepseek-v4-flash",
		UpstreamProtocol: "openai_chat",
		InputPrice:       0.5,
		OutputPrice:      1.5,
		ContextWindow:    128000,
		MaxTokens:        64000,
		SupportsVision:   boolPtr(true),
	})
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := svc.CreateModel(ctx, admin.ModelInput{
		ProviderID:       provider.ID,
		DisplayName:      "GPT-5.6 Luna",
		UpstreamName:     "gpt-5.6-luna",
		UpstreamProtocol: "openai_chat",
	}); err != nil {
		t.Fatalf("seed second model: %v", err)
	}
	key, err := svc.CreateClientKey(ctx, admin.ClientKeyInput{Name: "codex-local", QuotaTokens: 100000})
	if err != nil {
		t.Fatalf("seed client key: %v", err)
	}
	_ = key

	// A handful of request rows so the log and usage tables have content.
	now := time.Now().UnixMilli()
	providerID := provider.ID
	keyID := int64(1)
	for i := 0; i < 6; i++ {
		status := int64(200)
		errorType := ""
		if i == 4 {
			status, errorType = 502, "upstream_stream_error"
		}
		if i == 5 {
			status, errorType = 401, "authentication_error"
		}
		rec := store.RequestRecord{
			TS:              now - int64(i)*90_000,
			Endpoint:        "/v1/chat/completions",
			InboundProtocol: "openai_chat",
			ProviderID:      &providerID,
			ProviderKind:    "command_code",
			Model:           model.PublicName,
			ClientKeyID:     &keyID,
			StatusCode:      status,
			Stream:          i%2 == 0,
			DurationMs:      int64(1200 + i*300),
			TTFTMs:          int64(300 + i*40),
			InputTokens:     int64(1200 + i*100),
			OutputTokens:    int64(180 + i*10),
			CachedTokens:    int64(800),
			Cost:            0.004 + float64(i)/1000,
			FinishReason:    "stop",
			ErrorType:       errorType,
		}
		if err := state.app.Store.InsertRequest(rec); err != nil {
			t.Fatalf("seed request: %v", err)
		}
		if err := state.app.Store.RecordUsage(rec.TS, providerID, rec.Model, keyID,
			rec.InputTokens, rec.OutputTokens, rec.CachedTokens, rec.Cost); err != nil {
			t.Fatalf("seed usage: %v", err)
		}
	}
}

func boolPtr(v bool) *bool { return &v }
