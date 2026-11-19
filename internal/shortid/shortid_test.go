package shortid

import (
	"errors"
	"testing"
)

func TestLenAndShort(t *testing.T) {
	x := New([]string{"abcdef1", "abcdzz2", "q1"})
	if x.Len() != 5 {
		t.Fatalf("Len = %d, want 5 (abcd shared)", x.Len())
	}
	if got := x.Short("abcdef1"); got != "abcde" {
		t.Errorf("Short = %q", got)
	}
	if got := x.Short("q1"); got != "q1" {
		t.Errorf("Short(short id) = %q", got)
	}
	if New([]string{"a1", "b2"}).Len() != MinLen {
		t.Error("Len must not go below MinLen")
	}
}

func TestResolve(t *testing.T) {
	x := New([]string{"abcdef1", "abcdzz2", "Abcdqq3"})
	if id, err := x.Resolve("abcde"); err != nil || id != "abcdef1" {
		t.Errorf("unique prefix: %q, %v", id, err)
	}
	if id, err := x.Resolve("abcdzz2"); err != nil || id != "abcdzz2" {
		t.Errorf("full id: %q, %v", id, err)
	}
	if id, err := x.Resolve("Abcd"); err != nil || id != "Abcdqq3" {
		t.Errorf("case-sensitive prefix: %q, %v", id, err)
	}
	var amb *AmbiguousError
	if _, err := x.Resolve("abcd"); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Errorf("ambiguous: %v", err)
	}
	var nf *NotFoundError
	if _, err := x.Resolve("zzz"); !errors.As(err, &nf) {
		t.Errorf("not found: %v", err)
	}
}
