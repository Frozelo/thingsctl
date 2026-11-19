package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Frozelo/thingsctl/internal/testfixture"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	path := testfixture.Build(t)
	OpenDB = func() (*thingsdb.DB, error) { return thingsdb.Open(path) }
	Now = testfixture.Now
	var out, errb bytes.Buffer
	code := Main(args, &out, &errb)
	return out.String(), errb.String(), code
}

func wantLines(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n--- output ---\n%s", w, out)
		}
	}
}

func TestReadToday(t *testing.T) {
	out, _, code := run(t, "read", "today")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	wantLines(t, out,
		"today=11-19 today:5\n",
		"Tpas Scheduled past\n",
		"Tdue Pay bill !11-18\n",
		"Work Pwor:\n Ttod Draft report #urgent [1/2]\n",
		"Rins Daily standup ~rep\n",
		"evening:\n Teve Call mom @18:00\n",
	)
}

func TestReadUpcomingShowsDatesAndParents(t *testing.T) {
	out, _, _ := run(t, "read", "upcoming")
	wantLines(t, out,
		"today=11-19 upcoming:2\n",
		"Rtmp Daily standup @11-20 ~rep\n",
		"Tup0 Dentist @11-22 09:30 in:Ahom\n",
	)
}

func TestReadSearchAndFilters(t *testing.T) {
	out, _, _ := run(t, "read", "-q", "MILK")
	wantLines(t, out, "today=11-19 search:1\n", "Work Pwor:\n Tany Anytime under heading #home\n")

	out, _, _ = run(t, "read", "--tag", "errand_list")
	wantLines(t, out, "search:1\n", "Tsom Someday loose #errand_list\n")

	out, _, _ = run(t, "read", "anytime", "--project", "work")
	wantLines(t, out, "anytime:2\n")
}

func TestReadPagination(t *testing.T) {
	out, _, _ := run(t, "read", "logbook", "--limit", "1", "--offset", "1")
	wantLines(t, out, "logbook:3 showing:2-2\n", "Pdon Done Proj [proj 0] done:11-19\n", "+1 more (--offset 2)\n")
}

func TestReadDetailByShortID(t *testing.T) {
	out, _, code := run(t, "read", "--id", "Ttod")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	wantLines(t, out,
		"Ttod todo open\n", "title: Draft report\n", "project: Work Pwor\n", "when: 11-19\n",
		"tags: urgent\n", "checklist 1/2:\n x outline\n - numbers\n",
	)

	out, _, _ = run(t, "read", "--id", "Pwork")
	wantLines(t, out, "Pwor project open\n", "items:\n", "Ttod Draft report @11-19", "Phase 1:\n Tany Anytime under heading")
}

func TestReadSummary(t *testing.T) {
	out, _, _ := run(t, "read")
	want := "today=11-19 inbox:1 today:5 upcoming:2 anytime:5 someday:2 deadlines:1 overdue:1 projects:2 areas:1 tags:3\n"
	if out != want {
		t.Errorf("summary\n got %q\nwant %q", out, want)
	}
}

func TestReadErrors(t *testing.T) {
	_, errOut, code := run(t, "read", "bogus")
	if code != 1 || !strings.Contains(errOut, `invalid view "bogus"; valid: summary, inbox`) {
		t.Errorf("bad view: code=%d err=%q", code, errOut)
	}
	_, errOut, code = run(t, "read", "--id", "T")
	if code != 1 || !strings.Contains(errOut, "ambiguous") {
		t.Errorf("ambiguous id: code=%d err=%q", code, errOut)
	}
	_, errOut, _ = run(t, "read", "--modified", "last week")
	if !strings.Contains(errOut, "valid: YYYY-MM-DD") {
		t.Errorf("bad date: %q", errOut)
	}
}

func TestReadHelpNeedsNoDatabase(t *testing.T) {
	OpenDB = func() (*thingsdb.DB, error) { t.Fatal("help must not open the database"); return nil, nil }
	var out bytes.Buffer
	if code := Main([]string{"read", "help"}, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	wantLines(t, out.String(), "output format:", "--modified S")
}
