package thingsdb

import (
	"fmt"
	"strings"

	"github.com/Frozelo/thingsctl/internal/shortid"
)

// SelectForms describes what Select accepts.
const SelectForms = "inbox, today, upcoming, anytime, someday, deadlines, logbook, tag:<name>, project:<id or title>, area:<id or title>, q:<text>"

// Select returns the to-dos a bulk change applies to: those of a list view,
// or the open to-dos with a tag, in a project or area, or matching a search.
// Projects, headings and repeating templates are never selected.
func (s *Snap) Select(ids *shortid.Index, where string) ([]Task, error) {
	where = strings.TrimSpace(where)
	key, val, hasKey := strings.Cut(where, ":")
	key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
	var tasks []Task
	var keep func(Task) bool
	var err error
	switch {
	case !hasKey:
		switch v := View(strings.ToLower(where)); v {
		case ViewInbox, ViewToday, ViewUpcoming, ViewAnytime, ViewSomeday, ViewDeadlines, ViewLogbook:
			tasks, err = s.Tasks(v)
		default:
			return nil, fmt.Errorf("invalid where %q; use one of: %s", where, SelectForms)
		}
	case val == "":
		return nil, fmt.Errorf("where %q needs a value after the colon", where)
	case key == "tag":
		want := strings.ReplaceAll(strings.TrimPrefix(val, "#"), "_", " ")
		tasks, err = s.Tasks(ViewOpen)
		keep = func(t Task) bool {
			for _, tag := range t.Tags {
				if strings.EqualFold(tag, want) {
					return true
				}
			}
			return false
		}
	case key == "project" || key == "area":
		id, rerr := s.containerByTitle(key, val)
		if id == "" && rerr == nil {
			id, rerr = s.ResolveItem(ids, val)
		}
		if rerr != nil {
			return nil, rerr
		}
		kind, kerr := s.KindOf(id)
		if kerr != nil {
			return nil, kerr
		}
		if key == "project" {
			if t, terr := s.Task(id); kind != ObjTask || terr != nil || t.Kind != KindProject {
				return nil, fmt.Errorf("%q is not a project", val)
			}
			tasks, err = s.ProjectTasks(id)
		} else {
			if kind != ObjArea {
				return nil, fmt.Errorf("%q is not an area", val)
			}
			tasks, err = s.Tasks(ViewOpen)
			keep = func(t Task) bool { return t.AreaID == id }
		}
	case key == "q":
		q := strings.ToLower(val)
		tasks, err = s.Tasks(ViewOpen)
		keep = func(t Task) bool {
			return strings.Contains(strings.ToLower(t.Title), q) || strings.Contains(strings.ToLower(t.Notes), q)
		}
	default:
		return nil, fmt.Errorf("invalid where %q; use one of: %s", where, SelectForms)
	}
	if err != nil {
		return nil, err
	}
	out := tasks[:0]
	for _, t := range tasks {
		if t.Kind == KindTodo && !t.RepeatingTemplate && (keep == nil || keep(t)) {
			out = append(out, t)
		}
	}
	return out, nil
}

// containerByTitle finds an open project or an area by exact title, so that
// project:/area: are not confused by a tag or to-do with the same name.
// It returns "" when nothing matches by title.
func (s *Snap) containerByTitle(kind, title string) (string, error) {
	var found []string
	if kind == "area" {
		areas, err := s.Areas()
		if err != nil {
			return "", err
		}
		for _, a := range areas {
			if strings.EqualFold(a.Title, title) {
				found = append(found, a.UUID)
			}
		}
	} else {
		projects, err := s.Tasks(ViewProjects)
		if err != nil {
			return "", err
		}
		for _, p := range projects {
			if strings.EqualFold(p.Title, title) {
				found = append(found, p.UUID)
			}
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("%d %ss are titled %q; pass an id", len(found), kind, title)
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", nil
}
