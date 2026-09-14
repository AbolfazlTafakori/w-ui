// Package geoip turns a country code into the address ranges registered to
// that country, so a routing list can say "geoip:ir" the way the classic panel's can.
//
// The kernel matches addresses, not countries, so a country is a set of
// prefixes -- a few hundred to a few thousand -- fetched once, kept on disk,
// and refreshed weekly. The source is the per-country CIDR lists published
// from the regional registries' delegation files; nothing is inferred here.
package geoip

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Source is where a country's list comes from; the two are fetched
// separately because the lists are published that way.
var (
	sourceV4 = "https://raw.githubusercontent.com/ipverse/country-ip-blocks/master/country/%s/ipv4-aggregated.txt"
	sourceV6 = "https://raw.githubusercontent.com/ipverse/country-ip-blocks/master/country/%s/ipv6-aggregated.txt"
)

// refreshAfter is how old a list may be before it is fetched again.
const refreshAfter = 7 * 24 * time.Hour

var codeRe = regexp.MustCompile(`^[a-z]{2}$`)

// Store keeps the lists.
type Store struct {
	dir  string
	log  *slog.Logger
	http *http.Client

	mu    sync.RWMutex
	cache map[string][]netip.Prefix
}

// New makes a store keeping its files under dir.
func New(dir string, log *slog.Logger) *Store {
	return &Store{
		dir:   dir,
		log:   log,
		http:  &http.Client{Timeout: 60 * time.Second},
		cache: map[string][]netip.Prefix{},
	}
}

// IsCode reports whether s is a country code this store would look up.
func IsCode(s string) bool { return codeRe.MatchString(strings.ToLower(s)) }

// Prefixes returns a country's ranges: from memory, else from disk, else
// fetched. A country that cannot be fetched and has never been fetched is
// an error, so a list naming it is refused rather than quietly empty.
func (s *Store) Prefixes(cc string) ([]netip.Prefix, error) {
	cc = strings.ToLower(strings.TrimSpace(cc))
	if !IsCode(cc) {
		return nil, fmt.Errorf("geoip: %q is not a country code", cc)
	}
	s.mu.RLock()
	got, ok := s.cache[cc]
	s.mu.RUnlock()
	if ok {
		return got, nil
	}
	if ps, err := s.readFile(cc); err == nil {
		s.put(cc, ps)
		return ps, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ps, err := s.fetch(ctx, cc)
	if err != nil {
		return nil, err
	}
	s.put(cc, ps)
	return ps, nil
}

func (s *Store) put(cc string, ps []netip.Prefix) {
	s.mu.Lock()
	s.cache[cc] = ps
	s.mu.Unlock()
}

func (s *Store) path(cc string) string { return filepath.Join(s.dir, cc+".txt") }

func (s *Store) readFile(cc string) ([]netip.Prefix, error) {
	f, err := os.Open(s.path(cc))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parse(f)
}

// fetch downloads both families and writes the merged file.
func (s *Store) fetch(ctx context.Context, cc string) ([]netip.Prefix, error) {
	var all []netip.Prefix
	for i, src := range []string{sourceV4, sourceV6} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(src, cc), nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("geoip: fetch %s: %w", cc, err)
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			if i == 0 {
				return nil, fmt.Errorf("geoip: no list is published for %q", cc)
			}
			continue // no IPv6 allocations, which small countries have
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("geoip: fetch %s: %s", cc, resp.Status)
		}
		ps, err := parse(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, ps...)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("geoip: the list for %q is empty", cc)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, p := range all {
		b.WriteString(p.String())
		b.WriteByte('\n')
	}
	tmp := s.path(cc) + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, s.path(cc)); err != nil {
		return nil, err
	}
	s.log.Info("geoip list fetched", "country", cc, "prefixes", len(all))
	return all, nil
}

func parse(r io.Reader) ([]netip.Prefix, error) {
	var out []netip.Prefix
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := netip.ParsePrefix(line)
		if err != nil {
			continue
		}
		out = append(out, p.Masked())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// Refresh re-fetches every list on disk older than a week.
func (s *Store) Refresh(ctx context.Context) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		cc := strings.TrimSuffix(e.Name(), ".txt")
		if !IsCode(cc) || e.Name() == cc {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < refreshAfter {
			continue
		}
		if ps, err := s.fetch(ctx, cc); err == nil {
			s.put(cc, ps)
		} else if !errors.Is(err, context.Canceled) {
			s.log.Warn("geoip list could not be refreshed; keeping the old one", "country", cc, "err", err)
		}
	}
}

// Run refreshes daily until ctx ends.
func (s *Store) Run(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	s.Refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Refresh(ctx)
		}
	}
}
