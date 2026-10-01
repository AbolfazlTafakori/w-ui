package main

import (
	"strings"
	"testing"
)

func TestCompareSnapshotsBreaksOnlyOnWhatWasThere(t *testing.T) {
	before := Snapshot{
		Settings: map[string]string{"sub.port": "2096", "panel.timeLocation": "Asia/Tehran"},
		Files:    map[string]string{"/etc/wui/db.env": "aa"},
		Units:    map[string]string{"/etc/systemd/system/wui.service": "[Service]\nUser=wui\n"},
		Nft:      []string{"chain inet wui forward", "table inet wui"},
		Rules:    []string{"-4 32000: from all fwmark 0xa70001 lookup 47000"},
	}
	same := before
	if broken, _ := CompareSnapshots(before, same); len(broken) != 0 {
		t.Fatalf("the same snapshot is broken: %v", broken)
	}

	grown := Snapshot{
		Settings: map[string]string{"sub.port": "2096", "panel.timeLocation": "Asia/Tehran", "notify.backupTime": "@daily"},
		Files:    map[string]string{"/etc/wui/db.env": "aa", "/etc/wui/new.env": "bb"},
		Units:    before.Units,
		Nft:      append([]string{"chain inet wui extra"}, before.Nft...),
		Rules:    before.Rules,
	}
	broken, added := CompareSnapshots(before, grown)
	if len(broken) != 0 {
		t.Errorf("additions read as breakage: %v", broken)
	}
	if len(added) != 3 {
		t.Errorf("added = %v, want the setting, the file and the chain", added)
	}

	for name, change := range map[string]func(*Snapshot){
		"a setting changed": func(s *Snapshot) {
			s.Settings = map[string]string{"sub.port": "2097", "panel.timeLocation": "Asia/Tehran"}
		},
		"a setting gone":   func(s *Snapshot) { s.Settings = map[string]string{"sub.port": "2096"} },
		"a file rewritten": func(s *Snapshot) { s.Files = map[string]string{"/etc/wui/db.env": "cc"} },
		"a unit rewritten": func(s *Snapshot) {
			s.Units = map[string]string{"/etc/systemd/system/wui.service": "[Service]\nUser=root\n"}
		},
		"a chain renamed": func(s *Snapshot) { s.Nft = []string{"chain inet wui fwd", "table inet wui"} },
		"a rule gone":     func(s *Snapshot) { s.Rules = nil },
	} {
		after := before
		change(&after)
		broken, _ := CompareSnapshots(before, after)
		if len(broken) == 0 {
			t.Errorf("%s: not seen", name)
		}
		if name == "a unit rewritten" && !strings.Contains(strings.Join(broken, ""), "+ User=root") {
			t.Errorf("a rewritten unit does not say how: %v", broken)
		}
	}
}
