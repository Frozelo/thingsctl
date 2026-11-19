package write

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// world is everything a planner needs from one read snapshot. It is loaded
// before any write and the snapshot is closed before Things is touched.
type world struct {
	snap     *thingsdb.Snap
	today    thingsdb.Date
	ids      *shortid.Index
	projects []thingsdb.Task
	areas    []thingsdb.Area
	tags     []thingsdb.Tag
	token    string
}

func loadWorld(s *thingsdb.Snap) (*world, error) {
	all, err := s.AllIDs()
	if err != nil {
		return nil, err
	}
	w := &world{snap: s, today: s.Today(), ids: shortid.New(all)}
	if w.projects, err = s.Tasks(thingsdb.ViewProjects); err != nil {
		return nil, err
	}
	if w.areas, err = s.Areas(); err != nil {
		return nil, err
	}
	if w.tags, err = s.Tags(); err != nil {
		return nil, err
	}
	if w.token, err = s.Token(); err != nil {
		return nil, err
	}
	return w, nil
}

// container is a resolved project or area.
type container struct {
	kind  string // "project", "area" or "inbox"
	id    string
	title string
}

// resolveContainer accepts an id prefix or an exact (case-insensitive) title.
func (w *world) resolveContainer(ref string, allowInbox bool) (container, error) {
	if allowInbox && strings.EqualFold(ref, "inbox") {
		return container{kind: "inbox"}, nil
	}
	if id, err := w.ids.Resolve(ref); err == nil {
		for _, p := range w.projects {
			if p.UUID == id {
				return container{"project", p.UUID, p.Title}, nil
			}
		}
		for _, a := range w.areas {
			if a.UUID == id {
				return container{"area", a.UUID, a.Title}, nil
			}
		}
	}
	var found []container
	for _, p := range w.projects {
		if strings.EqualFold(p.Title, ref) {
			found = append(found, container{"project", p.UUID, p.Title})
		}
	}
	for _, a := range w.areas {
		if strings.EqualFold(a.Title, ref) {
			found = append(found, container{"area", a.UUID, a.Title})
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return container{}, fmt.Errorf("no open project or area %q (see: thingsctl read projects / areas)", ref)
	}
	var c []string
	for _, f := range found {
		c = append(c, f.kind+" "+w.ids.Short(f.id))
	}
	return container{}, fmt.Errorf("%q matches several: %s; pass the id", ref, strings.Join(c, ", "))
}

// resolveTags maps comma-separated names to existing tag titles. Things
// silently drops unknown tags, so they are rejected up front.
func (w *world) resolveTags(csv string) ([]string, error) {
	var out, unknown []string
	for _, raw := range strings.Split(csv, ",") {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		if name == "" {
			continue
		}
		t, ok := w.tag(name)
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		out = append(out, t.Title)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown tags: %s (create with: thingsctl add <name> --type tag)", strings.Join(unknown, ", "))
	}
	return out, nil
}

func (w *world) tag(name string) (thingsdb.Tag, bool) {
	name = strings.ReplaceAll(name, "_", " ")
	for _, t := range w.tags {
		if strings.EqualFold(t.Title, name) || strings.EqualFold(t.Title, strings.ReplaceAll(name, " ", "_")) {
			return t, true
		}
	}
	return thingsdb.Tag{}, false
}

// resolveTagRef accepts a tag id or name.
func (w *world) resolveTagRef(ref string) (thingsdb.Tag, error) {
	if id, err := w.ids.Resolve(ref); err == nil {
		for _, t := range w.tags {
			if t.UUID == id {
				return t, nil
			}
		}
	}
	if t, ok := w.tag(strings.TrimPrefix(ref, "#")); ok {
		return t, nil
	}
	return thingsdb.Tag{}, fmt.Errorf("no tag %q (see: thingsctl read tags)", ref)
}

// splitList splits checklist or item lists on newlines, or on ';' when there are none.
func splitList(s string) []string {
	sep := ";"
	if strings.Contains(s, "\n") {
		sep = "\n"
	}
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var errNeedToken = errors.New("this change needs the Things URL scheme: enable Things → Settings → General → Enable Things URLs, then retry")
