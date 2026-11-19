// Package thingsapp talks to the running Things app: AppleScript through
// osascript, and the things:/// URL scheme through `open -g`. User-provided
// strings are never spliced into AppleScript source; they travel as argv.
package thingsapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// BundleID identifies Things 3 for AppleScript and LaunchServices.
const BundleID = "com.culturedcode.ThingsMac"

// Runner executes AppleScript and opens URLs. Tests use a fake.
type Runner interface {
	// AppleScript runs a script whose `on run argv` receives args.
	AppleScript(ctx context.Context, script string, args []string) (string, error)
	// OpenURL opens a URL; background keeps Things from taking focus.
	OpenURL(ctx context.Context, u string, background bool) error
	// EnsureRunning launches Things in the background if needed.
	EnsureRunning(ctx context.Context) error
}

// ErrAutomationDenied means macOS blocked Apple Events to Things (-1743).
var ErrAutomationDenied = errors.New("not allowed to control Things: allow it for the app running thingsctl in System Settings → Privacy & Security → Automation → Things3")

// ErrNotFound means Things could not find the referenced item (-1728).
var ErrNotFound = errors.New("Things could not find that item")

// System is the real Runner.
type System struct{}

var reErrNum = regexp.MustCompile(`\((-?\d+)\)\s*$`)

// AppleScript runs osascript with the script on stdin.
func (System) AppleScript(ctx context.Context, script string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "osascript", append([]string{"-"}, args...)...)
	cmd.Stdin = strings.NewReader(script)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		switch m := reErrNum.FindStringSubmatch(msg); {
		case m != nil && m[1] == "-1743":
			return "", ErrAutomationDenied
		case m != nil && m[1] == "-1728":
			return "", ErrNotFound
		case msg != "":
			return "", fmt.Errorf("applescript: %s", msg)
		}
		return "", fmt.Errorf("applescript: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}

// OpenURL opens a things:/// URL via LaunchServices.
func (System) OpenURL(ctx context.Context, u string, background bool) error {
	args := []string{u}
	if background {
		args = []string{"-g", u}
	}
	if out, err := exec.CommandContext(ctx, "open", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("open url: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// EnsureRunning starts Things hidden so AppleScript does not bring it forward.
func (System) EnsureRunning(ctx context.Context) error {
	if running() {
		return nil
	}
	if err := exec.CommandContext(ctx, "open", "-g", "-b", BundleID).Run(); err != nil {
		return fmt.Errorf("cannot launch Things: %w", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if running() {
			time.Sleep(700 * time.Millisecond) // let it open its database
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("Things did not start within 8s")
}

func running() bool { return exec.Command("pgrep", "-x", "Things3").Run() == nil }

// Script builds an AppleScript run handler. Values are passed as argv items.
type Script struct {
	lines []string
	args  []string
}

// Arg registers a value and returns an AppleScript expression for it.
func (s *Script) Arg(v string) string {
	s.args = append(s.args, v)
	return fmt.Sprintf("(item %d of argv)", len(s.args))
}

// Line appends a statement (format args must be expressions from Arg or numbers).
func (s *Script) Line(format string, a ...any) { s.lines = append(s.lines, fmt.Sprintf(format, a...)) }

// Date appends statements that set variable name to local midnight of y-m-d,
// independent of the system date format.
func (s *Script) Date(name string, y, m, d int) {
	s.Line("set %s to current date", name)
	s.Line("set time of %s to 0", name)
	s.Line("set day of %s to 1", name)
	s.Line("set year of %s to %d", name, y)
	s.Line("set month of %s to %d", name, m)
	s.Line("set day of %s to %d", name, d)
}

// Source returns the full script.
func (s *Script) Source() string {
	var b strings.Builder
	b.WriteString("on run argv\n")
	fmt.Fprintf(&b, "tell application id %q\n", BundleID)
	for _, l := range s.lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("end tell\nend run\n")
	return b.String()
}

// Args returns the argv values.
func (s *Script) Args() []string { return s.args }

// URL builds a things:/// command URL with parameters in insertion order.
type URL struct {
	Command string
	params  [][2]string
}

// Set adds a parameter (empty values are kept: they clear fields in Things).
func (u *URL) Set(k, v string) { u.params = append(u.params, [2]string{k, v}) }

// String encodes the URL; Things wants %20 rather than '+'.
func (u *URL) String() string { return u.encode(false) }

// Redacted encodes the URL with the auth token hidden.
func (u *URL) Redacted() string { return u.encode(true) }

func (u *URL) encode(redact bool) string {
	var b strings.Builder
	b.WriteString("things:///" + u.Command)
	for i, p := range u.params {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		v := p[1]
		if redact && p[0] == "auth-token" {
			v = "***"
		}
		b.WriteString(p[0] + "=" + strings.ReplaceAll(url.QueryEscape(v), "+", "%20"))
	}
	return b.String()
}
