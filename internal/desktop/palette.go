package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// paletteItem is one entry of the command palette: what it says, where it
// comes from (shown as a muted hint), its glyph, and what it does.
type paletteItem struct {
	Label  string
	Hint   string
	Icon   func() *ui.SVG
	Action func()
}

// paletteDialog is the command palette (Cmd+K): one search box over every page
// and the few actions that make sense from anywhere. It is the keyboard answer
// to the sidebar, not a second navigation: entries always lead somewhere the
// sidebar could, so it can never open a page the user cannot reach otherwise.
func (s *uiState) paletteDialog(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(520).Gap(spM).Children(func() {
		// The field takes the focus when the palette opens, so typing filters
		// immediately; Escape closes the palette (the modal clears the flag).
		ui.TextInput(c, &s.paletteQuery).FillWidth().AutoFocus().
			Placeholder("搜索页面或动作…")

		items := s.paletteItems()
		if len(items) == 0 {
			ui.Text(c, "没有匹配的页面或动作").FontSize(11).TextColor(t.TextMuted)
			return
		}

		// Keep the list compact for a short match set and scroll only when the
		// matches would make the palette taller than the window allows.
		listHeight := float32(len(items))*34 + 4
		if listHeight > 320 {
			listHeight = 320
		}
		ui.Scroll(c).Height(listHeight).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				for _, item := range items {
					s.paletteRow(c, item)
				}
			})
		})
		ui.Text(c, "Enter 选择 · Esc 关闭 · Cmd+1..9 直达页面").FontSize(10).
			TextColor(t.TextMuted)
	})
}

// paletteRow is one command: a full-width hoverable row. The glyph matches the
// sidebar's for pages and the action buttons' for actions, so an entry reads
// the same wherever it appears. The label carries the verb, the hint where it
// lives, so two similar entries stay distinguishable.
func (s *uiState) paletteRow(c *ui.Context, item paletteItem) {
	t := c.Theme()
	row := ui.Row(c).FillWidth().Padding(6, 10).Radius(7).Gap(spS).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		if item.Icon != nil {
			ui.Icon(c, item.Icon()).FontSize(14).TextColor(t.TextMuted)
		}
		ui.Text(c, item.Label).FontSize(12).SingleLine()
		ui.Spacer(c)
		if item.Hint != "" {
			ui.Text(c, item.Hint).FontSize(10).TextColor(t.TextMuted).SingleLine()
		}
	})
	if row.Clicked() {
		s.paletteOpen = false
		s.paletteQuery = ""
		if item.Action != nil {
			item.Action()
		}
	}
}

// paletteItems lists the palette's entries filtered by the query: pages first,
// then the global actions. Filtering is local and case-insensitive; the hint
// matches too, so "供应商" finds both the page and the create action.
func (s *uiState) paletteItems() []paletteItem {
	query := strings.ToLower(strings.TrimSpace(s.paletteQuery))
	out := make([]paletteItem, 0, len(navGroups)+4)
	add := func(label, hint string, icon func() *ui.SVG, action func()) {
		if query != "" &&
			!strings.Contains(strings.ToLower(label), query) &&
			!strings.Contains(strings.ToLower(hint), query) {
			return
		}
		out = append(out, paletteItem{Label: label, Hint: hint, Icon: icon, Action: action})
	}

	for _, item := range navItems() {
		meta := pageTitles[item.Path]
		target := item.Path
		add("跳转 · "+meta.Title, meta.Subtitle, item.Icon, func() { s.navigate(target) })
	}
	add("新建供应商", "网关", iconPlus, func() { s.openNewProvider() })
	add("添加模型", "网关", iconPlus, func() {
		if len(s.providers) == 0 {
			s.navigate("/models")
			return
		}
		s.openModelForm()
	})
	add("新建客户端密钥", "网关", iconKeys, func() { s.navigate("/keys") })
	add("收起/展开侧栏", "界面", iconChevronLeft, func() { s.toggleSidebar() })
	add("刷新当前页", "重新加载数据", iconRefresh, func() {
		s.pageErr = ""
		s.reload()
	})
	return out
}
