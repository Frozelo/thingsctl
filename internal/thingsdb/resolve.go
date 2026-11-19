package thingsdb

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Frozelo/thingsctl/internal/shortid"
)

// ResolveItem turns a reference into a uuid. The reference is a short or
// full id, or else the exact title (any case) of a to-do, project, area or
// tag. Open to-dos and projects win over closed ones; several matches of
// the same rank are an error that lists them.
func (s *Snap) ResolveItem(ids *shortid.Index, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	id, idErr := ids.Resolve(ref)
	if idErr == nil {
		return id, nil
	}
	var amb *shortid.AmbiguousError
	if errors.As(idErr, &amb) {
		return "", idErr
	}
	type match struct{ uuid, where string }
	var open, closed, other []match
	rows, err := s.tx.QueryContext(s.ctx, `SELECT t.uuid, t.title, t.status,
			coalesce(p.title, ''), coalesce(a.title, '')
		FROM TMTask t
		LEFT JOIN TMTask h ON h.uuid = t.heading
		LEFT JOIN TMTask p ON p.uuid = coalesce(t.project, h.project)
		LEFT JOIN TMArea a ON a.uuid = coalesce(t.area, p.area)
		WHERE t.trashed = 0 AND t.type IN (0, 1) AND length(t.title) = ?`, utf8.RuneCountInString(ref))
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var uuid, title, project, area string
		var status Status
		if err := rows.Scan(&uuid, &title, &status, &project, &area); err != nil {
			rows.Close()
			return "", err
		}
		if !strings.EqualFold(title, ref) {
			continue
		}
		where := project
		if where == "" {
			where = area
		}
		m := match{uuid, where}
		if status == StatusOpen {
			open = append(open, m)
		} else {
			closed = append(closed, m)
		}
	}
	rows.Close()
	for _, q := range []string{`SELECT uuid, title FROM TMArea`, `SELECT uuid, title FROM TMTag`} {
		rows, err := s.tx.QueryContext(s.ctx, q)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var uuid, title string
			rows.Scan(&uuid, &title)
			if strings.EqualFold(title, ref) {
				other = append(other, match{uuid: uuid})
			}
		}
		rows.Close()
	}
	for _, ms := range [][]match{open, closed, other} {
		switch len(ms) {
		case 0:
			continue
		case 1:
			return ms[0].uuid, nil
		}
		c := make([]string, 0, 5)
		for _, m := range ms[:min(5, len(ms))] {
			if m.where != "" {
				c = append(c, fmt.Sprintf("%s (in:%q)", ids.Short(m.uuid), m.where))
			} else {
				c = append(c, ids.Short(m.uuid))
			}
		}
		return "", fmt.Errorf("%d items are titled %q: %s; pass an id", len(ms), ref, strings.Join(c, ", "))
	}
	return "", fmt.Errorf("no item with id or title %q", ref)
}
