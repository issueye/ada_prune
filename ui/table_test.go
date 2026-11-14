package ui

import (
	"testing"
	"time"

	gui "github.com/egoist/mygo/ui"
)

// TestTableRowActionsDropdown pins the interaction model of the gateway pages:
// each row carries a compact dropdown of actions, and the detail panel under the
// table is read-only.
//
// The trigger fits a text row and its menu is a window overlay, so the table
// still lays out every row at a uniform height.
func TestTableRowActionsDropdown(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/providers"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if !tt.HasText("OpenAI 主号") {
		t.Fatalf("the provider row is missing; texts: %v", tt.Texts())
	}

	// Choosing the row reveals its read-only detail panel.
	if err := tt.Click("OpenAI 主号"); err != nil {
		t.Fatalf("click row: %v", err)
	}
	tt.Frame()
	if state.providerSel != 0 {
		t.Fatalf("selected row = %d, want 0", state.providerSel)
	}
	if !tt.HasText("供应商 · OpenAI 主号") {
		t.Fatalf("detail panel is missing; texts: %v", tt.Texts())
	}

	// The row's dropdown carries the actions.
	if err := tt.Click("更多"); err != nil {
		t.Fatalf("click row actions: %v", err)
	}
	tt.Frame()
	for _, item := range []string{"编辑", "探活", "停用", "删除"} {
		if !tt.HasText(item) {
			t.Fatalf("menu item %q is missing; texts: %v", item, tt.Texts())
		}
	}

	// Choosing an item runs the action and closes the menu. The action writes
	// off the main thread; without a window the result lands only in the store,
	// so poll a refresh for it.
	if err := tt.Click("停用"); err != nil {
		t.Fatalf("choose menu item: %v", err)
	}
	tt.Frame()
	// 编辑 only exists in the menu; the page subtitle already contains 探活.
	if tt.HasText("编辑") {
		t.Fatalf("the menu should close after a choice; texts: %v", tt.Texts())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.refresh(); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if len(state.providers) == 1 && !state.providers[0].Enabled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(state.providers) != 1 || state.providers[0].Enabled {
		t.Fatal("choosing 停用 should disable the provider")
	}

	// The empty table (no rows) still renders its guidance. With a query set,
	// the page says nothing matched rather than that the catalogue is empty.
	state.page = "/models"
	state.modelQuery = "no-such-model"
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()
	if !tt.HasText("没有匹配的模型") {
		t.Fatalf("empty state is missing; texts: %v", tt.Texts())
	}
}

// TestSelectedRowPalette locks the contrast fix: a selected row is filled with
// the accent color, so its text and chips must turn to AccentText rather than
// stay Text/TextMuted (which is unreadable on the saturated fill).
func TestSelectedRowPalette(t *testing.T) {
	var onPrimary, onSecondary, offPrimary, offSecondary, fg, bg gui.Color
	tt := gui.NewTester(func(c *gui.Context) {
		onPrimary, onSecondary = rowColors(c, true)
		offPrimary, offSecondary = rowColors(c, false)
		fg, bg = pillTone(c, true, c.Theme().Success)
		gui.Text(c, "palette")
	}, 100, 100)
	tt.Frame()

	want := gui.LightTheme()
	if onPrimary != want.AccentText || onSecondary != want.AccentText {
		t.Fatalf("selected row colors = %v/%v, want AccentText %v", onPrimary, onSecondary, want.AccentText)
	}
	if offPrimary != want.Text || offSecondary != want.TextMuted {
		t.Fatalf("normal row colors = %v/%v, want Text/TextMuted", offPrimary, offSecondary)
	}
	if fg != want.AccentText {
		t.Fatalf("selected chip fg = %v, want %v", fg, want.AccentText)
	}
	if bg.A == 0 || bg.A == 255 {
		t.Fatalf("selected chip bg should be translucent, got %v", bg)
	}
}

// TestModelGridRendersEveryCard is a layout regression guard: the card grid
// must lay out every model, not just the first row of cards. It renders the
// page and checks the second card's text is present.
func TestModelGridRendersEveryCard(t *testing.T) {
	state, tt := newTester(t)
	seedDemoData(t, state)

	state.page = "/models"
	state.modelQuery = ""
	if err := state.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tt.Frame()

	if len(state.models) < 2 {
		t.Fatalf("expected at least two seeded models, got %d", len(state.models))
	}
	if !tt.HasText("gpt-5.6-luna") {
		t.Fatalf("the second row was not rendered; texts: %v", tt.Texts())
	}
}
