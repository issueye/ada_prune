package ui

import (
	gui "github.com/egoist/mygo/ui"
)

func (s *uiState) sidebar(c *gui.Context) {
	t := c.Theme()
	previous := s.page

	// Two shapes, one tree: the rail keeps every page reachable by its icon,
	// the full bar adds the group titles and labels. Width is the only thing
	// the rest of the window sees.
	width := float32(216)
	if s.sidebarCollapsed {
		width = 56
	}
	gui.Column(c).Width(width).FillHeight().Background(railColor(t)).Children(func() {
		gui.Column(c).Padding(spS, 10, spS, 10).Gap(1).Grow(1).Children(func() {
			for _, group := range navGroups {
				if s.sidebarCollapsed {
					// The rail drops the titles but keeps the group rhythm.
					gui.Box(c).Height(spS)
				} else {
					gui.Text(c, group.Title).FontSize(10).TextColor(t.TextMuted).Padding(10, 8, spXS, 10)
				}
				for _, item := range group.Items {
					s.navRow(c, item)
				}
			}
		}).Grow(1)

		gui.Divider(c)
		s.sidebarFooter(c)
		s.sidebarToggle(c)
	})

	if s.page != previous {
		s.onPageChange()
	}
}

// navRow is one sidebar item: a surface pill with an accent bar when chosen.
// Enter or Space activates the focused row, so the sidebar works without a
// pointer. Folded, the icon alone carries the page — named for the reader and
// screen readers, with the label as a tooltip.
func (s *uiState) navRow(c *gui.Context, item navItem) {
	t := c.Theme()
	selected := s.page == item.Path
	folded := s.sidebarCollapsed

	row := gui.Row(c.Key(item.Path)).Padding(0, 10).Height(30).Radius(8).
		Gap(spS).AlignItems(gui.Center).Focusable().Cursor(gui.CursorPointer)
	if folded {
		row = row.Padding(0, 0).Justify(gui.Center)
	}
	if selected {
		row.Background(t.Surface).Border(1, t.Border)
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}

	labelColor := t.Text
	if selected {
		labelColor = t.Accent
	}

	row.Children(func() {
		bar := gui.Box(c).Size(3, 14).Radius(2)
		if selected {
			bar.Background(t.Accent)
		}
		if item.Icon != nil {
			icon := gui.Icon(c, item.Icon()).FontSize(14).TextColor(labelColor)
			if folded {
				icon.Label(item.Label).Tooltip(item.Label)
			}
		}
		if !folded {
			gui.Text(c, item.Label).FontSize(12).TextColor(labelColor).SingleLine()
		}
	})

	if row.Clicked() || row.Shortcut(0, gui.KeyEnter) || row.Shortcut(0, gui.KeySpace) {
		s.page = item.Path
	}
}

// sidebarToggle folds or unfolds the rail; the choice persists (see
// toggleSidebar). Folded, the chevron alone fills the row.
func (s *uiState) sidebarToggle(c *gui.Context) {
	t := c.Theme()
	folded := s.sidebarCollapsed

	row := gui.Row(c.Key("sidebar-toggle")).Height(30).Radius(7).Gap(spS).
		AlignItems(gui.Center).Focusable().Cursor(gui.CursorPointer)
	if folded {
		row = row.Padding(0, 0).Justify(gui.Center)
	}
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		if folded {
			gui.Icon(c, iconChevronRight()).FontSize(14).TextColor(t.TextMuted).
				Label("展开侧栏").Tooltip("展开侧栏")
			return
		}
		gui.Icon(c, iconChevronLeft()).FontSize(14).TextColor(t.TextMuted)
		gui.Text(c, "收起侧栏").FontSize(11).TextColor(t.TextMuted).SingleLine()
	})
	if row.Clicked() || row.Shortcut(0, gui.KeyEnter) || row.Shortcut(0, gui.KeySpace) {
		s.toggleSidebar()
	}
}

func (s *uiState) sidebarFooter(c *gui.Context) {
	t := c.Theme()
	if s.sidebarCollapsed {
		// The rail keeps the one glanceable bit: the storage dot.
		dot := t.Success
		if s.overview.Degraded {
			dot = t.Warning
		}
		gui.Row(c).Justify(gui.Center).Padding(spS, 0).Children(func() {
			gui.Box(c).Size(6, 6).Radius(3).Background(dot).Tooltip(storageLabel(s.overview.Degraded))
		})
		return
	}
	gui.Column(c).Padding(spL, spM, spL, spM).Gap(spXS).Children(func() {
		gui.Row(c).Gap(6).AlignItems(gui.Center).Children(func() {
			dot := t.Success
			if s.overview.Degraded {
				dot = t.Warning
			}
			gui.Box(c).Size(6, 6).Radius(3).Background(dot)
			label := "存储正常"
			if s.overview.Degraded {
				label = "存储降级"
			}
			gui.Text(c, label).FontSize(10).TextColor(t.TextMuted)
		})
		gui.Text(c, s.endpoint()).FontSize(10).TextColor(t.TextMuted).SingleLine()
	})
}
