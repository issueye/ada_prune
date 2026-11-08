package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/learningdog1/ada_prune/internal/admin"
)

// createRouteTestModel seeds a provider with one credential and one model, the
// minimum a route member needs to be routable.
func createRouteTestModel(t *testing.T, state *uiState, providerName, upstream string) admin.ModelView {
	t.Helper()
	ctx := context.Background()
	p, err := state.app.Admin.CreateProvider(ctx, admin.ProviderInput{
		Name:          providerName,
		Kind:          "openai_compatible",
		BaseURL:       "https://api.example.com",
		AdapterConfig: map[string]any{"protocol": protocolOpenAIChat},
	})
	if err != nil {
		t.Fatalf("create provider %s: %v", providerName, err)
	}
	if _, err := state.app.Admin.CreateCredential(ctx, admin.CredentialInput{
		ProviderID: p.ID,
		Name:       "default",
		Secret:     "sk-test-0123456789abcdef",
	}); err != nil {
		t.Fatalf("create credential %s: %v", providerName, err)
	}
	m, err := state.app.Admin.CreateModel(ctx, admin.ModelInput{
		ProviderID:       p.ID,
		DisplayName:      upstream,
		UpstreamName:     upstream,
		UpstreamProtocol: protocolOpenAIChat,
	})
	if err != nil {
		t.Fatalf("create model %s: %v", upstream, err)
	}
	return m
}

// TestRouteModelCreateSpansProviders walks the module's core flow: one route
// model aggregates models from two different providers, its id is what a client
// sends, and the card copies that id.
func TestRouteModelCreateSpansProviders(t *testing.T) {
	state, tt := newTester(t)
	a := createRouteTestModel(t, state, "Provider A", "gpt-a")
	b := createRouteTestModel(t, state, "Provider B", "gpt-b")

	state.page = "/route-models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if err := tt.Click("新建路由模型"); err != nil {
		t.Fatalf("open route form: %v", err)
	}
	tt.Frame()
	if !state.routeFormOpen {
		t.Fatal("the route form should be open")
	}
	if !tt.HasText("已选 0") {
		t.Fatalf("the dialog should start with no selection; texts: %v", tt.Texts())
	}

	// Both providers' models are offered, and both can be checked at once.
	for _, name := range []string{a.PublicName, b.PublicName} {
		if !tt.HasText(name) {
			t.Fatalf("the member list is missing %q; texts: %v", name, tt.Texts())
		}
	}
	if err := tt.Click("选择 " + a.PublicName); err != nil {
		t.Fatalf("check first member: %v", err)
	}
	tt.Frame()
	if err := tt.Click("选择 " + b.PublicName); err != nil {
		t.Fatalf("check second member: %v", err)
	}
	tt.Frame()
	if state.routeMemberCount() != 2 {
		t.Fatalf("selected members = %d, want 2", state.routeMemberCount())
	}

	// Priority 10 is tried before 5.
	state.routeFormMembers[a.ID].Priority = "10"
	state.routeFormMembers[b.ID].Priority = "5"
	state.routeFormName = "fast"
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.routeModels) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(state.routeModels) != 1 {
		t.Fatalf("created routes = %d, want 1", len(state.routeModels))
	}
	route := state.routeModels[0]
	if route.Name != "fast" || route.Strategy != "priority" {
		t.Fatalf("route = %+v, want name fast with priority strategy", route)
	}
	if len(route.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(route.Members))
	}
	providers := map[string]bool{}
	for _, member := range route.Members {
		providers[member.ProviderName] = true
	}
	if !providers["Provider A"] || !providers["Provider B"] {
		t.Fatalf("members should span both providers: %+v", route.Members)
	}
	if route.Members[0].ModelID != a.ID || route.Members[0].Priority != 10 {
		t.Fatalf("priority order is wrong: %+v", route.Members)
	}

	// The card shows the route id and copies it, like the model cards do.
	state.routeFormOpen = false
	tt.Frame()
	if err := tt.Click("复制"); err != nil {
		t.Fatalf("copy route id: %v", err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != "fast" {
		t.Fatalf("clipboard = %q, want the route id", got)
	}
}

// TestRouteModelEditAndDelete pins the card dropdown: editing replaces the
// member set, and deleting removes the route.
func TestRouteModelEditAndDelete(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)
	ctx := context.Background()
	models, err := state.app.Admin.ListModels(ctx, admin.ModelFilterInput{})
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) < 2 {
		t.Fatalf("seeded models = %d, want 2", len(models))
	}
	if _, err := state.app.Admin.CreateRouteModel(ctx, admin.RouteModelInput{
		Name:     "fast",
		Strategy: "priority",
		Members: []admin.RouteMemberInput{
			{ModelID: models[0].ID, Priority: 1},
		},
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	state.page = "/route-models"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if len(state.routeModels) != 1 {
		t.Fatalf("routes = %d, want 1", len(state.routeModels))
	}

	if err := tt.Click("更多"); err != nil {
		t.Fatalf("open actions: %v", err)
	}
	tt.Frame()
	if err := tt.Click("编辑"); err != nil {
		t.Fatalf("choose edit: %v", err)
	}
	tt.Frame()
	if !state.routeFormOpen || state.routeFormIsNew {
		t.Fatalf("form open = %v, isNew = %v, want edit mode", state.routeFormOpen, state.routeFormIsNew)
	}
	if state.routeFormName != "fast" || state.routeMemberCount() != 1 {
		t.Fatalf("form name = %q, selected = %d, want fast/1", state.routeFormName, state.routeMemberCount())
	}

	// Swap the member: uncheck the first, check the second.
	if err := tt.Click("选择 " + models[0].PublicName); err != nil {
		t.Fatalf("uncheck first: %v", err)
	}
	tt.Frame()
	if err := tt.Click("选择 " + models[1].PublicName); err != nil {
		t.Fatalf("check second: %v", err)
	}
	tt.Frame()
	state.routeFormMembers[models[1].ID].Priority = "3"
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.routeModels) == 1 && len(state.routeModels[0].Members) == 1 &&
			state.routeModels[0].Members[0].ModelID == models[1].ID {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(state.routeModels) != 1 {
		t.Fatalf("routes = %d, want 1", len(state.routeModels))
	}
	if len(state.routeModels[0].Members) != 1 ||
		state.routeModels[0].Members[0].ModelID != models[1].ID {
		t.Fatalf("members were not replaced: %+v", state.routeModels[0].Members)
	}

	state.routeFormOpen = false
	tt.Frame()
	if err := tt.Click("更多"); err != nil {
		t.Fatalf("open actions: %v", err)
	}
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("choose delete: %v", err)
	}
	// The destructive action asks first: the confirmation is the second click.
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("confirm delete: %v", err)
	}
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.routeModels) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("confirming 删除 should delete the route; routes = %+v", state.routeModels)
}
