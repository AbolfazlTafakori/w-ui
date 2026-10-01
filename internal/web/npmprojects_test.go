package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The frontend and the documentation site are npm projects, each a Go module
// of its own, so "./..." from the repository root never reaches into their
// node_modules: an npm install there must not add packages to the panel's
// build, vet or tests. (One did: flatted ships Go code, and was compiled and
// tested as part of the panel on any machine that had run npm install.)
func TestTheNpmProjectsAreNotPartOfTheGoBuild(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, d := range []string{"web", "docs"} {
		if _, err := os.Stat(filepath.Join(root, d, "go.mod")); err != nil {
			t.Errorf("%s/ has no go.mod of its own, so ./... walks into its node_modules: %v", d, err)
		}
	}
	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list could not run here: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.Contains(pkg, "/web/") || strings.Contains(pkg, "/docs/") || strings.Contains(pkg, "node_modules") {
			t.Errorf("./... includes %s", pkg)
		}
	}
}
