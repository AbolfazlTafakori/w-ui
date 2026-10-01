package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/upgradecheck"
)

// Snapshot is what an install has that an update must leave as it found it:
// the settings it stored, its files under /etc/wui, its systemd units, and
// the names of its nftables tables and chains and the routing rules it
// placed. Taken before an update and again after, and compared.
type Snapshot struct {
	Settings map[string]string `json:"settings"`
	Files    map[string]string `json:"files"` // path -> sha256
	Units    map[string]string `json:"units"` // path -> content
	Nft      []string          `json:"nft"`   // "table inet wui", "chain inet wui forward", ...
	Rules    []string          `json:"rules"` // ip rule lines in the panel's range
}

// The units an install has.
var unitFiles = []string{
	"/etc/systemd/system/wui.service",
	"/etc/systemd/system/wui-update.path",
	"/etc/systemd/system/wui-update.service",
}

func snapshotCommand(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	out := fs.String("out", "", "file to write the snapshot to")
	confDir := fs.String("conf", "/etc/wui", "the install's configuration directory")
	dataDir := fs.String("data", "/var/lib/wui", "the install's data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("snapshot needs -out")
	}
	s := Snapshot{Files: map[string]string{}, Units: map[string]string{}}

	db, err := installDB(*confDir, *dataDir)
	if err != nil {
		return err
	}
	defer closeDB(db)
	if s.Settings, err = upgradecheck.StoredSettings(db); err != nil {
		return err
	}

	err = filepath.WalkDir(*confDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		s.Files[path] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return err
	}
	for _, u := range unitFiles {
		if raw, err := os.ReadFile(u); err == nil {
			s.Units[u] = string(raw)
		}
	}
	if s.Nft, err = nftNames(); err != nil {
		return err
	}
	s.Rules = panelRules()

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("snapshot: %d settings, %d files, %d units, %d nft names, %d rules\n",
		len(s.Settings), len(s.Files), len(s.Units), len(s.Nft), len(s.Rules))
	return os.WriteFile(*out, raw, 0o644)
}

// installDB opens the install's database the way the panel does: PostgreSQL
// when db.env says so, the SQLite file in the data directory otherwise.
func installDB(confDir, dataDir string) (*gorm.DB, error) {
	env := map[string]string{}
	if f, err := os.Open(filepath.Join(confDir, "db.env")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok {
				env[k] = strings.Trim(v, `"'`)
			}
		}
		f.Close()
	}
	if env["WUI_DB_DRIVER"] == "postgres" {
		return gorm.Open(postgres.Open(env["WUI_DB_SOURCE"]), quiet())
	}
	return openSQLite(filepath.Join(dataDir, "wui.db") + "?mode=ro")
}

var nftLine = regexp.MustCompile(`^\s*(table|chain|set|map) (\S+(?: \S+)?)`)

// nftNames lists the panel's tables and what is in them, by name.
func nftNames() ([]string, error) {
	out, err := exec.Command("nft", "list", "tables").Output()
	if err != nil {
		return nil, fmt.Errorf("nft list tables: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != "table" || !strings.HasPrefix(f[2], "wui") {
			continue
		}
		names = append(names, "table "+f[1]+" "+f[2])
		body, err := exec.Command("nft", "list", "table", f[1], f[2]).Output()
		if err != nil {
			return nil, err
		}
		for _, l := range strings.Split(string(body), "\n") {
			if m := nftLine.FindStringSubmatch(l); m != nil && m[1] != "table" {
				names = append(names, m[1]+" "+f[1]+" "+f[2]+" "+strings.Fields(m[2])[0])
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// panelRules are the policy routing rules in the panel's mark range.
func panelRules() []string {
	var rules []string
	for _, fam := range []string{"-4", "-6"} {
		out, err := exec.Command("ip", fam, "rule", "list").Output()
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "0xa7") || strings.Contains(l, "lookup 47") {
				rules = append(rules, fam+" "+strings.TrimSpace(l))
			}
		}
	}
	sort.Strings(rules)
	return rules
}

func compareCommand(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	before := fs.String("before", "", "snapshot taken before the update")
	after := fs.String("after", "", "snapshot taken after it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var a, b Snapshot
	for _, x := range []struct {
		path string
		into *Snapshot
	}{{*before, &a}, {*after, &b}} {
		raw, err := os.ReadFile(x.path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, x.into); err != nil {
			return fmt.Errorf("%s: %w", x.path, err)
		}
	}
	broken, added := CompareSnapshots(a, b)
	for _, s := range added {
		fmt.Println("  added:", s)
	}
	if len(broken) > 0 {
		return fmt.Errorf("the update changed what it must leave alone:\n  %s", strings.Join(broken, "\n  "))
	}
	fmt.Println("unchanged: settings, /etc/wui, systemd units, nftables names and routing rules")
	return nil
}

// CompareSnapshots lists what was removed or changed -- which breaks an
// install -- and, apart, what was added, which does not.
func CompareSnapshots(a, b Snapshot) (broken, added []string) {
	mapDiff := func(kind string, x, y map[string]string, show bool) {
		for k, v := range x {
			w, ok := y[k]
			switch {
			case !ok:
				broken = append(broken, fmt.Sprintf("%s %s is gone", kind, k))
			case v != w && show:
				broken = append(broken, fmt.Sprintf("%s %s changed:\n%s", kind, k, lineDiff(v, w)))
			case v != w:
				broken = append(broken, fmt.Sprintf("%s %s changed", kind, k))
			}
		}
		for k := range y {
			if _, ok := x[k]; !ok {
				added = append(added, kind+" "+k)
			}
		}
	}
	mapDiff("setting", a.Settings, b.Settings, false)
	mapDiff("file", a.Files, b.Files, false)
	mapDiff("unit", a.Units, b.Units, true)
	listDiff := func(kind string, x, y []string) {
		have := map[string]bool{}
		for _, s := range y {
			have[s] = true
		}
		was := map[string]bool{}
		for _, s := range x {
			was[s] = true
			if !have[s] {
				broken = append(broken, kind+" "+s+" is gone")
			}
		}
		for _, s := range y {
			if !was[s] {
				added = append(added, kind+" "+s)
			}
		}
	}
	listDiff("nft", a.Nft, b.Nft)
	listDiff("rule", a.Rules, b.Rules)
	sort.Strings(broken)
	sort.Strings(added)
	return broken, added
}

// lineDiff shows the lines one text has and the other does not.
func lineDiff(a, b string) string {
	in := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	x, y := in(a), in(b)
	var out []string
	for _, l := range strings.Split(a, "\n") {
		if !y[l] {
			out = append(out, "    - "+l)
		}
	}
	for _, l := range strings.Split(b, "\n") {
		if !x[l] {
			out = append(out, "    + "+l)
		}
	}
	return strings.Join(out, "\n")
}
