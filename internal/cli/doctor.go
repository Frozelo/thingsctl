package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Frozelo/thingsctl/internal/thingsapp"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// ThingsRunning reports whether the Things app is running; tests override it.
var ThingsRunning = thingsapp.Running

const doctorUsage = `thingsctl doctor — check that thingsctl can read and control Things
Checks the database, its schema, macOS permissions and Things settings.
Never launches Things and never changes anything. Exit code 1 if a check fails.
`

type doctor struct {
	w      io.Writer
	failed bool
}

func (d *doctor) report(status, name, format string, a ...any) {
	if status == "FAIL" {
		d.failed = true
	}
	fmt.Fprintf(d.w, "%-4s %s: %s\n", status, name, fmt.Sprintf(format, a...))
}

// hostApp names the app macOS attributes thingsctl's permissions to, when known.
func hostApp() string {
	if id := os.Getenv("__CFBundleIdentifier"); id != "" {
		return id
	}
	return "the app that runs thingsctl"
}

func runDoctor(args []string, w io.Writer) error {
	if len(args) > 0 {
		if args[0] == "-h" || args[0] == "--help" {
			fmt.Fprint(w, doctorUsage)
			return nil
		}
		return fmt.Errorf("unexpected argument %q (see: thingsctl doctor -h)", args[0])
	}
	d := &doctor{w: w}
	fmt.Fprintf(w, "thingsctl %s %s/%s\n", Version, runtime.GOOS, runtime.GOARCH)
	d.database()
	d.app()
	if os.Getenv("THINGSCTL_READONLY") == "1" {
		d.report("info", "read-only", "THINGSCTL_READONLY=1, writes are disabled")
	}
	if d.failed {
		return errors.New("some checks failed")
	}
	return nil
}

func (d *doctor) database() {
	db, err := OpenDB()
	if err != nil {
		if errors.Is(err, thingsdb.ErrPermission) {
			d.report("FAIL", "database", "macOS denied access. Grant Full Disk Access to %s in System Settings → Privacy & Security → Full Disk Access, then restart it", hostApp())
		} else {
			d.report("FAIL", "database", "%v", err)
		}
		return
	}
	defer db.Close()
	db.Now = Now
	snap, err := db.Snap(context.Background())
	if err != nil {
		d.report("FAIL", "database", "%v", err)
		return
	}
	defer snap.Close()
	version, err := snap.Version()
	if err != nil {
		d.report("FAIL", "database", "%s: cannot read version: %v", tildePath(db.Path), err)
		return
	}
	d.report("ok", "database", "%s (version %d)", tildePath(db.Path), version)

	missing, err := snap.MissingColumns()
	switch {
	case err != nil:
		d.report("FAIL", "schema", "%v", err)
	case len(missing) > 0:
		slices.Sort(missing)
		d.report("FAIL", "schema", "missing %s; this Things version is not supported yet", strings.Join(missing, ", "))
	case !slices.Contains(thingsdb.KnownVersions, version):
		d.report("warn", "schema", "database version %d is untested (tested: %v); reads may be incomplete", version, thingsdb.KnownVersions)
	default:
		d.report("ok", "schema", "compatible")
	}

	tok, err := snap.Token()
	switch {
	case err != nil:
		d.report("warn", "things urls", "cannot read the setting: %v", err)
	case tok == "":
		d.report("warn", "things urls", "off. Only needed to change evening, reminders, headings or checklists of existing items: Things → Settings → General → Enable Things URLs")
	default:
		d.report("ok", "things urls", "on")
	}
}

func (d *doctor) app() {
	if !ThingsRunning() {
		d.report("skip", "automation", "Things is not running; open it and run doctor again to check that thingsctl may control it")
		return
	}
	var s thingsapp.Script
	s.Line("return version")
	version, err := Runner.AppleScript(context.Background(), s.Source(), s.Args())
	switch {
	case errors.Is(err, thingsapp.ErrAutomationDenied):
		d.report("FAIL", "automation", "macOS denied control of Things. Allow it for %s in System Settings → Privacy & Security → Automation → Things3", hostApp())
	case err != nil:
		d.report("FAIL", "automation", "%v", err)
	default:
		d.report("ok", "automation", "allowed (Things %s)", version)
	}
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}
