//go:build linux

package ovpndriver

import (
	"os"
	"testing"
)

// The read-only check never restarts a server on a guess.
//
// It is asked about a process the panel did not start and a directory that may
// not be there, and the only answer that costs a customer their connection is
// "read-only". Everything else -- a directory that is writable, one that is
// missing, a pid that has gone -- has to come back false, or a panel restart
// would tear down a server that was working.
func TestReadOnlyCheckOnlyAnswersForAReadOnlyFilesystem(t *testing.T) {
	dir := t.TempDir()
	self := os.Getpid()

	if dirIsReadOnlyFor(self, dir) {
		t.Fatal("a writable directory was reported read-only")
	}
	if dirIsReadOnlyFor(self, dir+"/not-there") {
		t.Fatal("a missing directory was reported read-only")
	}
	// /proc/<pid> for a pid that is not running: the probe cannot be made at
	// all, which is not the same as a read-only filesystem.
	if dirIsReadOnlyFor(1<<22, dir) {
		t.Fatal("a pid that is gone was reported read-only")
	}

	// And it leaves nothing behind in a directory it found writable.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the probe was left on disk: %v", entries)
	}
}
