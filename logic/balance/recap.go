package balance

import (
	"fmt"
	"time"
)

// RecapBucket is the time grain for GET /api/v1/recap. The handler
// defaults an empty query param to RecapMonth before parsing.
type RecapBucket string

const (
	RecapWeek  RecapBucket = "week"
	RecapMonth RecapBucket = "month"
	RecapYear  RecapBucket = "year"
)

// ParseRecapBucket accepts only week|month|year. Pure — the handler
// maps the error to 400 validation_failed.
func ParseRecapBucket(s string) (RecapBucket, error) {
	switch RecapBucket(s) {
	case RecapWeek, RecapMonth, RecapYear:
		return RecapBucket(s), nil
	default:
		return "", fmt.Errorf("balance: bucket must be week, month, or year, got %q", s)
	}
}

// TruncateToBucket floors t to its bucket start in t's own location:
// Monday 00:00 for weeks, the 1st for months, Jan 1 for years. Pure.
func TruncateToBucket(t time.Time, b RecapBucket) time.Time {
	y, m, d := t.Date()
	loc := t.Location()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, loc)
	switch b {
	case RecapWeek:
		// time.Weekday: Sunday=0..Saturday=6; Monday-start offset.
		offset := (int(midnight.Weekday()) + 6) % 7
		return midnight.AddDate(0, 0, -offset)
	case RecapYear:
		return time.Date(y, time.January, 1, 0, 0, 0, 0, loc)
	default: // RecapMonth
		return time.Date(y, m, 1, 0, 0, 0, 0, loc)
	}
}

// RecapNet is the bucket net in cents: inflow - outflow. Pure —
// int64 cents per CLAUDE.md, never float.
func RecapNet(inflow, outflow int64) int64 { return inflow - outflow }
