package thingsdb

import (
	"os"
	"path/filepath"
	"testing"

	"database/sql"
)

// TestOpenIsReadOnly guards the core safety property: thingsctl can never
// write to the Things database through its read handle.
func TestOpenIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.sqlite")
	rw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Exec(`CREATE TABLE x(a)`); err != nil {
		t.Fatal(err)
	}
	rw.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.sql.Exec(`INSERT INTO x VALUES(1)`); err == nil {
		t.Fatal("write through the read handle succeeded; it must fail")
	}
}

func TestLocateErrors(t *testing.T) {
	t.Setenv("THINGS_DB", "")
	t.Setenv("HOME", t.TempDir())
	if _, err := Locate(); err != ErrNotFound {
		t.Errorf("empty home: err = %v, want ErrNotFound", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	container := filepath.Join(home, ContainerRel)
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(container, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(container, 0o755)
	if _, err := Locate(); err != ErrPermission {
		t.Errorf("unreadable container: err = %v, want ErrPermission", err)
	}
}
