package when

import (
	"testing"
	"time"

	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

var today = thingsdb.NewDate(2026, time.November, 19)

func TestDay(t *testing.T) {
	for in, want := range map[string]string{
		"today": "2026-11-19", "tomorrow": "2026-11-20", "yesterday": "2026-11-18",
		"+3d": "2026-11-22", "-19d": "2026-10-31", "2027-01-05": "2027-01-05",
	} {
		d, err := Day(in, today)
		if err != nil || d.ISO() != want {
			t.Errorf("Day(%q) = %s, %v; want %s", in, d.ISO(), err, want)
		}
	}
	if _, err := Day("next tuesday", today); err == nil {
		t.Error("natural language must be rejected")
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		url      string
		needsURL bool
	}{
		{"today", "2026-11-19", false},
		{"tomorrow", "2026-11-20", false},
		{"+2d", "2026-11-21", false},
		{"evening", "evening", true},
		{"anytime", "anytime", false},
		{"someday", "someday", false},
		{"@18:00", "2026-11-19@18:00", true},
		{"2026-12-01@09:30", "2026-12-01@09:30", true},
	}
	for _, c := range cases {
		w, err := Parse(c.in, today)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if w.URLValue() != c.url || w.NeedsURL() != c.needsURL {
			t.Errorf("Parse(%q) = url %q needsURL %v", c.in, w.URLValue(), w.NeedsURL())
		}
	}
	for _, bad := range []string{"", "yesterday", "someday@10:00", "today@25:00", "soon"} {
		if _, err := Parse(bad, today); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}
