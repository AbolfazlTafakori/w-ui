package logger

import (
	"strings"
	"time"
)

// Reading back a line this panel wrote.
//
// The panel logs to standard output in slog's text form, and the journal keeps
// exactly those bytes:
//
//	time=2026-09-05T05:19:20.688Z level=WARN msg="request failed" ip=1.2.3.4
//
// Handed to the page as one string, that renders as a wall with the timestamp
// printed twice and no level to colour. Pulling it apart is what makes a line
// read from the journal look like a line read from the ring — same columns,
// same badge, same searchable fields — instead of a different thing that
// happens to appear in the same list.
//
// Deliberately not in the Linux-only file that calls it: this is string
// handling and needs no journal to test.

// parseSlogText splits a line the panel wrote. ok is false for anything that is
// not one — a line from the Go runtime, a panic, a message from systemd itself
// — and those are kept whole rather than mangled into columns they do not have.
func parseSlogText(line string) (t time.Time, level, msg string, fields map[string]any, ok bool) {
	pairs := splitPairs(line)
	if len(pairs) == 0 {
		return time.Time{}, "", "", nil, false
	}

	fields = map[string]any{}
	for _, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			return time.Time{}, "", "", nil, false
		}
		key, value := p[:eq], unquote(p[eq+1:])

		switch key {
		case "time":
			// Several layouts, because the format follows the handler's
			// configuration rather than anything fixed.
			for _, layout := range []string{
				time.RFC3339Nano, time.RFC3339,
				"2006-01-02T15:04:05.000Z07:00",
				"2006/01/02 15:04:05",
			} {
				if parsed, err := time.Parse(layout, value); err == nil {
					t = parsed.UTC()
					break
				}
			}
		case "level":
			level = strings.ToUpper(value)
		case "msg":
			msg = value
		default:
			fields[key] = value
		}
	}

	// A line with no level and no message is not one of ours, whatever else it
	// had in it.
	if level == "" && msg == "" {
		return time.Time{}, "", "", nil, false
	}
	if len(fields) == 0 {
		fields = nil
	}
	return t, level, msg, fields, true
}

// splitPairs breaks a line on spaces, keeping quoted values whole.
//
// msg="could not reach it" is one pair, not three, and getting that wrong turns
// every message with a space in it into a row of nonsense keys.
func splitPairs(line string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	escaped := false

	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			cur.WriteRune(r)
			escaped = true
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// unquote removes the quotes slog adds around a value that needs them, and puts
// back the escapes it introduced.
func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
		v = strings.ReplaceAll(v, `\"`, `"`)
		v = strings.ReplaceAll(v, `\\`, `\`)
		v = strings.ReplaceAll(v, `\n`, "\n")
	}
	return v
}

// priorityLevel maps syslog severities onto the panel's four levels.
func priorityLevel(priority string) string {
	switch priority {
	case "0", "1", "2", "3":
		return "ERROR"
	case "4":
		return "WARN"
	case "7":
		return "DEBUG"
	default:
		return "INFO"
	}
}
