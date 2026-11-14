package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"sync"
)

// Spacing scale, in DIPs.
const (
	spXS float32 = 4
	spS  float32 = 8
	spM  float32 = 12
	spL  float32 = 16
	spXL float32 = 24
)

// card is the base surface: a rounded panel with a hairline border.
func card(c *gui.Context, fn func()) {
	t := c.Theme()
	gui.Column(c).FillWidth().Padding(spL).Gap(spM).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(fn)
}

// panel is a card with a title and a divider.
func panel(c *gui.Context, title string, fn func()) {
	card(c, func() {
		gui.Text(c, title).FontSize(13).Bold()
		gui.Divider(c)
		fn()
	})
}

// pill is a small status chip. It returns the chip so a caller can attach a
// tooltip or an accessible label to it.
// pill is a small status chip. It returns the chip so a caller can attach a
// tooltip or an accessible label to it.
func pill(c *gui.Context, text string, fg, bg gui.Color) gui.Element {
	return gui.Row(c).Padding(3, 7).Radius(5).Background(bg).AlignItems(gui.Center).Children(func() {
		gui.Text(c, text).FontSize(10).TextColor(fg).SingleLine()
	})
}

// infoIcon is the circled "i" that carries a field's description as a tooltip.
// It is parsed once: the SVG is part of the program, not input.
// infoIcon is the circled "i" that carries a field's description as a tooltip.
// It is parsed once: the SVG is part of the program, not input.
var infoIcon = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M8 7.2v3.8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="4.7" r="1" fill="currentColor"/></svg>`))
})

// hintIcon marks a description: the icon stays inline with the label, the text
// shows as a tooltip on hover or focus. The label names it for readers and tests.
// hintIcon marks a description: the icon stays inline with the label, the text
// shows as a tooltip on hover or focus. The label names it for readers and tests.
func hintIcon(c *gui.Context, name, hint string) {
	gui.Icon(c, infoIcon()).FontSize(11).TextColor(c.Theme().TextMuted).
		Label(name + "说明").Tooltip(hint)
}

// statusPill colors a boolean state. On a selected table row the chip turns
// white so it stays readable on the accent fill.
// statusPill colors a boolean state. On a selected table row the chip turns
// white so it stays readable on the accent fill.
func statusPill(c *gui.Context, enabled bool, onText, offText string, selected bool) {
	t := c.Theme()
	tone, text := t.TextMuted, offText
	if enabled {
		tone, text = t.Success, onText
	}
	fg, bg := pillTone(c, selected, tone)
	pill(c, text, fg, bg)
}

// routableReasonLabel translates the service's English routability reasons into
// the interface language. The mapping stays in the view layer: the service and
// the API contract keep the stable English strings clients match on.
// routableReasonLabel translates the service's English routability reasons into
// the interface language. The mapping stays in the view layer: the service and
// the API contract keep the stable English strings clients match on.
func routableReasonLabel(reason string) string {
	switch reason {
	case "provider is disabled":
		return "供应商已停用"
	case "provider has no active credential":
		return "供应商无可用凭据"
	case "model is disabled":
		return "模型已停用"
	case "provider is missing":
		return "供应商已被删除"
	}
	return reason
}

// routablePill shows whether a model or provider can serve right now. A reason
// travels with the pill as a tooltip, because the pill truncates long text.
// routablePill shows whether a model or provider can serve right now. A reason
// travels with the pill as a tooltip, because the pill truncates long text.
func routablePill(c *gui.Context, routable bool, reason string, selected bool) {
	t := c.Theme()
	tone, text := t.Warning, "不可路由"
	if routable {
		tone, text = t.Success, "可路由"
	} else if reason != "" {
		reason = routableReasonLabel(reason)
		text += " · " + reason
	}
	fg, bg := pillTone(c, selected, tone)
	chip := pill(c, text, fg, bg)
	if !routable && reason != "" {
		chip.Tooltip(text)
	}
}

// accessModePill shows the provider's access classification: reverse proxies
// get the accent tone, plain APIs a neutral one.
// accessModePill shows the provider's access classification: reverse proxies
// get the accent tone, plain APIs a neutral one.
func accessModePill(c *gui.Context, mode gateway.AccessMode, selected bool) {
	t := c.Theme()
	tone := t.TextMuted
	if mode == gateway.AccessReverseProxy {
		tone = t.Accent
	}
	fg, bg := pillTone(c, selected, tone)
	pill(c, accessModeLabel(mode), fg, bg)
}

// pillTone colors a status chip: its semantic tone on normal surfaces, white
// on the accent-filled selected row.
// pillTone colors a status chip: its semantic tone on normal surfaces, white
// on the accent-filled selected row.
func pillTone(c *gui.Context, selected bool, tone gui.Color) (fg, bg gui.Color) {
	t := c.Theme()
	if selected {
		return t.AccentText, t.AccentText.Alpha(0.18)
	}
	return tone, tone.Alpha(0.14)
}

// rowColors returns the primary and secondary text colors of a table row. The
// selected row is filled with the accent color, so both turn to AccentText to
// stay readable; explicit Text/TextMuted overrides would not.
// rowColors returns the primary and secondary text colors of a table row. The
// selected row is filled with the accent color, so both turn to AccentText to
// stay readable; explicit Text/TextMuted overrides would not.
func rowColors(c *gui.Context, selected bool) (primary, secondary gui.Color) {
	t := c.Theme()
	if selected {
		return t.AccentText, t.AccentText
	}
	return t.Text, t.TextMuted
}

// rowActions is the compact dropdown trigger of a table's action column. The
// menu is drawn as a window overlay, so it never changes the row's height.
// rowActions is the compact dropdown trigger of a table's action column. The
// menu is drawn as a window overlay, so it never changes the row's height.
func rowActions(c *gui.Context, build func(m *gui.Menu)) {
	gui.PopoverMenuButton(c, "更多", build).FontSize(11).Padding(2, 8)
}

// buttonIcon sizes the leading glyph of a button: one step above its label.
// The icon inherits the button's text color, so it follows hover and primary.
// buttonIcon sizes the leading glyph of a button: one step above its label.
// The icon inherits the button's text color, so it follows hover and primary.
func buttonIcon(c *gui.Context, svg *gui.SVG) {
	gui.Icon(c, svg).FontSize(13)
}

// iconButton is a button whose label leads with an icon. The empty label plus
// children keeps the theme's button look and adds the glyph; Clicked works as
// on any button.
// iconButton is a button whose label leads with an icon. The empty label plus
// children keeps the theme's button look and adds the glyph; Clicked works as
// on any button.
func iconButton(c *gui.Context, svg *gui.SVG, label string) gui.Element {
	return gui.Button(c, "").Children(func() {
		buttonIcon(c, svg)
		gui.Text(c, label).SingleLine()
	})
}

// primaryIconButton is the accent-colored counterpart of iconButton.
func primaryIconButton(c *gui.Context, svg *gui.SVG, label string) gui.Element {
	return gui.PrimaryButton(c, "").Children(func() {
		buttonIcon(c, svg)
		gui.Text(c, label).SingleLine()
	})
}

// legendDot labels one series of a chart: a tone swatch plus its name.
func legendDot(c *gui.Context, tone gui.Color, label string) {
	t := c.Theme()
	gui.Row(c).Gap(spXS).AlignItems(gui.Center).Children(func() {
		gui.Box(c).Size(8, 8).Radius(2).Background(tone.Alpha(0.7))
		gui.Text(c, label).FontSize(10).TextColor(t.TextMuted)
	})
}

// kpiCell is one reading of the dashboard's instrument strip.
type kpiCell struct {
	Label string
	Value string
	Hint  string
}

// kpiStrip is the single-band replacement for a row of equal stat cards: one
// surface, five readings, hairline separators. The numbers carry the weight;
// the labels and units stay small and quiet.
func kpiStrip(c *gui.Context, cells []kpiCell) {
	t := c.Theme()
	row := gui.Row(c).FillWidth().Radius(12).Border(1, t.Border).
		Background(t.Surface).AlignItems(gui.Stretch)
	row.Children(func() {
		for i, cell := range cells {
			if i > 0 {
				gui.Box(c).Width(1).Height(72).Background(t.Border)
			}
			gui.Column(c).Grow(1).Padding(spL, spL, spL, spL).Gap(2).Children(func() {
				gui.Text(c, cell.Label).FontSize(10).TextColor(t.TextMuted).SingleLine()
				gui.Text(c, cell.Value).FontSize(26).Bold().SingleLine()
				if cell.Hint != "" {
					gui.Text(c, cell.Hint).FontSize(10).TextColor(t.TextMuted).SingleLine()
				}
			})
		}
	})
}

// statTile is one headline number.
func statTile(c *gui.Context, label, value, hint string, tone gui.Color) {
	t := c.Theme()
	card(c, func() {
		gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
			gui.Box(c).Size(8, 8).Radius(4).Background(tone)
			gui.Text(c, label).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		gui.Text(c, value).FontSize(22).Bold().SingleLine()
		if hint != "" {
			gui.Text(c, hint).FontSize(10).TextColor(t.TextMuted).SingleLine()
		}
	})
}

// errorLine renders the page's load error, if any.
func (s *uiState) errorLine(c *gui.Context) {
	if s.pageErr == "" {
		return
	}
	t := c.Theme()
	gui.Row(c).FillWidth().Padding(8, 12).Radius(8).Gap(spS).AlignItems(gui.Center).
		Background(t.Danger.Alpha(0.1)).Children(func() {
		gui.Text(c, "加载失败: "+s.pageErr).FontSize(11).TextColor(t.Danger).SingleLine()
	})
}

// emptyState tells the user what to do next, not just that there is nothing.
func emptyState(c *gui.Context, message, hint string) {
	t := c.Theme()
	gui.Column(c).Padding(spL, 0).Gap(spXS).Children(func() {
		gui.Text(c, message).FontSize(12).TextColor(t.TextMuted)
		if hint != "" {
			gui.Text(c, hint).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// emptyStateAction is an empty state whose next step is a real button, so the
// direction the text gives is one click away instead of across the window.
// emptyStateAction is an empty state whose next step is a real button, so the
// direction the text gives is one click away instead of across the window.
func emptyStateAction(c *gui.Context, message, hint string, actions func()) {
	emptyState(c, message, hint)
	if actions != nil {
		gui.Row(c).Gap(spS).Padding(spXS, 0).Children(actions)
	}
}

// pager is a pagination strip: first and prev, the page indicator, next and
// last. The buttons disable at the bounds; onMove receives the 1-based target
// page. Trailing (optional) builds what sits on the right — a per-page chooser,
// a total — after the spacer.
// pager is a pagination strip: first and prev, the page indicator, next and
// last. The buttons disable at the bounds; onMove receives the 1-based target
// page. Trailing (optional) builds what sits on the right — a per-page chooser,
// a total — after the spacer.
func pager(c *gui.Context, page, pages int, hint string, trailing func(), onMove func(int)) {
	t := c.Theme()
	move := func(label string, target int, disabled bool) {
		b := gui.Button(c, label).FontSize(11).Padding(2, 10)
		if disabled {
			b.Disabled(true)
		}
		if b.Clicked() {
			onMove(target)
		}
	}
	gui.Row(c).FillWidth().Gap(spS).AlignItems(gui.Center).Children(func() {
		move("首页", 1, page <= 1)
		move("上一页", page-1, page <= 1)
		gui.Text(c, fmt.Sprintf("第 %d / %d 页", page, pages)).
			FontSize(11).TextColor(t.TextMuted).SingleLine()
		move("下一页", page+1, page >= pages)
		move("末页", pages, page >= pages)
		gui.Spacer(c)
		if trailing != nil {
			trailing()
		}
		if hint != "" {
			gui.Text(c, hint).FontSize(10).TextColor(t.TextMuted).SingleLine()
		}
	})
}

// sectionTitle labels a group inside a page.
func sectionTitle(c *gui.Context, title string) {
	gui.Text(c, title).FontSize(13).Bold()
}

// fmtInt renders an integer with thousands separators.

// fmtMoney renders a cost with enough precision for small amounts.

// fmtDuration renders a millisecond duration compactly.

// fmtTokens renders a token count compactly.

// statusColor maps an HTTP status onto a theme color.
func statusColor(c *gui.Context, status int64) gui.Color {
	t := c.Theme()
	switch {
	case status >= 500:
		return t.Danger
	case status >= 400:
		return t.Warning
	case status >= 200 && status < 300:
		return t.Success
	default:
		return t.TextMuted
	}
}

// mask renders a secret the way the store does, for display only.
