package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The documentation's shape: every page in English has its Persian twin and
// the other way round, every page can be reached from its sidebar, and the
// pages about how the project is run name only files that exist -- a
// checklist pointing at a test that was renamed is a checklist nobody can
// follow.
func TestTheDocsAreWholeInBothLanguages(t *testing.T) {
	root := filepath.Join("..", "..", "docs")
	config, err := os.ReadFile(filepath.Join(root, ".vitepress", "config.mjs"))
	if err != nil {
		t.Skip("docs not checked out")
	}
	pages := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".vitepress" || d.Name() == "public") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			rel, _ := filepath.Rel(root, path)
			pages[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for p := range pages {
		if strings.HasPrefix(p, "fa/") {
			if !pages[strings.TrimPrefix(p, "fa/")] {
				t.Errorf("docs/%s has no English page", p)
			}
		} else if !pages["fa/"+p] {
			t.Errorf("docs/%s has no Persian page (docs/fa/%s)", p, p)
		}
		// Each page is in its sidebar; an index is reached from the nav.
		if strings.HasSuffix(p, "index.md") {
			continue
		}
		link := "'/" + strings.TrimSuffix(p, ".md") + "'"
		if !strings.Contains(string(config), link) {
			t.Errorf("docs/%s is in no sidebar (no link %s in config.mjs)", p, link)
		}
	}

	// The paths the development pages name, in backticks, exist.
	repo := filepath.Join("..", "..")
	path := regexp.MustCompile("`((?:internal|cmd|scripts|tools|\\.github|docs)/[^`\\s]+)`")
	for _, page := range []string{"development", "fa/development"} {
		files, _ := filepath.Glob(filepath.Join(root, page, "*.md"))
		if len(files) == 0 {
			t.Errorf("docs/%s has no pages", page)
		}
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range path.FindAllStringSubmatch(string(raw), -1) {
				if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(m[1]))); err != nil {
					t.Errorf("%s names %s, which does not exist", filepath.ToSlash(f), m[1])
				}
			}
		}
	}
}
