package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Frozelo/thingsctl/internal/thingsapp"
	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// versionRunner answers the doctor's `return version` script.
type versionRunner struct {
	recordRunner
	version string
	err     error
}

func (r *versionRunner) AppleScript(_ context.Context, script string, _ []string) (string, error) {
	if !strings.Contains(script, "return version") {
		panic("doctor must only ask for the version")
	}
	return r.version, r.err
}

func doctorRun(t *testing.T, running bool, r thingsapp.Runner) (string, string, int) {
	t.Helper()
	ThingsRunning = func() bool { return running }
	t.Cleanup(func() { ThingsRunning = thingsapp.Running })
	Runner = r
	return run(t, "doctor")
}

func TestDoctorHealthy(t *testing.T) {
	out, _, code := doctorRun(t, true, &versionRunner{version: "3.24.1"})
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	wantLines(t, out,
		"ok   database: ",
		"(version 29)\n",
		"ok   schema: compatible\n",
		"warn things urls: off.",
		"ok   automation: allowed (Things 3.24.1)\n",
	)
}

func TestDoctorNeverLaunchesThings(t *testing.T) {
	out, _, code := doctorRun(t, false, &recordRunner{})
	if code != 0 {
		t.Fatalf("a skipped check must not fail: exit %d", code)
	}
	wantLines(t, out, "skip automation: Things is not running")
}

func TestDoctorFailures(t *testing.T) {
	out, errOut, code := doctorRun(t, true, &versionRunner{err: thingsapp.ErrAutomationDenied})
	if code != 1 || !strings.Contains(errOut, "some checks failed") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	wantLines(t, out, "FAIL automation: macOS denied control of Things.", "Automation → Things3")

	t.Setenv("__CFBundleIdentifier", "com.example.Term")
	ThingsRunning = func() bool { return false }
	OpenDB = func() (*thingsdb.DB, error) { return nil, thingsdb.ErrPermission }
	var o, e strings.Builder
	code = Main([]string{"doctor"}, &o, &e)
	if code != 1 {
		t.Errorf("permission: exit %d", code)
	}
	wantLines(t, o.String(), "FAIL database: macOS denied access. Grant Full Disk Access to com.example.Term")
}
