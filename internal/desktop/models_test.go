package desktop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_proxy/internal/admin"
)

// TestModelCardActions pins the card grid's interaction model: every card
// carries the mapping and a dropdown with the enable/disable action, and
// choosing it writes through to the store.
func TestModelCardActions(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	for _, text := range []string{"deepseek/deepseek-v4-flash", "gpt-5.6-luna", "OpenAI 主号", "OpenAI Chat", "可路由", "已启用"} {
		if !tt.HasText(text) {
			t.Fatalf("card content %q is missing; texts: %v", text, tt.Texts())
		}
	}

	// The first card's dropdown disables its model.
	if err := tt.Click("更多"); err != nil {
		t.Fatalf("click card actions: %v", err)
	}
	tt.Frame()
	if err := tt.Click("停用"); err != nil {
		t.Fatalf("choose menu item: %v", err)
	}
	tt.Frame()

	// The action writes off the main thread; without a window the result lands
	// only in the store, so poll a refresh for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.models) > 0 && !state.models[0].Enabled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(state.models) == 0 || state.models[0].Enabled {
		t.Fatal("choosing 停用 should disable the first model")
	}
}

// TestModelCardDeletesModel pins the dropdown's delete action: it removes the
// model through the service and the grid drops it after the refresh.
func TestModelCardDeletesModel(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if len(state.models) == 0 {
		t.Fatal("no models seeded")
	}
	upstream := state.models[0].UpstreamName

	if err := tt.Click("更多"); err != nil {
		t.Fatalf("click card actions: %v", err)
	}
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("choose delete: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		found := false
		for _, m := range state.models {
			if m.UpstreamName == upstream {
				found = true
				break
			}
		}
		if !found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("choosing 删除 should remove %q", upstream)
}

// TestModelEditDialog pins the edit flow: the dialog opens prefilled, saves the
// context window, prices and capabilities, and keeps the public id stable.
func TestModelEditDialog(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if len(state.models) == 0 {
		t.Fatal("no models seeded")
	}
	original := state.models[0]

	if err := tt.Click("更多"); err != nil {
		t.Fatalf("click card actions: %v", err)
	}
	tt.Frame()
	if err := tt.Click("编辑"); err != nil {
		t.Fatalf("choose edit: %v", err)
	}
	tt.Frame()

	if !state.modelFormOpen || state.modelFormIsNew {
		t.Fatalf("dialog open = %v, isNew = %v, want edit mode", state.modelFormOpen, state.modelFormIsNew)
	}
	for _, text := range []string{"编辑模型", "上下文", "最大输出", "输入价格", "缓存价格", "输出价格", "支持识图（视觉）"} {
		if !tt.HasText(text) {
			t.Fatalf("edit dialog is missing %q; texts: %v", text, tt.Texts())
		}
	}
	if !tt.HasText(original.PublicName) {
		t.Fatalf("the edited model's id %q should be prefilled; texts: %v", original.PublicName, tt.Texts())
	}

	state.modelFormContext = "64000"
	state.modelFormMaxTokens = "32000"
	state.modelFormInputPrice = "2"
	state.modelFormCachePrice = "0.5"
	state.modelFormOutputPrice = "3"
	state.modelFormVision = true
	state.modelFormTools = false
	tt.Frame()

	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		for _, m := range state.models {
			if m.ID != original.ID {
				continue
			}
			if m.ContextWindow == 64000 && m.MaxTokens == 32000 &&
				m.InputPrice == 2 && m.CachedInputPrice == 0.5 && m.OutputPrice == 3 &&
				m.SupportsVision && !m.SupportsTools {
				if m.PublicName != original.PublicName {
					t.Fatalf("public name changed to %q, want %q", m.PublicName, original.PublicName)
				}
				return
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the edit was not persisted; models: %+v", state.models)
}

// TestModelDiscoveryImport walks the discovery dialog: the service lists the
// upstream /models catalogue and marks known models; the dialog imports the
// checked ones under the provider's protocol.
func TestModelDiscoveryImport(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-test-alpha","owned_by":"test"},{"id":"gpt-test-beta","owned_by":"test"}]}`)
	}))
	defer upstream.Close()

	state, tt := newTester(t)
	ctx := context.Background()
	p, err := state.app.Admin.CreateProvider(ctx, admin.ProviderInput{
		Name:          "测试上游",
		Kind:          "openai_compatible",
		BaseURL:       upstream.URL,
		AdapterConfig: map[string]any{"protocol": protocolOpenAIChat},
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := state.app.Admin.CreateCredential(ctx, admin.CredentialInput{
		ProviderID: p.ID,
		Name:       "默认",
		Secret:     "sk-test-0123456789abcdef",
	}); err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if _, err := state.app.Admin.CreateModel(ctx, admin.ModelInput{
		ProviderID:       p.ID,
		UpstreamName:     "gpt-test-alpha",
		UpstreamProtocol: protocolOpenAIChat,
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	// The service lists the catalogue and marks the already-imported model.
	result, err := state.app.Admin.DiscoverModels(ctx, p.ID)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("discovered %d rows, want 2", len(result.Rows))
	}
	for _, row := range result.Rows {
		want := row.UpstreamName == "gpt-test-alpha"
		if row.Exists != want {
			t.Fatalf("row %q Exists = %v, want %v", row.UpstreamName, row.Exists, want)
		}
	}

	// Open the dialog from the toolbar.
	if err := tt.Click("从上游拉取"); err != nil {
		t.Fatalf("open discovery: %v", err)
	}
	tt.Frame()
	if !state.discoverOpen {
		t.Fatal("the discovery dialog should be open")
	}
	if state.discoverProviderID != p.ID {
		t.Fatalf("dialog provider = %d, want %d", state.discoverProviderID, p.ID)
	}
	if !tt.HasText("拉取模型列表") {
		t.Fatalf("fetch button is missing; texts: %v", tt.Texts())
	}

	// The fetch applies its result through the window; without one, seed the
	// rows the same way the window would.
	state.discoverRows = result.Rows
	tt.Frame()
	if !tt.HasText("已导入") {
		t.Fatalf("the known model should be marked; texts: %v", tt.Texts())
	}
	if !tt.HasText("测试上游/gpt-test-beta") {
		t.Fatalf("the row should preview the namespaced public id; texts: %v", tt.Texts())
	}

	// Check the new model and import it.
	if err := tt.Click("gpt-test-beta"); err != nil {
		t.Fatalf("check model: %v", err)
	}
	tt.Frame()
	if state.discoverCheckedCount() != 1 {
		t.Fatalf("checked count = %d, want 1", state.discoverCheckedCount())
	}
	if err := tt.Click("导入所选（1）"); err != nil {
		t.Fatalf("import: %v", err)
	}
	tt.Frame()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.models) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(state.models) != 2 {
		t.Fatalf("import should create the second model, got %d", len(state.models))
	}
	for _, m := range state.models {
		if m.UpstreamName == "gpt-test-beta" {
			if m.UpstreamProtocol != protocolOpenAIChat {
				t.Fatalf("imported protocol = %q, want the provider's %q", m.UpstreamProtocol, protocolOpenAIChat)
			}
			if m.PublicName != "测试上游/gpt-test-beta" {
				t.Fatalf("imported public name = %q, want the provider-namespaced id", m.PublicName)
			}
			return
		}
	}
	t.Fatal("gpt-test-beta was not imported")
}

// TestModelCardCopiesPublicName pins the card's copy button: it writes the
// model's public id — the string a client sends as the model field — to the
// clipboard and confirms with a flash.
func TestModelCardCopiesPublicName(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if len(state.models) == 0 {
		t.Fatal("no models seeded")
	}
	if err := tt.Click("复制"); err != nil {
		t.Fatalf("click copy: %v", err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != state.models[0].PublicName {
		t.Fatalf("clipboard = %q, want %q", got, state.models[0].PublicName)
	}
	if state.status != "已复制到剪贴板" {
		t.Fatalf("status = %q, want copy confirmation", state.status)
	}
}

// TestModelManualAddDialog pins the manual add flow: the dialog previews the
// provider-namespaced public id, copies it, and saving creates the model with
// that exact id.
func TestModelManualAddDialog(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if err := tt.Click("添加模型"); err != nil {
		t.Fatalf("open model dialog: %v", err)
	}
	tt.Frame()
	if !state.modelFormOpen {
		t.Fatal("the model dialog should be open")
	}
	if !tt.HasText("对外模型 ID") {
		t.Fatalf("the public id field is missing; texts: %v", tt.Texts())
	}

	// With only the upstream name typed, the preview is provider/upstream.
	state.modelFormUpstream = "gpt-new"
	tt.Frame()
	if !tt.HasText("OpenAI 主号/gpt-new") {
		t.Fatalf("the preview should show the namespaced id; texts: %v", tt.Texts())
	}

	// The dialog's copy button writes the same id the card copies.
	if err := tt.Click("复制 ID"); err != nil {
		t.Fatalf("click copy id: %v", err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != "OpenAI 主号/gpt-new" {
		t.Fatalf("clipboard = %q, want the namespaced id", got)
	}

	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.models) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, m := range state.models {
		if m.UpstreamName != "gpt-new" {
			continue
		}
		if m.PublicName != "OpenAI 主号/gpt-new" {
			t.Fatalf("public name = %q, want the namespaced id", m.PublicName)
		}
		if m.UpstreamProtocol != protocolOpenAIChat {
			t.Fatalf("protocol = %q, want the provider's %q", m.UpstreamProtocol, protocolOpenAIChat)
		}
		return
	}
	t.Fatalf("gpt-new was not created; models: %+v", state.models)
}

// TestModelDiscoveryEmptyOptions checks the dialog's guidance when no provider
// can list models.
func TestModelDiscoveryEmptyOptions(t *testing.T) {
	state, tt := newTester(t)

	state.page = "/models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if err := tt.Click("从上游拉取"); err != nil {
		t.Fatalf("open discovery: %v", err)
	}
	tt.Frame()
	if !tt.HasText("没有支持模型发现的供应商") {
		t.Fatalf("empty guidance is missing; texts: %v", tt.Texts())
	}

	// Escape closes the modal.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if state.discoverOpen {
		t.Fatal("Escape should close the discovery dialog")
	}
}
