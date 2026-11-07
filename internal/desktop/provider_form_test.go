package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/gateway"
)

func TestParseHeaders(t *testing.T) {
	headers, err := parseHeaders("x-org: team\nx-custom:1\n\n  x-spaced :  value  ")
	if err != nil {
		t.Fatalf("parseHeaders: %v", err)
	}
	if headers["x-org"] != "team" {
		t.Errorf("x-org = %q", headers["x-org"])
	}
	if headers["x-custom"] != "1" {
		t.Errorf("x-custom = %q", headers["x-custom"])
	}
	if headers["x-spaced"] != "value" {
		t.Errorf("x-spaced = %q", headers["x-spaced"])
	}

	if _, err := parseHeaders("no colon here"); err == nil {
		t.Error("a line without a colon should be rejected")
	}
	if _, err := parseHeaders(": value"); err == nil {
		t.Error("a line without a name should be rejected")
	}

	empty, err := parseHeaders("   \n\n")
	if err != nil || empty != nil {
		t.Fatalf("blank input = %v, %v", empty, err)
	}
}

func TestRenderHeadersRoundTrip(t *testing.T) {
	original := map[string]string{"x-org": "team", "authorization": "custom"}
	text := renderHeaders(original)
	parsed, err := parseHeaders(text)
	if err != nil {
		t.Fatalf("parseHeaders: %v", err)
	}
	for k, want := range original {
		if parsed[k] != want {
			t.Errorf("%s = %q, want %q", k, parsed[k], want)
		}
	}
	if renderHeaders(nil) != "" {
		t.Error("renderHeaders(nil) should be empty")
	}
}

func TestProviderFormCreatesProvider(t *testing.T) {
	state, _ := newTester(t)

	state.openNewProvider()
	if !state.formOpen || !state.formIsNew {
		t.Fatal("the form did not open in create mode")
	}
	state.formProtocol = protocolOpenAIChat
	state.formName = "Form Provider"
	state.formBaseURL = "https://api.example.com"
	state.formProxy = "http://127.0.0.1:7890"
	state.formSecret = "user_0123456789abcdef0123456789abcdef"
	state.formHeaders = "x-org: team\nx-trace: 1"

	state.saveProviderForm()

	provider := waitForProvider(t, state, "Form Provider")
	if provider.Kind != "openai_compatible" {
		t.Errorf("kind = %q", provider.Kind)
	}
	if provider.AccessMode != gateway.AccessAPI {
		t.Errorf("access mode = %q, want api", provider.AccessMode)
	}
	if provider.BaseURL != "https://api.example.com" {
		t.Errorf("base url = %q", provider.BaseURL)
	}
	if provider.ProxyURL != "http://127.0.0.1:7890" {
		t.Errorf("proxy = %q", provider.ProxyURL)
	}
	if provider.ExtraHeaders["x-org"] != "team" || provider.ExtraHeaders["x-trace"] != "1" {
		t.Errorf("headers = %v", provider.ExtraHeaders)
	}

	// A credential was created from the key field.
	creds, err := state.app.Admin.ListCredentials(mustContext(t), provider.ID)
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("credentials = %d, want 1", len(creds))
	}
	if creds[0].SecretMask == "" || creds[0].SecretMask == "user_0123456789abcdef0123456789abcdef" {
		t.Fatalf("the credential is not masked: %q", creds[0].SecretMask)
	}
}

func TestProviderFormCreatesReverseProxy(t *testing.T) {
	state, _ := newTester(t)
	// The adapter catalogue loads on refresh; the reverse group needs it.
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	state.openNewProvider()
	state.setFormAccessMode(gateway.AccessReverseProxy)
	if state.formKind != "command_code" {
		t.Fatalf("form kind = %q, want the first registered reverse proxy", state.formKind)
	}
	state.formName = "CC 主号"
	state.formBaseURL = "https://api.commandcode.ai"
	state.formSecret = "user_0123456789abcdef0123456789abcdef"
	state.saveProviderForm()

	provider := waitForProvider(t, state, "CC 主号")
	if provider.Kind != "command_code" {
		t.Errorf("kind = %q, want command_code", provider.Kind)
	}
	if provider.AccessMode != gateway.AccessReverseProxy {
		t.Errorf("access mode = %q, want reverse_proxy", provider.AccessMode)
	}
	// The form owns only the protocol key of API adapters; a reverse proxy
	// keeps an empty config and runs on the adapter's defaults.
	if len(provider.AdapterConfig) != 0 {
		t.Errorf("adapter config = %v, want empty", provider.AdapterConfig)
	}
}

func TestFieldHintShowsAsTooltip(t *testing.T) {
	state, tt := newTester(t)
	state.openNewProvider()
	tt.Frame()

	const hint = "唯一，用于列表与模型默认对外名"
	if tt.HasText(hint) {
		t.Fatal("the hint must not take layout space before hover")
	}
	r, ok := tt.Find("名称说明")
	if !ok {
		t.Fatalf("the hint icon is missing; texts: %v", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(700 * time.Millisecond)
	if !tt.HasText(hint) {
		t.Fatalf("hovering the icon did not show the hint; texts: %v", tt.Texts())
	}
}

func TestProviderFormActionsLayout(t *testing.T) {
	state, tt := newTester(t)
	state.openNewProvider()
	tt.Frame()

	cancel, ok := tt.Find("取消")
	if !ok {
		t.Fatalf("the cancel button is missing; texts: %v", tt.Texts())
	}
	save, ok := tt.Find("保存")
	if !ok {
		t.Fatalf("the save button is missing; texts: %v", tt.Texts())
	}
	if cancel.X >= save.X {
		t.Errorf("取消 should sit left of 保存: cancel.X=%v save.X=%v", cancel.X, save.X)
	}
	if cancel.Y != save.Y {
		t.Errorf("取消 and 保存 should share one row: %v vs %v", cancel.Y, save.Y)
	}
	// The dialog is centered in the 1280-wide tester window, so a
	// right-aligned action row puts 保存 past the window's center.
	if save.X <= 640 {
		t.Errorf("the action row should be right-aligned: save.X=%v", save.X)
	}
}

func TestProviderFormValidates(t *testing.T) {
	state, _ := newTester(t)

	cases := map[string]func(){
		"名称不能为空": func() {
			state.openNewProvider()
			state.formName = "  "
		},
		"请选择协议": func() {
			state.openNewProvider()
			state.formName = "x"
			state.formProtocol = ""
		},
		"请选择适配器": func() {
			state.openNewProvider()
			state.formName = "x"
			// No refresh ran, so no adapter catalogue is loaded.
			state.setFormAccessMode(gateway.AccessReverseProxy)
			state.formSecret = "user_aaaaaaaaaaaaaaaa"
		},
		"新建供应商需要填写上游密钥": func() {
			state.openNewProvider()
			state.formName = "x"
			state.formProtocol = protocolOpenAIChat
			state.formSecret = ""
		},
		"Header": func() {
			state.openNewProvider()
			state.formName = "x"
			state.formProtocol = protocolOpenAIChat
			state.formSecret = "user_aaaaaaaaaaaaaaaa"
			state.formHeaders = "broken line"
		},
	}
	for want, setup := range cases {
		state.formError = ""
		setup()
		state.saveProviderForm()
		if state.formError == "" {
			t.Fatalf("expected a validation error containing %q", want)
		}
	}
}

func TestProviderFormEditsExisting(t *testing.T) {
	state, _ := newTester(t)
	seedDemoData(t, state)

	// Select the seeded provider and open the edit form.
	state.page = "/providers"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(state.providers) == 0 {
		t.Fatal("no providers seeded")
	}
	state.providerSel = 0
	state.openEditProvider(0)

	if state.formIsNew {
		t.Fatal("the form opened in create mode")
	}
	if state.formName == "" {
		t.Fatal("the form was not filled from the provider")
	}

	state.formName = "Renamed Provider"
	state.formProxy = "socks5://127.0.0.1:1080"
	state.formSecret = "" // keep the stored key
	state.saveProviderForm()

	renamed := waitForProvider(t, state, "Renamed Provider")
	if renamed.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Errorf("proxy = %q", renamed.ProxyURL)
	}
	// The key was not rotated: the store still has exactly one credential.
	creds, err := state.app.Admin.ListCredentials(mustContext(t), renamed.ID)
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("credentials = %d, want the original one only", len(creds))
	}
}

// waitForProvider polls the store until the provider shows up, because saving
// happens off the main thread.
func waitForProvider(t *testing.T, state *uiState, name string) admin.ProviderView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := state.app.Admin.ListProviders(mustContext(t))
		if err != nil {
			t.Fatalf("list providers: %v", err)
		}
		for _, row := range rows {
			if row.Name == name {
				return row
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider %q was never created", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mustContext returns a background context for store calls in tests.
func mustContext(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}
