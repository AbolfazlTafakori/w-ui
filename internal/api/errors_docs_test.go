package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The errors reference quotes messages exactly. This reads every quoted
// message on that page and looks for it in the source, so a reworded
// message fails the build until the page is updated with it -- and a page
// that documents a message nobody emits fails too.
//
// "…" in the docs stands for a value; the text is checked around it.

func TestEveryDocumentedErrorExistsInTheSource(t *testing.T) {
	root := filepath.Join("..", "..")
	page, err := os.ReadFile(filepath.Join(root, "docs", "reference", "errors.md"))
	if err != nil {
		t.Skip("docs not checked out")
	}

	// Everything an error message could come from, in one haystack.
	var hay strings.Builder
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == "node_modules" || name == ".git" || name == "dist" || name == "docs" || name == "cache" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".sh") {
			b, err := os.ReadFile(path)
			if err == nil {
				hay.Write(b)
				hay.WriteByte('\n')
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	src := hay.String()
	// Go and shell escape a quote inside a string differently from how the
	// docs show it; compare with both forms of quoting collapsed.
	src = strings.NewReplacer(`\"`, `"`, `%q`, `"…"`, `%d`, `…`, `%s`, `…`, `%v`, `…`, `%w`, `…`, `\\`, `\`).Replace(src)
	src = shellVars.ReplaceAllString(src, "…")
	// A message the source builds from two adjacent literals reads as one.
	src = concat.ReplaceAllString(src, "")
	// The panel capitalises the first letter on the way out; compare without case.
	src = strings.ToLower(src)

	// Only rows of the tables: the first cell, which may hold several
	// messages separated by " / ".
	row := regexp.MustCompile("(?m)^\\| (`[^|]+`) \\|")
	code := regexp.MustCompile("`([^`]+)`")
	missing := 0
	seen := 0
	for _, m := range row.FindAllStringSubmatch(string(page), -1) {
		for _, c := range code.FindAllStringSubmatch(m[1], -1) {
			msg := strings.TrimSpace(c[1])
			msg = strings.TrimSuffix(msg, " (log)")
			seen++
			if !documented(src, msg) {
				missing++
				t.Errorf("documented but not in the source: %q", msg)
			}
		}
	}
	if seen < 150 {
		t.Errorf("only %d messages read from the page; the table format changed?", seen)
	}
	t.Logf("%d messages checked, %d missing", seen, missing)
}

var concat = regexp.MustCompile(`"\s*\+\s*"`)

var shellVars = regexp.MustCompile(`\$\{[^}]+\}|\$\([^)]*\)|\$[A-Za-z_][A-Za-z_0-9]*`)

// documented reports whether every fixed part of a message, split at the
// placeholders, appears in the source.
func documented(src, msg string) bool {
	parts := strings.Split(msg, "…")
	found := 0
	for _, p := range parts {
		p = strings.Trim(p, ` "'.:;,()`)
		if len(p) < 6 {
			continue
		}
		found++
		if !strings.Contains(src, strings.ToLower(p)) {
			return false
		}
	}
	return found > 0
}
