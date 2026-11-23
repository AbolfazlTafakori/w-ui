//go:build linux

package ovpndriver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

	// Written just now, as OpenVPN writes it every ten seconds.
	report := `TITLE,OpenVPN 2.7.0
TIME,now,` + strconv.FormatInt(time.Now().Unix(), 10) + `
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

// A status file nobody writes any longer -- replaced under a running daemon,
// which then writes to the deleted one -- is whole and ends in END, and would
// say for ever that nobody is connected. Seen on a test server: six days of
// no OpenVPN customer online and no limit on one file used from two places.
// Past its age the socket is asked, and what the daemon says now is the answer.
func TestAStatusFileNobodyWritesAnyLongerIsNotTheAnswer(t *testing.T) {
	dir := t.TempDir()
	l := ovpnconf.NewLayout(dir, "ovpntest")
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := "TITLE,OpenVPN 2.6.19\nTIME,then," + strconv.FormatInt(time.Now().Add(-6*24*time.Hour).Unix(), 10) +
		"\nHEADER,CLIENT_LIST,Common Name\nEND\n"
	if err := os.WriteFile(l.StatusFile(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", l.Management())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		if _, err := c.Read(buf); err != nil {
			return
		}
		// The daemon's own answer, as its management interface gives it.
		_, _ = c.Write([]byte("TITLE,OpenVPN 2.6.19\nTIME,now," + strconv.FormatInt(time.Now().Unix(), 10) +
			"\nCLIENT_LIST,s7-phone,203.0.113.9:41984,10.72.0.13,,1383,3123,now," + strconv.FormatInt(time.Now().Unix(), 10) +
			",s7-phone,34,0,AES-256-GCM\nEND\n"))
	}()

	got, err := New().sessionReport(context.Background(), l)
	if err != nil {
		t.Fatalf("sessionReport: %v", err)
	}
	if s := parseStatus(got); len(s) != 1 || s[0].Username != "s7-phone" {
		t.Fatalf("the stale file was taken over the daemon: %+v", s)
	}
}
