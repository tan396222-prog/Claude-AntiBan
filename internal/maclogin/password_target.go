package maclogin

import "time"

type passwordTarget struct {
	maintenanceWindow
	Accessibility     bool `json:"accessibility"`
	SelectionRequired bool `json:"selection_required"`
}

// Before Accessibility is granted, the user selects the input position in
// Apple's system panel. The host binds that click to the current GUI session
// and panel, and consumes it when the user requests password entry.
type passwordSelection struct {
	Geometry string
	X, Y     int
	Clicked  time.Time
}

func (selection passwordSelection) permits(target passwordTarget, user string, now time.Time) bool {
	if !target.valid(user) {
		return false
	}
	if target.Accessibility {
		return target.PasswordFocused
	}
	return target.SelectionRequired && selection.Geometry == target.geometry() &&
		now.Sub(selection.Clicked) >= 0 && now.Sub(selection.Clicked) <= 30*time.Second &&
		target.contains(selection.X, selection.Y)
}
