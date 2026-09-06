//go:build linux

package ovpndriver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/ovpnconf"
)

// The status file is read instead of the management socket, and the reason is
// worth guarding: every management connection writes three lines to OpenVPN's
// log at verb 3, and the collector runs every two seconds.
func TestSessionsAreReadFromTheFileOpenVPNAlreadyWrites(t *testing.T) {
	dir := t.TempDir()
	l := ovpnconf.NewLayout(dir, "ovpntest")
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		t.Fatal(err)
	}

	const report = `TITLE,OpenVPN 2.7.0
TIME,2026-09-06 13:38:22,1788701902
HEADER,CLIENT_LIST,Common Name,Real Address,Virtual Address,Virtual IPv6 Address,Bytes Received,Bytes Sent,Connected Since,Connected Since (time_t),Username,Client ID,Peer ID,Data Channel Cipher
CLIENT_LIST,ali,203.0.113.9:51820,10.88.0.6,,4096,8192,2026-09-06 13:00:00,1788700000,ali,0,0,AES-256-GCM
END
`
	if err := os.WriteFile(l.StatusFile(), []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}

	d := New()
	got, err := d.sessionReport(context.Background(), l)
	if err != nil {
		t.Fatalf("sessionReport: %v", err)
	}
	sessions := parseStatus(got)
	if len(sessions) != 1 || sessions[0].Username != "ali" {
		t.Fatalf("the file was not read into a session: %+v", sessions)
	}
	if sessions[0].RX != 4096 || sessions[0].TX != 8192 {
		t.Errorf("counters came back as %d/%d", sessions[0].RX, sessions[0].TX)
	}
}

// OpenVPN truncates and rewrites the file on its timer, so a read can land
// halfway through one. A half-written report has no END, and answering with
// what it holds would report every customer as disconnected for a tick.
func TestAHalfWrittenStatusFileIsNotTreatedAsTheAnswer(t *testing.T) {
	dir := t.TempDir()
	l := ovpnconf.NewLayout(dir, "ovpntest")
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The first two lines of a report, as a truncate-and-rewrite would leave it.
	if err := os.WriteFile(l.StatusFile(), []byte("TITLE,OpenVPN 2.7.0\nTIME,x,1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := New()
	// There is no management socket here, so falling back must be what fails —
	// which is the proof that the partial file was refused.
	_, err := d.sessionReport(context.Background(), l)
	if err == nil {
		t.Fatal("a half-written report was accepted as the answer")
	}
	if !strings.Contains(err.Error(), "management socket") {
		t.Errorf("expected a fall back to the socket, got %v", err)
	}
}

// And a server that has only just started has written no file at all.
func TestAMissingStatusFileFallsBackRatherThanReportingNobody(t *testing.T) {
	dir := t.TempDir()
	l := ovpnconf.NewLayout(dir, "ovpntest")
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.Dir, "status")); err == nil {
		t.Fatal("the fixture already has a status file")
	}

	d := New()
	if _, err := d.sessionReport(context.Background(), l); err == nil {
		t.Fatal("a missing report was accepted as the answer")
	}
}
