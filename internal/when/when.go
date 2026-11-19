// Package when parses the fixed date vocabulary thingsctl accepts as input.
// There is deliberately no natural-language parsing: models convert phrases
// like "next tuesday" to ISO dates reliably, and a closed vocabulary gives
// one-line errors instead of silent misreads.
package when

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// DayForms lists the accepted day forms, for error messages and help.
const DayForms = "YYYY-MM-DD, today, tomorrow, yesterday, +Nd, -Nd"

// Day parses a calendar day relative to today.
func Day(s string, today thingsdb.Date) (thingsdb.Date, error) {
	switch s = strings.TrimSpace(strings.ToLower(s)); s {
	case "today":
		return today, nil
	case "tomorrow":
		return today.AddDays(1), nil
	case "yesterday":
		return today.AddDays(-1), nil
	}
	if len(s) > 2 && (s[0] == '+' || s[0] == '-') && s[len(s)-1] == 'd' {
		if n, err := strconv.Atoi(s[1 : len(s)-1]); err == nil {
			if s[0] == '-' {
				n = -n
			}
			return today.AddDays(n), nil
		}
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return thingsdb.DateOf(t), nil
	}
	return 0, fmt.Errorf("invalid date %q; valid: %s", s, DayForms)
}

// Kind is where a "when" value puts an item.
type Kind int

const (
	KindDate    Kind = iota // a specific day (today included)
	KindEvening             // this evening
	KindAnytime             // Anytime list, no date
	KindSomeday             // Someday list, no date
)

// When is a parsed --when value.
type When struct {
	Kind        Kind
	Date        thingsdb.Date // KindDate and KindEvening
	HasReminder bool
	Reminder    thingsdb.Clock
}

// WhenForms lists the accepted --when forms.
const WhenForms = "today, tomorrow, evening, anytime, someday, YYYY-MM-DD, +Nd; append @HH:MM for a reminder"

// Parse parses a --when value such as "tomorrow", "evening", "2026-12-01@09:30" or "@18:00".
func Parse(s string, today thingsdb.Date) (When, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	base, clock, hasClock := strings.Cut(s, "@")
	var w When
	if hasClock {
		h, m, ok := parseClock(clock)
		if !ok {
			return When{}, fmt.Errorf("invalid reminder time %q; use @HH:MM (24h)", clock)
		}
		w.HasReminder, w.Reminder = true, thingsdb.NewClock(h, m)
	}
	switch base {
	case "", "today":
		if base == "" && !hasClock {
			return When{}, fmt.Errorf("empty when; valid: %s", WhenForms)
		}
		w.Kind, w.Date = KindDate, today
	case "evening", "this evening", "tonight":
		w.Kind, w.Date = KindEvening, today
	case "anytime":
		w.Kind = KindAnytime
	case "someday":
		w.Kind = KindSomeday
	default:
		d, err := Day(base, today)
		if err != nil || d < today {
			return When{}, fmt.Errorf("invalid when %q; valid: %s", s, WhenForms)
		}
		w.Kind, w.Date = KindDate, d
	}
	if w.HasReminder && (w.Kind == KindAnytime || w.Kind == KindSomeday) {
		return When{}, fmt.Errorf("a reminder needs a date: use e.g. today@%s", clock)
	}
	return w, nil
}

func parseClock(s string) (int, int, bool) {
	hs, ms, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(hs)
	m, err2 := strconv.Atoi(ms)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// URLValue renders the value for the Things URL scheme "when" parameter.
// A reminder always uses an explicit ISO date, which Things documents.
func (w When) URLValue() string {
	switch {
	case w.HasReminder:
		return fmt.Sprintf("%s@%s", w.Date.ISO(), w.Reminder)
	case w.Kind == KindEvening:
		return "evening"
	case w.Kind == KindAnytime:
		return "anytime"
	case w.Kind == KindSomeday:
		return "someday"
	}
	return w.Date.ISO()
}

// NeedsURL reports whether only the URL scheme can express this value
// (AppleScript has no evening or reminder support).
func (w When) NeedsURL() bool { return w.HasReminder || w.Kind == KindEvening }
