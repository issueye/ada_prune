package desktop

import (
	"fmt"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_proxy/internal/gateway"
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
func card(c *ui.Context, fn func()) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(spL).Gap(spM).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(fn)
}

// panel is a card with a title and a divider.
func panel(c *ui.Context, title string, fn func()) {
	card(c, func() {
		ui.Text(c, title).FontSize(13).Bold()
		ui.Divider(c)
		fn()
	})
}

// pill is a small status chip.
func pill(c *ui.Context, text string, fg, bg ui.Color) {
	ui.Row(c).Padding(2, 8).Radius(999).Background(bg).Children(func() {
		ui.Text(c, text).FontSize(11).TextColor(fg).SingleLine()
	})
}

// infoIcon is the circled "i" that carries a field's description as a tooltip.
// It is parsed once: the SVG is part of the program, not input.
var infoIcon = sync.OnceValue(func() *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M8 7.2v3.8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="4.7" r="1" fill="currentColor"/></svg>`))
})

// hintIcon marks a description: the icon stays inline with the label, the text
// shows as a tooltip on hover or focus. The label names it for readers and tests.
func hintIcon(c *ui.Context, name, hint string) {
	ui.Icon(c, infoIcon()).FontSize(11).TextColor(c.Theme().TextMuted).
		Label(name + "说明").Tooltip(hint)
}

// statusPill colors a boolean state. On a selected table row the chip turns
// white so it stays readable on the accent fill.
func statusPill(c *ui.Context, enabled bool, onText, offText string, selected bool) {
	t := c.Theme()
	tone, text := t.TextMuted, offText
	if enabled {
		tone, text = t.Success, onText
	}
	fg, bg := pillTone(c, selected, tone)
	pill(c, text, fg, bg)
}

// routablePill shows whether a model or provider can serve right now.
func routablePill(c *ui.Context, routable bool, reason string, selected bool) {
	t := c.Theme()
	tone, text := t.Warning, "不可路由"
	if routable {
		tone, text = t.Success, "可路由"
	} else if reason != "" {
		text += " · " + reason
	}
	fg, bg := pillTone(c, selected, tone)
	pill(c, text, fg, bg)
}

// accessModePill shows the provider's access classification: reverse proxies
// get the accent tone, plain APIs a neutral one.
func accessModePill(c *ui.Context, mode gateway.AccessMode, selected bool) {
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
func pillTone(c *ui.Context, selected bool, tone ui.Color) (fg, bg ui.Color) {
	t := c.Theme()
	if selected {
		return t.AccentText, t.AccentText.Alpha(0.18)
	}
	return tone, tone.Alpha(0.14)
}

// rowColors returns the primary and secondary text colors of a table row. The
// selected row is filled with the accent color, so both turn to AccentText to
// stay readable; explicit Text/TextMuted overrides would not.
func rowColors(c *ui.Context, selected bool) (primary, secondary ui.Color) {
	t := c.Theme()
	if selected {
		return t.AccentText, t.AccentText
	}
	return t.Text, t.TextMuted
}

// rowActions is the compact dropdown trigger of a table's action column. The
// menu is drawn as a window overlay, so it never changes the row's height.
func rowActions(c *ui.Context, build func(m *ui.Menu)) {
	ui.PopoverMenuButton(c, "更多", build).FontSize(11).Padding(2, 8)
}

// legendDot labels one series of a chart: a tone swatch plus its name.
func legendDot(c *ui.Context, tone ui.Color, label string) {
	t := c.Theme()
	ui.Row(c).Gap(spXS).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(2).Background(tone.Alpha(0.7))
		ui.Text(c, label).FontSize(10).TextColor(t.TextMuted)
	})
}

// statTile is one headline number.
func statTile(c *ui.Context, label, value, hint string, tone ui.Color) {
	t := c.Theme()
	card(c, func() {
		ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(tone)
			ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		ui.Text(c, value).FontSize(22).Bold().SingleLine()
		if hint != "" {
			ui.Text(c, hint).FontSize(10).TextColor(t.TextMuted).SingleLine()
		}
	})
}

// errorLine renders the page's load error, if any.
func (s *uiState) errorLine(c *ui.Context) {
	if s.pageErr == "" {
		return
	}
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(8, 12).Radius(8).Gap(spS).AlignItems(ui.Center).
		Background(t.Danger.Alpha(0.1)).Children(func() {
		ui.Text(c, "加载失败: "+s.pageErr).FontSize(11).TextColor(t.Danger).SingleLine()
	})
}

// emptyState tells the user what to do next, not just that there is nothing.
func emptyState(c *ui.Context, message, hint string) {
	t := c.Theme()
	ui.Column(c).Padding(spL, 0).Gap(spXS).Children(func() {
		ui.Text(c, message).FontSize(12).TextColor(t.TextMuted)
		if hint != "" {
			ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// sectionTitle labels a group inside a page.
func sectionTitle(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(13).Bold()
}

// fmtInt renders an integer with thousands separators.
func fmtInt(v int64) string {
	negative := v < 0
	if negative {
		v = -v
	}
	raw := fmt.Sprintf("%d", v)
	out := make([]byte, 0, len(raw)+len(raw)/3)
	for i, digit := range []byte(raw) {
		if i > 0 && (len(raw)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digit)
	}
	if negative {
		return "-" + string(out)
	}
	return string(out)
}

// fmtMoney renders a cost with enough precision for small amounts.
func fmtMoney(v float64) string {
	if v == 0 {
		return "0"
	}
	if v < 0.01 {
		return fmt.Sprintf("%.4f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// fmtDuration renders a millisecond duration compactly.
func fmtDuration(ms int64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%dm%02ds", ms/60000, (ms%60000)/1000)
	}
}

// fmtTokens renders a token count compactly.
func fmtTokens(v int64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fK", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// statusColor maps an HTTP status onto a theme color.
func statusColor(c *ui.Context, status int64) ui.Color {
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
func mask(secret string) string {
	runes := []rune(secret)
	switch {
	case len(runes) == 0:
		return ""
	case len(runes) <= 4:
		return "…"
	case len(runes) <= 14:
		return string(runes[:4]) + "…"
	default:
		return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
	}
}
