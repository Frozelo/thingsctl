// Package thingsdb reads the local Things 3 database. It only ever opens the
// database read-only; all writes go through Things itself (AppleScript or the
// URL scheme) so that Things Cloud sync stays intact.
package thingsdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// ContainerRel is the Things group container, relative to the home directory.
const ContainerRel = "Library/Group Containers/JLMPQHK86H.com.culturedcode.ThingsMac"

// KnownVersions lists Meta.databaseVersion values thingsctl was tested with.
var KnownVersions = []int{29}

// ErrPermission means macOS refused access to the Things container (TCC).
var ErrPermission = errors.New("permission denied reading the Things database: grant Full Disk Access to the app that runs thingsctl (Terminal, Claude, Cursor…) in System Settings → Privacy & Security, then restart it")

// ErrNotFound means no Things database exists for this user.
var ErrNotFound = errors.New("Things database not found: is Things 3 installed and opened at least once? (or set THINGS_DB)")

// Locate returns the path of the Things database. THINGS_DB overrides it.
func Locate() (string, error) {
	if p := os.Getenv("THINGS_DB"); p != "" {
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("THINGS_DB=%s: no such file", p)
			}
			return "", classify(err)
		}
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ContainerRel)
	// os.ReadDir (unlike filepath.Glob) surfaces EPERM from a TCC denial.
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", classify(err)
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "ThingsData-") {
			continue
		}
		p := filepath.Join(root, e.Name(), "Things Database.thingsdatabase", "main.sqlite")
		if _, err := os.Stat(p); err != nil {
			return "", classify(err)
		}
		return p, nil
	}
	return "", ErrNotFound
}

func classify(err error) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return ErrPermission
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	}
	return err
}

// DB is a read-only handle to the Things database.
type DB struct {
	sql  *sql.DB
	Path string
	// Now returns the current time; today's date is derived from it.
	Now func() time.Time
}

// Open opens the database read-only. Never immutable=1: that would ignore the
// WAL and return stale data while Things is running.
func Open(path string) (*DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	dsn := u.String() + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(200)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, classifyOpen(err)
	}
	return &DB{sql: db, Path: path, Now: time.Now}, nil
}

func classifyOpen(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "authorization denied") || strings.Contains(msg, "operation not permitted") {
		return ErrPermission
	}
	return err
}

// OpenDefault locates and opens the database.
func OpenDefault() (*DB, error) {
	p, err := Locate()
	if err != nil {
		return nil, err
	}
	return Open(p)
}

// Close releases the handle.
func (d *DB) Close() error { return d.sql.Close() }

// Today returns today's date in local time.
func (d *DB) Today() Date { return DateOf(d.Now()) }

// Snap begins a read transaction so that all queries of one request see one
// consistent snapshot. It must be closed promptly: an open read transaction
// blocks Things' WAL checkpoints.
func (d *DB) Snap(ctx context.Context) (*Snap, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, classifyOpen(err)
	}
	return &Snap{tx: tx, ctx: ctx, today: d.Today(), loc: d.Now().Location()}, nil
}

var reInteger = regexp.MustCompile(`<integer>(\d+)</integer>`)

// Version returns Meta.databaseVersion (stored as an XML plist).
func (s *Snap) Version() (int, error) {
	var v string
	if err := s.tx.QueryRowContext(s.ctx, `SELECT value FROM Meta WHERE key='databaseVersion'`).Scan(&v); err != nil {
		return 0, err
	}
	m := reInteger.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("unparsed databaseVersion")
	}
	return strconv.Atoi(m[1])
}

var requiredColumns = map[string][]string{
	"TMTask": {"uuid", "type", "status", "trashed", "title", "notes", "start", "startDate",
		"startBucket", "reminderTime", "deadline", "deadlineSuppressionDate", "index", "todayIndex",
		"creationDate", "userModificationDate", "stopDate", "area", "project", "heading",
		"checklistItemsCount", "openChecklistItemsCount", "openUntrashedLeafActionsCount",
		"rt1_repeatingTemplate", "rt1_recurrenceRule", "rt1_nextInstanceStartDate"},
	"TMArea":          {"uuid", "title", "index"},
	"TMTag":           {"uuid", "title", "parent", "index"},
	"TMTaskTag":       {"tasks", "tags"},
	"TMChecklistItem": {"uuid", "title", "status", "index", "task"},
	"TMSettings":      {"uriSchemeAuthenticationToken"},
}

// MissingColumns reports required columns absent from the schema, as
// "table.column". An empty result means the schema is compatible.
func (s *Snap) MissingColumns() ([]string, error) {
	var missing []string
	for table, cols := range requiredColumns {
		rows, err := s.tx.QueryContext(s.ctx, `SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			return nil, err
		}
		have := map[string]bool{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return nil, err
			}
			have[n] = true
		}
		rows.Close()
		for _, c := range cols {
			if !have[c] {
				missing = append(missing, table+"."+c)
			}
		}
	}
	return missing, nil
}
