package notify

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is when something recurring happens: one of the cron shorthands,
// an interval, or a crontab line with or without a seconds field -- the same
// forms 3x-ui's bot takes for its report time.
type Schedule struct {
	every  time.Duration
	fields [6]fieldSet // second minute hour dom month dow
}

type fieldSet struct {
	any bool
	set map[int]bool
}

func (f fieldSet) has(v int) bool { return f.any || f.set[v] }

// ParseSchedule reads a schedule. It refuses anything it cannot run, so a
// mistyped line is caught on save and not at three in the morning.
func ParseSchedule(s string) (Schedule, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "@hourly":
		return parseCron("0 0 * * * *")
	case "@daily", "@midnight":
		return parseCron("0 0 0 * * *")
	case "@weekly":
		return parseCron("0 0 0 * * 0")
	case "@monthly":
		return parseCron("0 0 0 1 * *")
	case "@yearly", "@annually":
		return parseCron("0 0 0 1 1 *")
	}
	if strings.HasPrefix(s, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(s, "@every ")))
		if err != nil || d < time.Second {
			return Schedule{}, fmt.Errorf("%q is not an interval like 30m or 6h", s)
		}
		return Schedule{every: d}, nil
	}
	return parseCron(s)
}

func parseCron(s string) (Schedule, error) {
	parts := strings.Fields(s)
	switch len(parts) {
	case 5:
		parts = append([]string{"0"}, parts...)
	case 6:
	default:
		return Schedule{}, fmt.Errorf("a crontab line has 5 or 6 fields, %q has %d", s, len(parts))
	}
	limits := [6][2]int{{0, 59}, {0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	var sc Schedule
	for i, p := range parts {
		f, err := parseField(p, limits[i][0], limits[i][1])
		if err != nil {
			return Schedule{}, err
		}
		sc.fields[i] = f
	}
	return sc, nil
}

func parseField(p string, lo, hi int) (fieldSet, error) {
	if p == "*" {
		return fieldSet{any: true}, nil
	}
	out := fieldSet{set: map[int]bool{}}
	for _, part := range strings.Split(p, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 {
				return fieldSet{}, fmt.Errorf("bad step in %q", p)
			}
			step, part = n, part[:i]
		}
		a, b := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			r := strings.SplitN(part, "-", 2)
			x, err1 := strconv.Atoi(r[0])
			y, err2 := strconv.Atoi(r[1])
			if err1 != nil || err2 != nil || x < lo || y > hi || x > y {
				return fieldSet{}, fmt.Errorf("bad range in %q", p)
			}
			a, b = x, y
		default:
			x, err := strconv.Atoi(part)
			if err != nil || x < lo || x > hi {
				return fieldSet{}, fmt.Errorf("%q is out of range in %q", part, p)
			}
			a, b = x, x
			if step > 1 {
				b = hi
			}
		}
		for v := a; v <= b; v += step {
			out.set[v%(hi+1)] = true
		}
	}
	return out, nil
}

// Next is the first time after now the schedule fires.
func (sc Schedule) Next(now time.Time) time.Time {
	if sc.every > 0 {
		return now.Add(sc.every)
	}
	f := sc.fields
	matchMinute := func(t time.Time) bool {
		return f[4].has(int(t.Month())) && f[3].has(t.Day()) && f[5].has(int(t.Weekday())) &&
			f[2].has(t.Hour()) && f[1].has(t.Minute())
	}
	// The rest of this minute first, then minute by minute for up to a year.
	base := now.Truncate(time.Minute)
	if matchMinute(base) {
		for sec := now.Second() + 1; sec < 60; sec++ {
			if f[0].has(sec) {
				return base.Add(time.Duration(sec) * time.Second)
			}
		}
	}
	t := base.Add(time.Minute)
	end := t.AddDate(1, 0, 0)
	for ; t.Before(end); t = t.Add(time.Minute) {
		if !matchMinute(t) {
			continue
		}
		for sec := 0; sec < 60; sec++ {
			if f[0].has(sec) {
				return t.Add(time.Duration(sec) * time.Second)
			}
		}
	}
	return end
}
