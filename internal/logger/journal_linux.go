//go:build linux

package logger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Reading the log the system kept, not the one this process is holding.
//
// The ring lives in memory, which means it is empty in the one situation an
// operator most wants it: just after a restart. A panel that crashed, was
// upgraded, or was killed by the kernel has nothing to show for the minutes
// before, and those are the minutes in question.
//
// journalctl has them. This asks for them in JSON so the same structure the
// panel shows for its own buffer comes back — a time, a level and a message —
// rather than a wall of text the page would have to guess at.

const (
	// journalTimeout bounds the call. A busy journal on a small server can take
	// a moment; a page waiting on it forever is worse than a page that says it
	// could not read it.
	journalTimeout = 15 * time.Second

	// journalUnit is the service the installer creates.
	journalUnit = "wui.service"
)

// Journal reads recent entries for this panel's unit from the system journal.
//
// n is bounded by the caller. Nothing else here is built from anything a
// request supplied: the arguments are fixed and the count is an integer, so
// there is no string a caller could steer into the command.
func Journal(ctx context.Context, n int, minLevel, query string) ([]Entry, error) {
	if n <= 0 || n > 5000 {
		n = 1000
	}

	ctx, cancel := context.WithTimeout(ctx, journalTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "journalctl",
		"-u", journalUnit,
		"-n", strconv.Itoa(n),
		"-o", "json",
		"--no-pager",
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		// Named plainly. "exit status 1" sends an operator looking through
		// their own logs for a problem with reading logs.
		if strings.Contains(msg, "executable file not found") {
			return nil, fmt.Errorf("this server has no journalctl, so only the panel's own buffer is available")
		}
		return nil, fmt.Errorf("could not read the system journal: %s", msg)
	}

	// journalctl exits zero when it has no permission to read anything, saying
	// so only on stderr. Taking that as success returns an empty list, which
	// reads as "nothing happened" rather than "this could not be read" — and
	// the panel runs as its own user, which by default is exactly the case.
	if msg := strings.TrimSpace(errBuf.String()); strings.Contains(msg, "insufficient permissions") {
		return nil, fmt.Errorf(
			"this panel's user cannot read the system journal. To allow it, run: " +
				"usermod -aG systemd-journal wui && systemctl restart wui — " +
				"note that this also lets the panel read other services' logs on this machine")
	}

	return parseJournal(out.Bytes(), minLevel, query), nil
}

// parseJournal turns journalctl's JSON lines into entries, newest first.
//
// Kept separate from the command so the shape of what journald emits can be
// tested without a journal to read.
func parseJournal(raw []byte, minLevel, query string) []Entry {
	threshold := levelRank(minLevel)
	needle := strings.ToLower(strings.TrimSpace(query))

	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(raw))
	// A single line can be long: a stack trace arrives as one message.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		var row struct {
			Message   string `json:"MESSAGE"`
			Priority  string `json:"PRIORITY"`
			Timestamp string `json:"__REALTIME_TIMESTAMP"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			continue
		}
		if row.Message == "" {
			continue
		}

		e := journalEntry(row.Message, row.Priority, row.Timestamp)
		if levelRank(e.Level) < threshold {
			continue
		}
		if needle != "" && !matches(e, needle) {
			continue
		}
		out = append(out, e)
	}

	// journalctl prints oldest first; everything above expects the newest at
	// the top, the way the ring returns it.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// journalEntry builds one entry, preferring the panel's own structured line
// over what journald wrapped it in.
func journalEntry(message, priority, stamp string) Entry {
	e := Entry{
		Time:    journalTime(stamp),
		Level:   priorityLevel(priority),
		Message: strings.TrimSpace(message),
	}

	// The panel writes "time=... level=INFO msg=... key=value". When that is
	// what the journal is holding, the parts are pulled back out so a journal
	// line looks the same on the page as a live one, instead of one long
	// string with the timestamp printed twice.
	if t, level, msg, fields, ok := parseSlogText(e.Message); ok {
		if !t.IsZero() {
			e.Time = t
		}
		e.Level = level
		e.Message = msg
		e.Fields = fields
	}
	return e
}

// journalTime reads journald's microseconds-since-the-epoch stamp.
func journalTime(stamp string) time.Time {
	usec, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(usec).UTC()
}

// ScanJournal reads this panel's lines from the journal back to since, oldest
// first, and hands keep each one the panel itself wrote whose message is one
// of msgs. It streams: a year of a busy panel's log is far more than should
// be held in memory to find a handful of lines in it.
func ScanJournal(ctx context.Context, since time.Time, msgs []string, keep func(Entry)) error {
	want := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		want[m] = true
	}
	cmd := exec.CommandContext(ctx, "journalctl",
		"-u", journalUnit,
		"--since", since.UTC().Format("2006-01-02 15:04:05")+" UTC",
		"-o", "json",
		"--output-fields", "MESSAGE,PRIORITY",
		"--no-pager",
	)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			return fmt.Errorf("this server has no journalctl")
		}
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		// Most lines are not wanted; a cheap look first saves decoding them.
		hit := false
		for m := range want {
			if bytes.Contains(line, []byte(m)) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		var row struct {
			Message   string `json:"MESSAGE"`
			Priority  string `json:"PRIORITY"`
			Timestamp string `json:"__REALTIME_TIMESTAMP"`
		}
		if json.Unmarshal(line, &row) != nil || row.Message == "" {
			continue
		}
		e := journalEntry(row.Message, row.Priority, row.Timestamp)
		if want[e.Message] && e.Fields != nil {
			keep(e)
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()
	msg := strings.TrimSpace(errBuf.String())
	if strings.Contains(msg, "insufficient permissions") {
		return ErrJournalDenied
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("could not read the system journal: %s", msg)
	}
	return nil
}
