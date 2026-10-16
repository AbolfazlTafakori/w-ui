package nodes

import (
	"encoding/base64"
	"testing"
)

func TestOnlyNamesAPanelMadeAreLeftOutOfAnImport(t *testing.T) {
	for name, made := range map[string]bool{
		"wui-12":           true,
		"wui-a47ec8-12":    true,
		"wui_a47ec8_190":   true,
		"wuikeep_30296bfa": true,
		"wui_test1":        false,
		"wui-john":         false,
		"alice":            false,
		"wui-":             false,
	} {
		if got := madeByAPanel(name); got != made {
			t.Errorf("%q: made by a panel %v, want %v", name, got, made)
		}
	}
}

func TestALinkIsFoundByTheCredentialItCarries(t *testing.T) {
	id := "3f1c2b8e-0d4a-4e6b-9c1d-7a2e5f8b9c0d"
	// A vmess link is base64 JSON, which may have a '/' in it.
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"a/b??","add":"1.2.3.4","port":"443","id":"`+id+`","path":"/x/y"}`))
	ss := "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:"+"s3cr3t/p+w")) + "@1.2.3.4:8388#me"
	for _, c := range []struct {
		link, cred string
		want       bool
	}{
		{"vless://" + id + "@1.2.3.4:443?type=tcp#a", id, true},
		{vmess, id, true},
		{vmess, "3f1c2b8e-0000-4e6b-9c1d-7a2e5f8b9c0d", false},
		{ss, "s3cr3t/p+w", true},
		{"trojan://pass%2Fword@1.2.3.4:443#t", "pass/word", true},
		{"vless://" + id + "@1.2.3.4:443", "", false},
	} {
		if got := linkCarries(c.link, c.cred); got != c.want {
			t.Errorf("%s carries %q: %v, want %v", c.link, c.cred, got, c.want)
		}
	}
}
