package ui

import gui "github.com/egoist/mygo/ui"

// appTheme returns the console's design tokens. The palette is the "松针 / 纸"
// system: warm paper surfaces, pine-green accent, hairline borders, and a
// matching night mode for值班.
func appTheme(dark bool) *gui.Theme {
	if dark {
		return &gui.Theme{
			Dark:           true,
			Background:     gui.Hex("#0E1412"),
			Surface:        gui.Hex("#161E1B"),
			SurfaceHover:   gui.Hex("#1C2521"),
			SurfacePressed: gui.Hex("#24302A"),
			Border:         gui.Hex("#2A3530"),
			Text:           gui.Hex("#EDEFEA"),
			TextMuted:      gui.Hex("#8A958D"),
			Accent:         gui.Hex("#6ACBA8"),
			AccentHover:    gui.Hex("#8FDCC0"),
			AccentPressed:  gui.Hex("#4FB994"),
			AccentText:     gui.Hex("#08120E"),
			Danger:         gui.Hex("#E58C79"),
			Warning:        gui.Hex("#DFB36C"),
			Success:        gui.Hex("#74D3A3"),
			Selection:      gui.RGBA(106, 203, 168, 0.20),
			Focus:          gui.RGBA(106, 203, 168, 0.50),
			Inverse:        gui.Hex("#060A08"),
			InverseText:    gui.Hex("#EDEFEA"),
			Scrollbar:      gui.RGBA(237, 239, 234, 0.28),
			Radius:         10,
			Spacing:        4,
			FontSize:       13,
		}
	}
	return &gui.Theme{
		Background:     gui.Hex("#F1EFE8"),
		Surface:        gui.Hex("#FBFAF6"),
		SurfaceHover:   gui.Hex("#F4F2EA"),
		SurfacePressed: gui.Hex("#EBE8DF"),
		Border:         gui.Hex("#DEDBD1"),
		Text:           gui.Hex("#161B18"),
		TextMuted:      gui.Hex("#5E6962"),
		Accent:         gui.Hex("#146C56"),
		AccentHover:    gui.Hex("#0E5442"),
		AccentPressed:  gui.Hex("#0A4335"),
		AccentText:     gui.Hex("#FFFFFF"),
		Danger:         gui.Hex("#AC4331"),
		Warning:        gui.Hex("#9A671F"),
		Success:        gui.Hex("#2C7A51"),
		Selection:      gui.RGBA(20, 108, 86, 0.18),
		Focus:          gui.RGBA(20, 108, 86, 0.50),
		Inverse:        gui.Hex("#161B18"),
		InverseText:    gui.Hex("#FBFAF6"),
		Scrollbar:      gui.RGBA(22, 27, 24, 0.28),
		Radius:         10,
		Spacing:        4,
		FontSize:       13,
	}
}

// railColor is the paper strip behind the navigation, a shade deeper than the
// page so the rail reads without a shadow.
func railColor(t *gui.Theme) gui.Color {
	if t.Dark {
		return gui.Hex("#111816")
	}
	return gui.Hex("#ECE9E0")
}
