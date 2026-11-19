// Package render prints Things data in a compact, line-based form designed to
// cost as few model tokens as possible while staying unambiguous:
//
//	today=11-19 today:3
//	a3f9 Buy milk !11-22 #home
//	Work b2c4:
//	 c9d1 Draft report [1/3] ~rep
//	evening:
//	 d0e1 Call mom @18:00
//
// Dates are absolute (MM-DD, or YYYY-MM-DD outside the current year); the
// header states today's date once. Empty fields are omitted.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// Ctx carries what every line needs.
type Ctx struct {
	Today thingsdb.Date
	IDs   *shortid.Index
}

// Date formats a date relative to the current year.
func (c Ctx) Date(d thingsdb.Date) string {
	if d.Year() == c.Today.Year() {
		return fmt.Sprintf("%02d-%02d", int(d.Month()), d.Day())
	}
	return d.ISO()
}

// Header writes "today=MM-DD <name>:<total>[ showing:a-b]".
func (c Ctx) Header(w io.Writer, name string, total, from, to int) {
	fmt.Fprintf(w, "today=%s %s:%d", c.Date(c.Today), name, total)
	if from > 0 || to < total {
		fmt.Fprintf(w, " showing:%d-%d", from+1, to)
	}
	fmt.Fprintln(w)
}

// Footer writes the "+N more" hint when a list was cut.
func (c Ctx) Footer(w io.Writer, total, to int) {
	if to < total {
		fmt.Fprintf(w, "+%d more (--offset %d)\n", total-to, to)
	}
}

// LineOpts selects optional parts of a task line.
type LineOpts struct {
	When      bool // show the start date
	Status    bool // show done/canceled with date
	InProject bool // project lines inside the projects view: "[N]" instead of "[proj N]"
}

// Line formats one task.
func (c Ctx) Line(t thingsdb.Task, o LineOpts) string {
	var b strings.Builder
	b.WriteString(c.IDs.Short(t.UUID))
	b.WriteByte(' ')
	b.WriteString(oneLine(t.Title))
	if t.Kind == thingsdb.KindProject {
		if o.InProject {
			fmt.Fprintf(&b, " [%d]", t.OpenChildren)
		} else {
			fmt.Fprintf(&b, " [proj %d]", t.OpenChildren)
		}
	}
	switch {
	case o.When && !t.StartDate.IsZero():
		b.WriteString(" @" + c.Date(t.StartDate))
		if t.HasReminder {
			b.WriteString(" " + t.Reminder.String())
		}
	case t.HasReminder:
		b.WriteString(" @" + t.Reminder.String())
	}
	if o.When && t.Evening {
		b.WriteString(" eve")
	}
	if !t.Deadline.IsZero() {
		b.WriteString(" !" + c.Date(t.Deadline))
	}
	for _, tag := range t.Tags {
		b.WriteString(" #" + TagName(tag))
	}
	if t.ChecklistTotal > 0 {
		fmt.Fprintf(&b, " [%d/%d]", t.ChecklistTotal-t.ChecklistOpen, t.ChecklistTotal)
	}
	if t.RepeatingInstance || t.RepeatingTemplate {
		b.WriteString(" ~rep")
	}
	if o.Status {
		switch t.Status {
		case thingsdb.StatusDone:
			b.WriteString(" done:" + c.Date(thingsdb.DateOf(t.Stopped)))
		case thingsdb.StatusCanceled:
			b.WriteString(" canceled:" + c.Date(thingsdb.DateOf(t.Stopped)))
		}
	}
	return b.String()
}

// TagName renders a tag as a single token (spaces become underscores).
func TagName(s string) string { return strings.ReplaceAll(s, " ", "_") }

func oneLine(s string) string {
	if strings.ContainsAny(s, "\r\n") {
		s = strings.Join(strings.Fields(s), " ")
	}
	return s
}

type group struct {
	key, title, id string
	items          []thingsdb.Task
}

// Grouped writes tasks grouped by project (or area for loose items), keeping
// first-appearance order; loose items with neither come first, unindented.
func (c Ctx) Grouped(w io.Writer, tasks []thingsdb.Task, o LineOpts) {
	var loose []thingsdb.Task
	var groups []*group
	byKey := map[string]*group{}
	for _, t := range tasks {
		key, title := t.ProjectID, t.ProjectTitle
		if t.Kind == thingsdb.KindProject || key == "" {
			key, title = t.AreaID, t.AreaTitle
		}
		if key == "" {
			loose = append(loose, t)
			continue
		}
		g := byKey[key]
		if g == nil {
			g = &group{key: key, title: title, id: c.IDs.Short(key)}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.items = append(g.items, t)
	}
	for _, t := range loose {
		fmt.Fprintln(w, c.Line(t, o))
	}
	for _, g := range groups {
		fmt.Fprintf(w, "%s %s:\n", oneLine(g.title), g.id)
		for _, t := range g.items {
			fmt.Fprintln(w, " "+c.Line(t, o))
		}
	}
}

// Detail writes the full form of one task.
func (c Ctx) Detail(w io.Writer, t thingsdb.Task) {
	kind := map[thingsdb.Kind]string{thingsdb.KindTodo: "todo", thingsdb.KindProject: "project", thingsdb.KindHeading: "heading"}[t.Kind]
	status := map[thingsdb.Status]string{thingsdb.StatusOpen: "open", thingsdb.StatusDone: "done", thingsdb.StatusCanceled: "canceled"}[t.Status]
	if t.Trashed {
		status += " trashed"
	}
	fmt.Fprintf(w, "%s %s %s\n", c.IDs.Short(t.UUID), kind, status)
	fmt.Fprintf(w, "title: %s\n", oneLine(t.Title))
	if t.ProjectID != "" {
		fmt.Fprintf(w, "project: %s %s\n", oneLine(t.ProjectTitle), c.IDs.Short(t.ProjectID))
	}
	if t.HeadingID != "" {
		fmt.Fprintf(w, "heading: %s\n", oneLine(t.HeadingTitle))
	}
	if t.AreaID != "" {
		fmt.Fprintf(w, "area: %s %s\n", oneLine(t.AreaTitle), c.IDs.Short(t.AreaID))
	}
	when := ""
	switch {
	case !t.StartDate.IsZero():
		when = c.Date(t.StartDate)
	case t.Status == thingsdb.StatusOpen && t.Start == thingsdb.StartInbox && t.Kind == thingsdb.KindTodo:
		when = "inbox"
	case t.Status == thingsdb.StatusOpen && t.Start == thingsdb.StartSomeday:
		when = "someday"
	case t.Status == thingsdb.StatusOpen && t.Start == thingsdb.StartAnytime:
		when = "anytime"
	}
	if t.Evening {
		when += " evening"
	}
	if t.HasReminder {
		when += " " + t.Reminder.String()
	}
	if when != "" {
		fmt.Fprintf(w, "when: %s\n", strings.TrimSpace(when))
	}
	if !t.Deadline.IsZero() {
		fmt.Fprintf(w, "deadline: %s\n", c.Date(t.Deadline))
	}
	if len(t.Tags) > 0 {
		names := make([]string, len(t.Tags))
		for i, tag := range t.Tags {
			names[i] = TagName(tag)
		}
		fmt.Fprintf(w, "tags: %s\n", strings.Join(names, " "))
	}
	switch {
	case t.RepeatingTemplate:
		fmt.Fprintln(w, "repeat: template (read-only; edit the rule in Things)")
	case t.RepeatingInstance:
		fmt.Fprintln(w, "repeat: instance (when/deadline/complete are locked by Things)")
	}
	if t.Kind == thingsdb.KindProject {
		fmt.Fprintf(w, "open: %d\n", t.OpenChildren)
	}
	if len(t.Checklist) > 0 {
		done := 0
		for _, ci := range t.Checklist {
			if ci.Status != thingsdb.StatusOpen {
				done++
			}
		}
		fmt.Fprintf(w, "checklist %d/%d:\n", done, len(t.Checklist))
		for _, ci := range t.Checklist {
			mark := "-"
			if ci.Status == thingsdb.StatusDone {
				mark = "x"
			} else if ci.Status == thingsdb.StatusCanceled {
				mark = "~"
			}
			fmt.Fprintf(w, " %s %s\n", mark, oneLine(ci.Title))
		}
	}
	dates := []string{}
	if !t.Created.IsZero() {
		dates = append(dates, "created:"+c.Date(thingsdb.DateOf(t.Created)))
	}
	if !t.Modified.IsZero() {
		dates = append(dates, "modified:"+c.Date(thingsdb.DateOf(t.Modified)))
	}
	if !t.Stopped.IsZero() && t.Status != thingsdb.StatusOpen {
		dates = append(dates, map[thingsdb.Status]string{thingsdb.StatusDone: "done:", thingsdb.StatusCanceled: "canceled:"}[t.Status]+c.Date(thingsdb.DateOf(t.Stopped)))
	}
	if len(dates) > 0 {
		fmt.Fprintln(w, strings.Join(dates, " "))
	}
	if n := strings.TrimSpace(t.Notes); n != "" {
		fmt.Fprintf(w, "notes:\n%s\n", n)
	}
}
