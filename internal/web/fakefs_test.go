package web

import (
	"io/fs"
	"testing/fstest"
)

// fakeFS is a tiny map-backed filesystem for the index rewriting tests, so
// they do not depend on whatever the last npm build produced.
type fakeFS map[string]string

func (f fakeFS) Open(name string) (fs.File, error) {
	m := fstest.MapFS{}
	for k, v := range f {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m.Open(name)
}
