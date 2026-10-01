package web

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
)

// The bundle the panel serves is the one any machine builds from a clean
// checkout: its text files end their lines with LF and nothing else. A
// bundle built from a checkout whose files had turned CRLF carried the
// carriage returns into what every panel serves -- and differed from what a
// clean clone builds, so the committed bundle could not be checked against
// its sources. .gitattributes keeps the sources and the bundle LF.
func TestTheBundleIsLF(t *testing.T) {
	if !Built() {
		t.Skip("the frontend is not built")
	}
	checked := 0
	err := fs.WalkDir(dist, "dist", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".css") {
			return nil
		}
		raw, err := fs.ReadFile(dist, path)
		if err != nil {
			return err
		}
		if i := bytes.IndexByte(raw, '\r'); i >= 0 {
			t.Errorf("%s has a carriage return at byte %d", path, i)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 3 {
		t.Fatalf("only %d text files in the bundle; this test is not reading it", checked)
	}
}
