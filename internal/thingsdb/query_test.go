package thingsdb_test

import (
	"context"
	"slices"
	"testing"
	"time"

	f "github.com/Frozelo/thingsctl/internal/testfixture"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

func snap(t *testing.T) *thingsdb.Snap {
	t.Helper()
	s, err := f.Open(t).Snap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ids(ts []thingsdb.Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.UUID
	}
	return out
}

func TestViews(t *testing.T) {
	s := snap(t)
	cases := []struct {
		view    thingsdb.View
		want    []string
		ordered bool
	}{
		{thingsdb.ViewInbox, []string{f.TodoInbox}, true},
		{thingsdb.ViewToday, []string{f.TodoPast, f.TodoDue, f.TodoToday, f.RepInst, f.TodoEve}, true},
		{thingsdb.ViewUpcoming, []string{f.RepTmpl, f.TodoUp}, true},
		{thingsdb.ViewAnytime, []string{f.TodoToday, f.TodoEve, f.TodoDue, f.TodoAny, f.RepInst}, false},
		{thingsdb.ViewSomeday, []string{f.ProjSome, f.TodoSome}, false},
		{thingsdb.ViewLogbook, []string{f.TodoCxl, f.ProjDone, f.TodoDone}, true},
		{thingsdb.ViewTrash, []string{f.TodoTrash}, true},
		{thingsdb.ViewDeadlines, []string{f.TodoDue}, true},
		{thingsdb.ViewProjects, []string{f.ProjWork, f.ProjSome}, false},
	}
	for _, c := range cases {
		t.Run(string(c.view), func(t *testing.T) {
			got, err := s.Tasks(c.view)
			if err != nil {
				t.Fatal(err)
			}
			g, w := ids(got), slices.Clone(c.want)
			if !c.ordered {
				slices.Sort(g)
				slices.Sort(w)
			}
			if !slices.Equal(g, w) {
				t.Errorf("got %v\nwant %v", g, w)
			}
		})
	}
}

func TestTaskResolution(t *testing.T) {
	s := snap(t)
	any, err := s.Task(f.TodoAny)
	if err != nil {
		t.Fatal(err)
	}
	if any.ProjectID != f.ProjWork || any.HeadingTitle != "Phase 1" {
		t.Errorf("heading child should inherit project: got project=%q heading=%q", any.ProjectID, any.HeadingTitle)
	}
	if !slices.Equal(any.Tags, []string{"home"}) {
		t.Errorf("tags = %v", any.Tags)
	}
	today, err := s.Task(f.TodoToday)
	if err != nil {
		t.Fatal(err)
	}
	if len(today.Checklist) != 2 || today.Checklist[0].Status != thingsdb.StatusDone {
		t.Errorf("checklist = %+v", today.Checklist)
	}
	eve, _ := s.Task(f.TodoEve)
	if !eve.Evening || !eve.HasReminder || eve.Reminder.String() != "18:00" {
		t.Errorf("evening/reminder = %v %v %v", eve.Evening, eve.HasReminder, eve.Reminder)
	}
	tmpl, _ := s.Task(f.RepTmpl)
	if !tmpl.RepeatingTemplate || tmpl.StartDate != f.Today.AddDays(1) {
		t.Errorf("template next date = %v", tmpl.StartDate.ISO())
	}
	if _, err := s.Task("nope"); err != thingsdb.ErrNoObject {
		t.Errorf("missing task err = %v", err)
	}
}

func TestKindOfAndMeta(t *testing.T) {
	s := snap(t)
	for id, want := range map[string]thingsdb.ObjectKind{
		f.TodoInbox: thingsdb.ObjTask, f.AreaHome: thingsdb.ObjArea, f.TagHome: thingsdb.ObjTag, f.Check1: thingsdb.ObjChecklist,
	} {
		if got, err := s.KindOf(id); err != nil || got != want {
			t.Errorf("KindOf(%s) = %v, %v", id, got, err)
		}
	}
	if v, err := s.Version(); err != nil || v != 29 {
		t.Errorf("Version = %d, %v", v, err)
	}
	if m, err := s.MissingColumns(); err != nil || len(m) != 0 {
		t.Errorf("MissingColumns = %v, %v", m, err)
	}
	areas, _ := s.Areas()
	if len(areas) != 1 || areas[0].OpenProjects != 1 || areas[0].OpenTodos != 3 {
		t.Errorf("areas = %+v", areas)
	}
}

func TestDatePacking(t *testing.T) {
	if d := thingsdb.Date(132823552); d.ISO() != "2026-11-20" {
		t.Errorf("decode = %s", d.ISO())
	}
	if c := thingsdb.Clock(671088640); c.String() != "10:00" {
		t.Errorf("clock = %s", c)
	}
	if d := thingsdb.NewDate(2026, time.December, 31).AddDays(1); d.ISO() != "2027-01-01" {
		t.Errorf("AddDays = %s", d.ISO())
	}
}
