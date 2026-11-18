package backup

import (
	"archive/tar"
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database"
)

// The database dump inside an archive.
//
// A tar entry states its size before its bytes, and a dump's size is not
// known until it is written. The first portable copy waited for the whole
// thing in a buffer; this one is written in parts of at most dumpPartSize,
// each an entry of its own, so a backup holds one part in memory however
// large the panel -- and never writes the dump to disk outside the archive,
// where a sealed backup would leave it in the clear. The parts are one stream
// again on the way back: read in order, they are the dump.

// DumpDir is where an archive keeps the dump's parts.
const DumpDir = "wui-dump"

// dumpPartSize is the most of the dump held in memory at once.
const dumpPartSize = 8 << 20

// PendingDumpFile is where a restore leaves a dump to be loaded once the
// database is open, in the data directory.
const PendingDumpFile = ".restore-dump.jsonl"

// isDumpPart reports whether an archive entry is one of the dump's parts.
func isDumpPart(name string) bool {
	return strings.HasPrefix(name, DumpDir+"/") && strings.HasSuffix(name, ".jsonl") && path.Base(name) != ""
}

// partWriter cuts what is written to it into tar entries of at most
// dumpPartSize: wui-dump/00001.jsonl, 00002.jsonl and on.
type partWriter struct {
	tw   *tar.Writer
	buf  []byte
	n    int
	when time.Time
}

func newPartWriter(tw *tar.Writer) *partWriter {
	return &partWriter{tw: tw, buf: make([]byte, 0, dumpPartSize), when: time.Now()}
}

func (p *partWriter) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		room := dumpPartSize - len(p.buf)
		take := len(b)
		if take > room {
			take = room
		}
		p.buf = append(p.buf, b[:take]...)
		b = b[take:]
		written += take
		if len(p.buf) == dumpPartSize {
			if err := p.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (p *partWriter) flush() error {
	if len(p.buf) == 0 {
		return nil
	}
	p.n++
	hdr := &tar.Header{Name: fmt.Sprintf("%s/%05d.jsonl", DumpDir, p.n), Mode: 0o600, Size: int64(len(p.buf)), ModTime: p.when, Typeflag: tar.TypeReg}
	if err := p.tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: write header for %s: %w", hdr.Name, err)
	}
	if _, err := p.tw.Write(p.buf); err != nil {
		return fmt.Errorf("backup: write %s: %w", hdr.Name, err)
	}
	p.buf = p.buf[:0]
	return nil
}

// Close writes what is left.
func (p *partWriter) Close() error { return p.flush() }

// dumpChecker follows an archive's dump parts as they are read and checks
// the dump they make -- every table's count and checksum, and its end -- in
// the same pass, without keeping any of it.
type dumpChecker struct {
	pw     *io.PipeWriter
	done   chan error
	sum    *database.DumpSummary
	seen   bool
	last   string
	once   sync.Once
	result error
}

func newDumpChecker() *dumpChecker {
	pr, pw := io.Pipe()
	c := &dumpChecker{pw: pw, done: make(chan error, 1)}
	go func() {
		sum, err := database.CheckDump(bufio.NewReaderSize(pr, 256<<10))
		c.sum = sum
		// Whatever is still being written after the check stopped is drained,
		// so the archive's reader is never left blocked on it.
		_, _ = io.Copy(io.Discard, pr)
		c.done <- err
	}()
	return c
}

// part takes one part, which must come after the last.
func (c *dumpChecker) part(name string, r io.Reader) error {
	if c.seen && name <= c.last {
		return fmt.Errorf("its database copy is out of order at %s", name)
	}
	c.seen, c.last = true, name
	_, err := io.Copy(c.pw, r)
	return err
}

// finish is the verdict on the whole dump, nil when there was none. Safe to
// call again: the verdict is kept.
func (c *dumpChecker) finish() (*database.DumpSummary, error) {
	c.once.Do(func() {
		// Cleanup on the way out: nothing is left to do if it fails, and the error that brought us here is the one reported.
		_ = c.pw.Close()
		c.result = <-c.done
	})
	if !c.seen {
		return nil, nil
	}
	return c.sum, c.result
}

// joinDumpParts writes the staged parts, in order, into one file in the data
// directory for the load at the next start, and removes them.
func joinDumpParts(staging, dataDir string, parts []string) error {
	sort.Strings(parts)
	dst := filepath.Join(dataDir, PendingDumpFile)
	out, err := os.OpenFile(dst+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	for _, rel := range parts {
		src := filepath.Join(staging, filepath.FromSlash(rel))
		in, err := os.Open(src)
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err != nil {
			out.Close()
			return err
		}
		// Cleanup on the way out: nothing is left to do if it fails, and the error that brought us here is the one reported.
		_ = os.Remove(src)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".tmp", dst)
}

// stagedDumpDriver is the engine a staged dump was taken on, from its first
// part's header.
func stagedDumpDriver(staging string, parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return dumpDriver(filepath.Join(staging, filepath.FromSlash(parts[0])))
}
