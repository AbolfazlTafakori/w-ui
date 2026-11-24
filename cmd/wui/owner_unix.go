//go:build unix

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ownAsDataDir gives paths -- a directory with everything under it -- to the
// owner of the data directory, where this command runs as root and the
// panel as its own user. The management script runs as root: a restore it
// staged, an archive it wrote, a database log it opened, were root's, and
// the panel could not read them -- a staged restore was discarded at the
// next start as incomplete, an archive could not be downloaded.
func ownAsDataDir(dataDir string, paths ...string) {
	if os.Geteuid() != 0 {
		return
	}
	st, err := os.Stat(dataDir)
	if err != nil {
		return
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid == 0 {
		return
	}
	uid, gid := int(sys.Uid), int(sys.Gid)
	for _, p := range paths {
		// Whatever can be reached: a path that is not there was not made.
		_ = filepath.WalkDir(p, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			_ = os.Lchown(path, uid, gid)
			return nil
		})
	}
}
