//go:build linux

package shaper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// cmdTimeout bounds every tc invocation. A hung tc must not stall the
// reconciler, which would stop collection for every other client too.
const cmdTimeout = 15 * time.Second

// TC programs rate limits through the tc command.
type TC struct {
	log *slog.Logger

	mu sync.Mutex
	// rooted records the devices whose hierarchy has been laid down, so the
	// root qdisc is written once rather than on every reconcile tick.
	rooted map[string]bool
	// probe caches whether this kernel can shape at all. It cannot change while
	// the panel runs, and answering it costs a device create and delete.
	probe  error
	probed bool
}

// New builds a shaper for this platform.
func New(log *slog.Logger) *TC {
	if log == nil {
		log = slog.Default()
	}
	return &TC{log: log, rooted: map[string]bool{}}
}

// Health reports whether a rate limit would actually be applied.
//
// The presence of the tc binary is not the question. A kernel built without the
// classful schedulers accepts the command and silently does nothing, so the
// panel would show a speed limit that no packet ever meets. This puts a real
// hierarchy on a throwaway device and sees whether the kernel takes it.
func (s *TC) Health(ctx context.Context) error {
	if _, err := exec.LookPath("tc"); err != nil {
		return ErrNoTool
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("%w: ip is not installed (install iproute2)", ErrUnavailable)
	}

	s.mu.Lock()
	cached, probed := s.probe, s.probed
	s.mu.Unlock()
	if probed {
		return cached
	}

	err := s.probeHTB(ctx)
	s.mu.Lock()
	s.probe, s.probed = err, true
	s.mu.Unlock()
	return err
}

// probeHTB builds the hierarchy on a scratch device and throws it away.
const probeDevice = "wui-htbprobe"

func (s *TC) probeHTB(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	// Left over from a previous run that was killed mid-probe.
	_ = exec.CommandContext(ctx, "ip", "link", "del", probeDevice).Run()

	if err := exec.CommandContext(ctx, "ip", "link", "add", probeDevice, "type", "dummy").Run(); err != nil {
		// Without a scratch device the question cannot be answered. Reporting
		// unavailable would be a guess; shaping may well work.
		s.log.Debug("could not create a probe device; assuming shaping works", "error", err)
		return nil
	}
	defer func() {
		_ = exec.Command("ip", "link", "del", probeDevice).Run()
	}()

	out, err := exec.CommandContext(ctx, "tc", "qdisc", "replace", "dev", probeDevice,
		"root", "handle", "1:", "htb", "default", "ffff").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: this kernel has no HTB scheduler, so speed limits "+
			"are recorded but never applied (%s)",
			ErrUnavailable, strings.TrimSpace(string(out)))
	}
	return nil
}

// Apply makes every device carry exactly the given limits.
//
// Each device is handled independently: one device failing — a tunnel that has
// just gone away, most often — must not stop the others from being corrected.
func (s *TC) Apply(ctx context.Context, devices []string, clients []Client) error {
	if err := s.Health(ctx); err != nil {
		return err
	}

	want, problems := toDesired(clients)
	for _, p := range problems {
		// A client that cannot be shaped is reported and skipped. Failing the
		// whole batch would leave every other customer unshaped over one bad
		// row.
		s.log.Warn("skipping a client that cannot be shaped", "error", p)
	}

	var failures []string
	for _, dev := range devices {
		if err := s.applyDevice(ctx, dev, want); err != nil {
			s.log.Warn("could not shape a device", "device", dev, "error", err)
			failures = append(failures, dev)
		}
	}
	if len(failures) == len(devices) && len(devices) > 0 {
		return fmt.Errorf("shaper: no device could be shaped (%s)", strings.Join(failures, ", "))
	}
	return nil
}

func (s *TC) applyDevice(ctx context.Context, device string, want desired) error {
	if err := s.ensureRoot(ctx, device); err != nil {
		return err
	}

	have, err := s.currentClasses(ctx, device)
	if err != nil {
		return err
	}

	d := computeDiff(want, have)
	if d.empty() {
		// Nothing changed. Not touching the device is the point of the diff:
		// rewriting the hierarchy every tick would reset every customer's
		// queue and shred their throughput.
		return nil
	}

	script := BuildScript(device, want, d)
	if err := s.batch(ctx, script); err != nil {
		return err
	}

	s.log.Info("shaping updated", "device", device,
		"added", len(d.add), "changed", len(d.change), "removed", len(d.remove))
	return nil
}

// ensureRoot lays down the hierarchy the first time a device is seen, and again
// if something outside the panel has removed it.
func (s *TC) ensureRoot(ctx context.Context, device string) error {
	s.mu.Lock()
	done := s.rooted[device]
	s.mu.Unlock()

	if done {
		// Cheap check that it is still there. Someone running `tc qdisc del`
		// by hand, or the device being recreated, would otherwise leave every
		// customer silently unshaped until the panel restarted.
		if ok, _ := s.hasRoot(ctx, device); ok {
			return nil
		}
		s.log.Warn("the shaping hierarchy disappeared; rebuilding", "device", device)
		s.mu.Lock()
		delete(s.rooted, device)
		s.mu.Unlock()
	}

	if err := s.batch(ctx, RootScript(device)); err != nil {
		return err
	}
	s.mu.Lock()
	s.rooted[device] = true
	s.mu.Unlock()
	return nil
}

func (s *TC) hasRoot(ctx context.Context, device string) (bool, error) {
	out, err := s.run(ctx, "qdisc", "show", "dev", device)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, fmt.Sprintf("htb %d:", major)), nil
}

// currentClasses reads back the rates the device is actually enforcing.
//
// Reading the kernel rather than trusting a remembered value is what makes the
// shaper self-healing: a hierarchy edited by hand is put back on the next tick.
func (s *TC) currentClasses(ctx context.Context, device string) (map[uint16]uint64, error) {
	out, err := s.run(ctx, "-j", "class", "show", "dev", device)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		Handle string `json:"handle"`
		Kind   string `json:"kind"`
		Rate   any    `json:"rate"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("shaper: reading classes on %s: %w", device, err)
	}

	have := make(map[uint16]uint64, len(raw))
	for _, c := range raw {
		if c.Kind != "htb" {
			continue
		}
		minor, ok := parseMinor(c.Handle)
		if !ok {
			continue
		}
		rate, ok := parseRate(c.Rate)
		if !ok {
			continue
		}
		have[minor] = rate
	}
	return have, nil
}

// batch feeds a script to tc in one go.
func (s *TC) batch(ctx context.Context, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	// -force keeps the batch going past a line that was already true, which is
	// what makes re-running safe.
	cmd := exec.CommandContext(ctx, "tc", "-force", "-batch", "-")
	cmd.Stdin = strings.NewReader(script)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shaper: tc batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *TC) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tc", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("shaper: tc %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Close leaves the hierarchy in place. Removing it would hand every customer an
// unlimited link at the moment the panel stopped.
func (s *TC) Close() error { return nil }
