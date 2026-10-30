package handler

import "time"

// reversedRangeError validates the human-page date order for the
// Transactions / Spending / Rekap filters. Both endpoints parse first
// (blank or malformed input falls back to defaults, echoing the
// default); this only fires when both dates parsed and From is after
// To, so the page can show an inline error and keep the user's input
// instead of a misleading "no data" empty state.
func reversedRangeError(from, to time.Time) string {
	if from.After(to) {
		return "From date must be on or before To date."
	}
	return ""
}
