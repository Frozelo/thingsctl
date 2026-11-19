// Package shortid shortens Things uuids to the shortest prefix length that is
// unique across all ids (like git abbreviated hashes). Ids are case-sensitive.
package shortid

import (
	"fmt"
	"sort"
	"strings"
)

// MinLen is the minimum prefix length, so ids stay stable as the database grows.
const MinLen = 4

// Index resolves and abbreviates ids.
type Index struct {
	sorted []string
	n      int
}

// New builds an index over all ids.
func New(ids []string) *Index {
	s := append([]string(nil), ids...)
	sort.Strings(s)
	n := MinLen
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			continue
		}
		if l := commonPrefix(s[i-1], s[i]) + 1; l > n {
			n = l
		}
	}
	return &Index{sorted: s, n: n}
}

func commonPrefix(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

// Len is the prefix length used for display.
func (x *Index) Len() int { return x.n }

// Short abbreviates an id.
func (x *Index) Short(id string) string {
	if len(id) <= x.n {
		return id
	}
	return id[:x.n]
}

// AmbiguousError lists the ids a prefix matches.
type AmbiguousError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("id %q is ambiguous: %s", e.Prefix, strings.Join(e.Candidates, ", "))
}

// NotFoundError means no id has the prefix.
type NotFoundError struct{ Prefix string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("no item with id %q", e.Prefix) }

// Resolve expands a prefix (or full id) to exactly one id. It never guesses:
// an ambiguous prefix returns *AmbiguousError with up to 5 candidates.
func (x *Index) Resolve(prefix string) (string, error) {
	if prefix == "" {
		return "", &NotFoundError{Prefix: prefix}
	}
	i := sort.SearchStrings(x.sorted, prefix)
	var matches []string
	for j := i; j < len(x.sorted) && strings.HasPrefix(x.sorted[j], prefix); j++ {
		if x.sorted[j] == prefix {
			return prefix, nil
		}
		if len(matches) == 0 || matches[len(matches)-1] != x.sorted[j] {
			matches = append(matches, x.sorted[j])
		}
	}
	switch len(matches) {
	case 0:
		return "", &NotFoundError{Prefix: prefix}
	case 1:
		return matches[0], nil
	}
	c := make([]string, 0, 5)
	for _, m := range matches {
		if len(c) == 5 {
			break
		}
		c = append(c, x.Short(m))
	}
	return "", &AmbiguousError{Prefix: prefix, Candidates: c}
}
