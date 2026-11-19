package thingsdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Kind is TMTask.type.
type Kind int

const (
	KindTodo    Kind = 0
	KindProject Kind = 1
	KindHeading Kind = 2
)

// Status is TMTask.status / TMChecklistItem.status.
type Status int

const (
	StatusOpen     Status = 0
	StatusCanceled Status = 2
	StatusDone     Status = 3
)

// Start is TMTask.start: which list an open item lives in.
type Start int

const (
	StartInbox   Start = 0
	StartAnytime Start = 1
	StartSomeday Start = 2
)

// Task is a to-do, project or heading row with its resolved parents.
type Task struct {
	UUID    string
	Kind    Kind
	Status  Status
	Trashed bool
	Title   string
	Notes   string

	Start       Start
	StartDate   Date // "when"; for repeating templates, the next instance date
	Evening     bool
	HasReminder bool
	Reminder    Clock
	Deadline    Date

	Created  time.Time
	Modified time.Time
	Stopped  time.Time // completion or cancellation time

	ProjectID, ProjectTitle string
	AreaID, AreaTitle       string
	HeadingID, HeadingTitle string
	Tags                    []string

	ChecklistTotal int
	ChecklistOpen  int
	OpenChildren   int // projects: open to-dos inside

	RepeatingInstance bool
	RepeatingTemplate bool

	Checklist []ChecklistItem // filled only by Get
}

// ChecklistItem is one TMChecklistItem row.
type ChecklistItem struct {
	UUID   string
	Title  string
	Status Status
}

// Area is one TMArea row with counts of open items.
type Area struct {
	UUID         string
	Title        string
	OpenProjects int
	OpenTodos    int
	Tags         []string
}

// Tag is one TMTag row.
type Tag struct {
	UUID      string
	Title     string
	Shortcut  string
	ParentID  string
	OpenTasks int
}

// Snap is a consistent read-only snapshot of the database.
type Snap struct {
	tx    *sql.Tx
	ctx   context.Context
	today Date
	loc   *time.Location
	tags  map[string][]string // task uuid -> tag titles
}

// Close ends the read transaction.
func (s *Snap) Close() error { return s.tx.Rollback() }

// Today is the snapshot's notion of today (local time).
func (s *Snap) Today() Date { return s.today }

// Location is the local time zone used for dates.
func (s *Snap) Location() *time.Location { return s.loc }

const taskCols = `t.uuid, t.type, t.status, t.trashed, coalesce(t.title,''), coalesce(t.notes,''),
 coalesce(t.start,0), coalesce(t.startDate,0), coalesce(t.startBucket,0),
 t.reminderTime IS NOT NULL, coalesce(t.reminderTime,0), coalesce(t.deadline,0),
 coalesce(t.creationDate,0), coalesce(t.userModificationDate,0), coalesce(t.stopDate,0),
 coalesce(p.uuid,''), coalesce(p.title,''), coalesce(a.uuid,''), coalesce(a.title,''),
 coalesce(h.uuid,''), coalesce(h.title,''),
 coalesce(t.checklistItemsCount,0), coalesce(t.openChecklistItemsCount,0),
 coalesce(t.openUntrashedLeafActionsCount,0),
 t.rt1_repeatingTemplate IS NOT NULL, t.rt1_recurrenceRule IS NOT NULL,
 coalesce(t.rt1_nextInstanceStartDate,0)`

// Tasks under a heading have project NULL; their project comes via the heading.
const taskFrom = ` FROM TMTask t
 LEFT JOIN TMTask h ON h.uuid = t.heading
 LEFT JOIN TMTask p ON p.uuid = coalesce(t.project, h.project)
 LEFT JOIN TMArea a ON a.uuid = coalesce(t.area, p.area)`

// live: not trashed (itself or via parent), a to-do or project, not a
// repeating template (templates are rules, not tasks).
const live = ` t.trashed=0 AND t.type IN (0,1) AND t.rt1_recurrenceRule IS NULL
 AND (p.uuid IS NULL OR p.trashed=0) AND (h.uuid IS NULL OR h.trashed=0)`

const openInOpenParent = ` AND t.status=0 AND (p.uuid IS NULL OR p.status=0)`

// Grouping order: loose items first (NULLs sort first), then by area and project.
const groupOrder = ` a."index", p."index", h."index", t."index"`

// View names a built-in list.
type View string

const (
	ViewInbox     View = "inbox"
	ViewToday     View = "today"
	ViewUpcoming  View = "upcoming"
	ViewAnytime   View = "anytime"
	ViewSomeday   View = "someday"
	ViewLogbook   View = "logbook"
	ViewTrash     View = "trash"
	ViewDeadlines View = "deadlines"
	ViewProjects  View = "projects"
	ViewOpen      View = "open" // every open to-do/project (search base)
	ViewAll       View = "all"  // open + logbook (search --all base)
)

// Tasks returns the items of a list view in Things' order.
func (s *Snap) Tasks(v View) ([]Task, error) {
	today := int64(s.today)
	switch v {
	case ViewInbox:
		return s.queryTasks(live + openInOpenParent + ` AND t.type=0 AND t.start=0 ORDER BY t."index"`)
	case ViewToday:
		return s.queryTasks(live+openInOpenParent+
			` AND ((t.start IN (1,2) AND t.startDate>0 AND t.startDate<=?)
			   OR (t.deadline>0 AND t.deadline<=? AND t.deadlineSuppressionDate IS NULL))
			 ORDER BY t.startBucket, t.todayIndex, t."index"`, today, today)
	case ViewUpcoming:
		tasks, err := s.queryTasks(live+openInOpenParent+
			` AND t.start=2 AND t.startDate>? ORDER BY t.startDate, t.todayIndex, t."index"`, today)
		if err != nil {
			return nil, err
		}
		// Repeating templates show their next occurrence in Upcoming.
		tmpl, err := s.queryTasks(` t.trashed=0 AND t.status=0 AND t.rt1_recurrenceRule IS NOT NULL
			AND t.rt1_nextInstanceStartDate>?`, today)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, tmpl...)
		sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].StartDate < tasks[j].StartDate })
		return tasks, nil
	case ViewAnytime:
		// Items in a Someday or scheduled project are hidden from Anytime.
		return s.queryTasks(live + ` AND t.status=0 AND t.type=0 AND t.start=1
			AND (p.uuid IS NULL OR (p.status=0 AND p.start=1)) ORDER BY` + groupOrder)
	case ViewSomeday:
		return s.queryTasks(live + openInOpenParent + ` AND t.start=2 AND coalesce(t.startDate,0)=0 ORDER BY` + groupOrder)
	case ViewLogbook:
		return s.queryTasks(` t.trashed=0 AND t.type IN (0,1) AND t.status IN (2,3) ORDER BY t.stopDate DESC`)
	case ViewTrash:
		return s.queryTasks(` t.trashed=1 AND t.type IN (0,1) ORDER BY t.userModificationDate DESC`)
	case ViewDeadlines:
		return s.queryTasks(live + openInOpenParent + ` AND t.deadline>0 ORDER BY t.deadline`)
	case ViewProjects:
		return s.queryTasks(live + ` AND t.type=1 AND t.status=0 ORDER BY a."index", t."index"`)
	case ViewOpen:
		return s.queryTasks(live + openInOpenParent + ` ORDER BY` + groupOrder)
	case ViewAll:
		return s.queryTasks(live + ` ORDER BY` + groupOrder)
	}
	return nil, fmt.Errorf("unknown view %q", v)
}

// ProjectTasks returns the open to-dos of a project, ordered by heading.
func (s *Snap) ProjectTasks(projectID string) ([]Task, error) {
	return s.queryTasks(` t.trashed=0 AND t.type=0 AND t.status=0 AND t.rt1_recurrenceRule IS NULL
		AND coalesce(t.project, h.project)=? ORDER BY h."index", t."index"`, projectID)
}

// AreaItems returns the open projects and loose to-dos of an area.
func (s *Snap) AreaItems(areaID string) ([]Task, error) {
	return s.queryTasks(` t.trashed=0 AND t.type IN (0,1) AND t.status=0 AND t.rt1_recurrenceRule IS NULL
		AND t.area=? AND t.project IS NULL AND t.heading IS NULL ORDER BY t.type DESC, t."index"`, areaID)
}

// TaggedTasks returns the open items carrying a tag.
func (s *Snap) TaggedTasks(tagID string) ([]Task, error) {
	return s.queryTasks(live+openInOpenParent+
		` AND t.uuid IN (SELECT tasks FROM TMTaskTag WHERE tags=?) ORDER BY`+groupOrder, tagID)
}

func (s *Snap) queryTasks(where string, args ...any) ([]Task, error) {
	if err := s.loadTags(); err != nil {
		return nil, err
	}
	rows, err := s.tx.QueryContext(s.ctx, `SELECT `+taskCols+taskFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var evening int
		var created, modified, stopped float64
		var nextInstance Date
		if err := rows.Scan(&t.UUID, &t.Kind, &t.Status, &t.Trashed, &t.Title, &t.Notes,
			&t.Start, &t.StartDate, &evening, &t.HasReminder, &t.Reminder, &t.Deadline,
			&created, &modified, &stopped,
			&t.ProjectID, &t.ProjectTitle, &t.AreaID, &t.AreaTitle, &t.HeadingID, &t.HeadingTitle,
			&t.ChecklistTotal, &t.ChecklistOpen, &t.OpenChildren,
			&t.RepeatingInstance, &t.RepeatingTemplate, &nextInstance); err != nil {
			return nil, err
		}
		t.Evening = evening == 1
		t.Created, t.Modified, t.Stopped = epoch(created), epoch(modified), epoch(stopped)
		if t.RepeatingTemplate {
			t.StartDate = nextInstance
		}
		t.Tags = s.tags[t.UUID]
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Snap) loadTags() error {
	if s.tags != nil {
		return nil
	}
	s.tags = map[string][]string{}
	rows, err := s.tx.QueryContext(s.ctx, `SELECT tt.tasks, coalesce(g.title,'')
		FROM TMTaskTag tt JOIN TMTag g ON g.uuid = tt.tags ORDER BY g."index"`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var task, title string
		if err := rows.Scan(&task, &title); err != nil {
			return err
		}
		s.tags[task] = append(s.tags[task], title)
	}
	return rows.Err()
}

// Areas returns all areas with open-item counts.
func (s *Snap) Areas() ([]Area, error) {
	rows, err := s.tx.QueryContext(s.ctx, `SELECT a.uuid, coalesce(a.title,''),
		(SELECT count(*) FROM TMTask t WHERE t.area=a.uuid AND t.type=1 AND t.status=0 AND t.trashed=0),
		(SELECT count(*) FROM TMTask t LEFT JOIN TMTask p ON p.uuid=t.project
		  WHERE coalesce(t.area,p.area)=a.uuid AND t.type=0 AND t.status=0 AND t.trashed=0
		  AND t.rt1_recurrenceRule IS NULL)
		FROM TMArea a ORDER BY a."index"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Area
	for rows.Next() {
		var a Area
		if err := rows.Scan(&a.UUID, &a.Title, &a.OpenProjects, &a.OpenTodos); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Tags returns all tags with the number of open items carrying them.
func (s *Snap) Tags() ([]Tag, error) {
	rows, err := s.tx.QueryContext(s.ctx, `SELECT g.uuid, coalesce(g.title,''), coalesce(g.shortcut,''), coalesce(g.parent,''),
		(SELECT count(*) FROM TMTaskTag tt JOIN TMTask t ON t.uuid=tt.tasks
		  WHERE tt.tags=g.uuid AND t.status=0 AND t.trashed=0)
		FROM TMTag g ORDER BY g."index"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.UUID, &t.Title, &t.Shortcut, &t.ParentID, &t.OpenTasks); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AllIDs returns every addressable uuid (tasks, areas, tags, checklist items),
// used to compute short-id prefixes that are unique across all of them.
func (s *Snap) AllIDs() ([]string, error) {
	rows, err := s.tx.QueryContext(s.ctx, `SELECT uuid FROM TMTask UNION ALL SELECT uuid FROM TMArea
		UNION ALL SELECT uuid FROM TMTag UNION ALL SELECT uuid FROM TMChecklistItem`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ObjectKind tells what a uuid refers to.
type ObjectKind string

const (
	ObjTask      ObjectKind = "task" // to-do, project or heading
	ObjArea      ObjectKind = "area"
	ObjTag       ObjectKind = "tag"
	ObjChecklist ObjectKind = "checklist-item"
)

// ErrNoObject means the uuid does not exist.
var ErrNoObject = errors.New("no such item")

// KindOf reports which table a uuid belongs to.
func (s *Snap) KindOf(uuid string) (ObjectKind, error) {
	var k string
	err := s.tx.QueryRowContext(s.ctx, `SELECT 'task' FROM TMTask WHERE uuid=?1
		UNION ALL SELECT 'area' FROM TMArea WHERE uuid=?1
		UNION ALL SELECT 'tag' FROM TMTag WHERE uuid=?1
		UNION ALL SELECT 'checklist-item' FROM TMChecklistItem WHERE uuid=?1 LIMIT 1`, uuid).Scan(&k)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoObject
	}
	return ObjectKind(k), err
}

// Task returns one task (any kind, any state) with its checklist.
func (s *Snap) Task(uuid string) (Task, error) {
	ts, err := s.queryTasks(` t.uuid=?`, uuid)
	if err != nil {
		return Task{}, err
	}
	if len(ts) == 0 {
		return Task{}, ErrNoObject
	}
	t := ts[0]
	rows, err := s.tx.QueryContext(s.ctx, `SELECT uuid, coalesce(title,''), coalesce(status,0)
		FROM TMChecklistItem WHERE task=? ORDER BY "index"`, uuid)
	if err != nil {
		return Task{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ChecklistItem
		if err := rows.Scan(&c.UUID, &c.Title, &c.Status); err != nil {
			return Task{}, err
		}
		t.Checklist = append(t.Checklist, c)
	}
	return t, rows.Err()
}

// ChecklistOwner returns the task a checklist item belongs to.
func (s *Snap) ChecklistOwner(itemID string) (string, error) {
	var task string
	err := s.tx.QueryRowContext(s.ctx, `SELECT coalesce(task,'') FROM TMChecklistItem WHERE uuid=?`, itemID).Scan(&task)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoObject
	}
	return task, err
}
