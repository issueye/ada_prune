package ui

import (
	"context"
	gui "github.com/egoist/mygo/ui"
)

// fieldWith renders a label, its hint and the control they describe as one
// group, so a control is never mistaken for the next field's. The hint stays
// out of the layout: an info icon by the label shows it as a tooltip.
func fieldWith(c *gui.Context, label, hint string, build func()) {
	gui.Column(c).Gap(spXS).Children(func() {
		gui.Row(c).Gap(spXS).AlignItems(gui.Center).Children(func() {
			gui.Text(c, label).FontSize(11).Bold()
			if hint != "" {
				hintIcon(c, label, hint)
			}
		})
		build()
	})
}

// ctx returns a request context for UI-triggered actions.
func (s *uiState) ctx() context.Context { return context.Background() }
