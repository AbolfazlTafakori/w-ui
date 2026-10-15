//go:build linux

package fwsync

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Active is the firewalls on this server that are on: ufw when its status
// says active, firewalld when it is running. None is nothing to do.
func Active() []Firewall {
	var out []Firewall
	if path, err := exec.LookPath("ufw"); err == nil {
		if o, err := run(path, "status"); err == nil && strings.Contains(o, "Status: active") {
			out = append(out, ufw{path})
		}
	}
	if path, err := exec.LookPath("firewall-cmd"); err == nil {
		if o, err := run(path, "--state"); err == nil && strings.TrimSpace(o) == "running" {
			out = append(out, firewalld{path})
		}
	}
	return out
}

func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ufw takes a port as "51820/udp" and a protocol as "proto gre from any to
// any"; a rule it adds says "Rule added", one it had says "Skipping".
type ufw struct{ path string }

func (ufw) Name() string { return "ufw" }

func (u ufw) spec(r Rule) []string {
	if r.Port == 0 {
		return []string{"proto", r.Proto, "from", "any", "to", "any"}
	}
	return []string{r.String()}
}

func (u ufw) Allow(r Rule) (bool, error) {
	args := append(append([]string{"allow"}, u.spec(r)...), "comment", "wui tunnel")
	out, err := run(u.path, args...)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "Rule added"), nil
}

func (u ufw) Has(r Rule) (bool, error) {
	out, err := run(u.path, "show", "added")
	if err != nil {
		return false, err
	}
	want := "ufw allow " + strings.Join(u.spec(r), " ")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), want) {
			return true, nil
		}
	}
	return false, nil
}

func (u ufw) Remove(r Rule) error {
	_, err := run(u.path, append([]string{"delete", "allow"}, u.spec(r)...)...)
	return err
}

// firewalld is changed in what runs and in what it starts with, never by a
// reload: a reload would drop anything else only in what runs.
type firewalld struct{ path string }

func (firewalld) Name() string { return "firewalld" }

func (f firewalld) flag(verb string, r Rule) string {
	if r.Port == 0 {
		return "--" + verb + "-protocol=" + r.Proto
	}
	return "--" + verb + "-port=" + r.String()
}

func (f firewalld) Has(r Rule) (bool, error) {
	out, _ := run(f.path, f.flag("query", r))
	return strings.TrimSpace(out) == "yes", nil
}

func (f firewalld) Allow(r Rule) (bool, error) {
	if ok, _ := f.Has(r); ok {
		return false, nil
	}
	if _, err := run(f.path, f.flag("add", r)); err != nil {
		return false, err
	}
	if _, err := run(f.path, "--permanent", f.flag("add", r)); err != nil {
		return true, err
	}
	return true, nil
}

func (f firewalld) Remove(r Rule) error {
	_, err1 := run(f.path, f.flag("remove", r))
	_, err2 := run(f.path, "--permanent", f.flag("remove", r))
	if err1 != nil {
		return err1
	}
	return err2
}
