package ui

import (
	"time"
)

// usageFrom is the inclusive start day of the selected window; empty means all
// time. The store buckets by UTC day, so the window is computed in UTC too.
func (s *uiState) usageFrom() string {
	days := s.usageRangeDays()
	if days == 0 {
		return ""
	}
	return time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
}

// usageRangeDays is the window length in days, zero for all time.
func (s *uiState) usageRangeDays() int {
	switch s.usageRange {
	case "7d":
		return 7
	case "30d":
		return 30
	default:
		return 0
	}
}
