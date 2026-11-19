//go:build eval

package eval

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/Frozelo/thingsctl/internal/command"
	"github.com/Frozelo/thingsctl/internal/render"
	"github.com/Frozelo/thingsctl/internal/shortid"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
	"github.com/Frozelo/thingsctl/internal/when"
	"github.com/Frozelo/thingsctl/internal/write"
)

// Sim applies validated thingsctl commands straight to a database file, the
// way Things would, so that an agent's follow-up reads see its writes.
// It is deliberately simple: the eval needs plausible state, not fidelity.
type Sim struct {
	Path string
}

func (s *Sim) Apply(ctx context.Context, v any, w io.Writer) error {
	db, err := sql.Open("sqlite", s.Path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st := &simTx{tx: tx, today: thingsdb.DateOf(time.Now()), now: float64(time.Now().Unix())}
	var receipt func() string
	switch c := v.(type) {
	case *command.Add:
		receipt, err = st.add(*c)
	case *command.Update:
		receipt, err = st.update(*c)
	case *command.Trash:
		id := st.resolve(c.ID)
		st.exec(`UPDATE TMTask SET trashed=1, userModificationDate=? WHERE uuid=?`, st.now, id)
		receipt = func() string { return "ok " + s.line(ctx, id, write.Echo{}) }
	case *command.Show:
		receipt = func() string { return "ok shown " + c.Target }
	case *command.Batch:
		receipt, err = st.batch(c.JSON)
	default:
		return fmt.Errorf("sim: %T", v)
	}
	if err == nil {
		err = st.err
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	out := receipt()
	out = strings.ReplaceAll(out, "\x00", "")
	for _, id := range st.lines {
		out = strings.Replace(out, "\x01"+id, s.line(ctx, id, write.EchoFor(v)), 1)
	}
	fmt.Fprint(w, out)
	return nil
}

func (s *Sim) line(ctx context.Context, id string, echo write.Echo) string {
	db, err := thingsdb.Open(s.Path)
	if err != nil {
		return id
	}
	defer db.Close()
	return write.Receipt(ctx, db, id, echo)
}

type simTx struct {
	tx    *sql.Tx
	today thingsdb.Date
	now   float64
	err   error
	lines []string // ids whose receipt lines are rendered after commit
}

func (s *simTx) exec(q string, a ...any) {
	if s.err != nil {
		return
	}
	if _, err := s.tx.Exec(q, a...); err != nil {
		s.err = fmt.Errorf("sim: %s: %w", q, err)
	}
}

func (s *simTx) one(q string, a ...any) string {
	var v sql.NullString
	if s.err == nil {
		if err := s.tx.QueryRow(q, a...).Scan(&v); err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.err = err
		}
	}
	return v.String
}

func (s *simTx) strings(q string, a ...any) []string {
	var out []string
	rows, err := s.tx.Query(q, a...)
	if err != nil {
		s.err = err
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		rows.Scan(&v)
		out = append(out, v)
	}
	return out
}

func newID() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz123456789"
	b := make([]byte, 22)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

func (s *simTx) resolve(ref string) string {
	all := s.strings(`SELECT uuid FROM TMTask UNION ALL SELECT uuid FROM TMArea UNION ALL SELECT uuid FROM TMTag`)
	if id, err := shortid.New(all).Resolve(strings.TrimSpace(ref)); err == nil {
		return id
	}
	return ref
}

// container returns (project, area) for an --in reference.
func (s *simTx) container(ref string) (project, area string) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.EqualFold(ref, "inbox") {
		return "", ""
	}
	if id := s.one(`SELECT uuid FROM TMTask WHERE type=1 AND trashed=0 AND lower(title)=lower(?)`, ref); id != "" {
		return id, ""
	}
	if id := s.one(`SELECT uuid FROM TMArea WHERE lower(title)=lower(?)`, ref); id != "" {
		return "", id
	}
	id := s.resolve(ref)
	if s.one(`SELECT uuid FROM TMArea WHERE uuid=?`, id) != "" {
		return "", id
	}
	return id, ""
}

func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// setWhen writes start/startDate/startBucket/reminderTime for a when value.
func (s *simTx) setWhen(id, v string) {
	w, err := when.Parse(v, s.today)
	if err != nil {
		return
	}
	start, date, bucket, rem := 1, any(nil), 0, any(nil)
	switch w.Kind {
	case when.KindDate:
		date = int64(w.Date)
		if w.Date > s.today {
			start = 2
		}
	case when.KindEvening:
		date, bucket = int64(w.Date), 1
	case when.KindSomeday:
		start = 2
	}
	if w.HasReminder {
		rem = int64(w.Reminder)
	}
	s.exec(`UPDATE TMTask SET start=?, startDate=?, startBucket=?, reminderTime=? WHERE uuid=?`, start, date, bucket, rem, id)
}

func (s *simTx) setDeadline(id, v string) {
	if v == "none" {
		s.exec(`UPDATE TMTask SET deadline=NULL WHERE uuid=?`, id)
		return
	}
	if d, err := when.Day(v, s.today); err == nil {
		s.exec(`UPDATE TMTask SET deadline=? WHERE uuid=?`, int64(d), id)
	}
}

func (s *simTx) setTags(id string, names []string) {
	s.exec(`DELETE FROM TMTaskTag WHERE tasks=?`, id)
	for _, n := range names {
		if tag := s.one(`SELECT uuid FROM TMTag WHERE lower(title)=lower(?)`, n); tag != "" {
			s.exec(`INSERT INTO TMTaskTag(tasks,tags) VALUES(?,?)`, id, tag)
		}
	}
}

func (s *simTx) tagNames(id string) []string {
	return s.strings(`SELECT g.title FROM TMTaskTag x JOIN TMTag g ON g.uuid=x.tags WHERE x.tasks=?`, id)
}

func (s *simTx) setChecklist(id string, items []string, mode string) {
	existing := s.strings(`SELECT title FROM TMChecklistItem WHERE task=? ORDER BY "index"`, id)
	switch mode {
	case "append":
		items = append(existing, items...)
	case "prepend":
		items = append(items, existing...)
	}
	s.exec(`DELETE FROM TMChecklistItem WHERE task=?`, id)
	for i, it := range items {
		s.exec(`INSERT INTO TMChecklistItem(uuid,title,status,"index",task) VALUES(?,?,0,?,?)`, newID(), it, i+1, id)
	}
	s.exec(`UPDATE TMTask SET checklistItemsCount=?, openChecklistItemsCount=? WHERE uuid=?`, len(items), len(items), id)
}

func (s *simTx) insertTask(typ int, title, project, area, heading string) string {
	id := newID()
	start := 0
	if project != "" || area != "" || heading != "" {
		start = 1
	}
	s.exec(`INSERT INTO TMTask(uuid,type,status,trashed,start,title,project,area,heading,"index",creationDate,userModificationDate)
		VALUES(?,?,0,0,?,?,?,?,?,0,?,?)`, id, typ, start, title, nz(project), nz(area), nz(heading), s.now, s.now)
	return id
}

func (s *simTx) add(a command.Add) (func() string, error) {
	switch a.Type {
	case "area":
		id := newID()
		s.exec(`INSERT INTO TMArea(uuid,title,visible,"index") VALUES(?,?,1,0)`, id, a.Title)
		return func() string { return "ok " + id[:4] + " area " + a.Title }, nil
	case "tag":
		id := newID()
		parent := ""
		if a.In != "" {
			parent = s.one(`SELECT uuid FROM TMTag WHERE lower(title)=lower(?)`, a.In)
		}
		s.exec(`INSERT INTO TMTag(uuid,title,parent,"index") VALUES(?,?,?,0)`, id, a.Title, nz(parent))
		return func() string { return "ok " + id[:4] + " #" + render.TagName(a.Title) }, nil
	}
	project, area := s.container(a.In)
	heading := ""
	if a.Heading != "" && project != "" {
		heading = s.one(`SELECT uuid FROM TMTask WHERE type=2 AND project=? AND lower(title)=lower(?)`, project, a.Heading)
		if heading != "" {
			project = ""
		}
	}
	typ := 0
	if a.Type == "project" {
		typ, project = 1, ""
	}
	id := s.insertTask(typ, a.Title, project, area, heading)
	if a.Notes != "" {
		s.exec(`UPDATE TMTask SET notes=? WHERE uuid=?`, a.Notes, id)
	}
	if a.When != "" {
		s.setWhen(id, a.When)
	}
	if a.Deadline != "" {
		s.setDeadline(id, a.Deadline)
	}
	if a.Tags != "" {
		s.setTags(id, splitTags(a.Tags))
	}
	if a.Checklist != "" {
		s.setChecklist(id, splitList(a.Checklist), "set")
	}
	items := splitList(a.Items)
	for _, it := range items {
		s.insertTask(0, it, id, "", "")
	}
	if typ == 1 {
		s.exec(`UPDATE TMTask SET openUntrashedLeafActionsCount=? WHERE uuid=?`, len(items), id)
	}
	s.lines = append(s.lines, id)
	return func() string { return "ok \x01" + id }, nil
}

func (s *simTx) update(u command.Update) (func() string, error) {
	id := s.resolve(u.ID)
	if s.one(`SELECT uuid FROM TMArea WHERE uuid=?`, id) != "" {
		if u.Title != "" {
			s.exec(`UPDATE TMArea SET title=? WHERE uuid=?`, u.Title, id)
		}
		return func() string { return "ok " + id[:4] + " area " + u.Title }, nil
	}
	if s.one(`SELECT uuid FROM TMTag WHERE uuid=?`, id) != "" {
		if u.Title != "" {
			s.exec(`UPDATE TMTag SET title=? WHERE uuid=?`, u.Title, id)
		}
		return func() string { return "ok " + id[:4] + " #" + render.TagName(u.Title) }, nil
	}
	if u.Title != "" {
		s.exec(`UPDATE TMTask SET title=? WHERE uuid=?`, u.Title, id)
	}
	if u.Notes != "" {
		old := s.one(`SELECT coalesce(notes,'') FROM TMTask WHERE uuid=?`, id)
		notes := u.Notes
		switch {
		case u.NotesMode == "append" && old != "":
			notes = old + "\n" + u.Notes
		case u.NotesMode == "prepend" && old != "":
			notes = u.Notes + "\n" + old
		}
		s.exec(`UPDATE TMTask SET notes=? WHERE uuid=?`, notes, id)
	}
	if u.When != "" {
		s.setWhen(id, u.When)
	}
	if u.Deadline != "" {
		s.setDeadline(id, u.Deadline)
	}
	if u.Tags != "" {
		names := splitTags(u.Tags)
		cur := s.tagNames(id)
		switch u.TagsMode {
		case "add":
			for _, n := range names {
				if !slices.ContainsFunc(cur, func(c string) bool { return strings.EqualFold(c, n) }) {
					cur = append(cur, n)
				}
			}
			names = cur
		case "remove":
			names = slices.DeleteFunc(cur, func(c string) bool {
				return slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(c, n) })
			})
		}
		s.setTags(id, names)
	}
	if u.In != "" {
		project, area := s.container(u.In)
		start := s.one(`SELECT start FROM TMTask WHERE uuid=?`, id)
		if project == "" && area == "" {
			s.exec(`UPDATE TMTask SET project=NULL, area=NULL, heading=NULL, start=0, startDate=NULL WHERE uuid=?`, id)
		} else {
			if start == "0" {
				s.exec(`UPDATE TMTask SET start=1 WHERE uuid=?`, id)
			}
			s.exec(`UPDATE TMTask SET project=?, area=?, heading=NULL WHERE uuid=?`, nz(project), nz(area), id)
		}
	}
	if u.Heading != "" {
		project := s.one(`SELECT coalesce(t.project, h.project) FROM TMTask t LEFT JOIN TMTask h ON h.uuid=t.heading WHERE t.uuid=?`, id)
		if h := s.one(`SELECT uuid FROM TMTask WHERE type=2 AND project=? AND lower(title)=lower(?)`, project, u.Heading); h != "" {
			s.exec(`UPDATE TMTask SET heading=?, project=NULL WHERE uuid=?`, h, id)
		}
	}
	if u.Checklist != "" {
		s.setChecklist(id, splitList(u.Checklist), u.ChecklistMode)
	}
	switch u.Status {
	case "done":
		s.exec(`UPDATE TMTask SET status=3, stopDate=? WHERE uuid=?`, s.now, id)
	case "canceled":
		s.exec(`UPDATE TMTask SET status=2, stopDate=? WHERE uuid=?`, s.now, id)
	case "open":
		s.exec(`UPDATE TMTask SET status=0, stopDate=NULL WHERE uuid=?`, id)
	}
	s.exec(`UPDATE TMTask SET userModificationDate=? WHERE uuid=?`, s.now, id)
	s.lines = append(s.lines, id)
	return func() string { return "ok \x01" + id }, nil
}

// batch applies the subset of Things JSON that agents use.
func (s *simTx) batch(raw string) (func() string, error) {
	var items []struct {
		Type       string         `json:"type"`
		Operation  string         `json:"operation"`
		ID         string         `json:"id"`
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	join := func(v any, nested bool) string {
		switch x := v.(type) {
		case string:
			return x
		case []any:
			var ss []string
			for _, e := range x {
				if s, ok := e.(string); ok {
					ss = append(ss, s)
				} else if m, ok := e.(map[string]any); ok && nested {
					at, _ := m["attributes"].(map[string]any)
					ss = append(ss, str(at, "title"))
				}
			}
			if nested {
				return strings.Join(ss, "\n")
			}
			return strings.Join(ss, ",")
		}
		return ""
	}
	var created []string
	for _, it := range items {
		at := it.Attributes
		in := str(at, "list-id")
		for _, k := range []string{"list", "area-id", "area"} {
			if in == "" {
				in = str(at, k)
			}
		}
		if it.Operation == "update" {
			u := command.Update{ID: it.ID, Title: str(at, "title"), Notes: str(at, "notes"), When: str(at, "when"),
				Tags: join(at["tags"], false), In: in, Heading: str(at, "heading"), Checklist: join(at["checklist-items"], true)}
			if d, ok := at["deadline"]; ok {
				u.Deadline, _ = d.(string)
				if u.Deadline == "" {
					u.Deadline = "none"
				}
			}
			if t := join(at["add-tags"], false); t != "" {
				u.Tags, u.TagsMode = t, "add"
			}
			if n := str(at, "append-notes"); n != "" {
				u.Notes, u.NotesMode = n, "append"
			}
			if at["completed"] == true {
				u.Status = "done"
			}
			if at["canceled"] == true {
				u.Status = "canceled"
			}
			if _, err := s.update(u); err != nil {
				return nil, err
			}
			continue
		}
		a := command.Add{Title: str(at, "title"), Notes: str(at, "notes"), When: str(at, "when"), Deadline: str(at, "deadline"),
			Tags: join(at["tags"], false), In: in, Heading: str(at, "heading"), Checklist: join(at["checklist-items"], true)}
		if it.Type == "project" {
			a.Type, a.Items = "project", join(at["items"], true)
		}
		if _, err := s.add(a); err != nil {
			return nil, err
		}
		created = append(created, s.lines[len(s.lines)-1])
	}
	s.lines = nil
	return func() string {
		short := make([]string, len(created))
		for i, id := range created {
			short[i] = id[:4]
		}
		return fmt.Sprintf("sent %d items in 1 calls; new rows seen: %d %s", len(items), len(created), strings.Join(short, " "))
	}, nil
}
