// Package listfield is how the panel stores a list in one field -- tags,
// domains, addresses -- and reads one an operator typed or pasted.
package listfield

import "strings"

// Split reads a stored or submitted list. Commas, spaces and newlines all
// separate, because an operator pasting a column of addresses should not
// have to reformat it first. Each entry once, in the order given.
func Split(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(f))
	seen := map[string]bool{}
	for _, v := range f {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// Join stores a list: trimmed, each entry once, comma-separated.
func Join(v []string) string {
	clean := make([]string, 0, len(v))
	seen := map[string]bool{}
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			clean = append(clean, s)
		}
	}
	return strings.Join(clean, ",")
}
