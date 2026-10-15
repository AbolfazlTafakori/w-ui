package service

import (
	"encoding/base64"
	"regexp"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestXUIOwns(t *testing.T) {
	for email, want := range map[string]bool{
		"wui-12": true, "wui-1": true,
		"wui-": false, "wui-1a": false, "wtest-1": false, "user@example.com": false, "WUI-3": false,
	} {
		if got := XUIOwns(email); got != want {
			t.Errorf("XUIOwns(%q) = %v, want %v", email, got, want)
		}
	}
	acc := model.Account{ID: 42}
	if !XUIOwns(XUIEmail(&acc)) {
		t.Fatal("an email this panel makes is not recognised as its own")
	}
}

func TestXUISecretShapes(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for _, kind := range []string{"vless", "vmess"} {
		s, err := xuiSecret(model.XUIInbound{Kind: kind})
		if err != nil || !uuid.MatchString(s) {
			t.Fatalf("%s: %q, %v", kind, s, err)
		}
	}
	for method, size := range map[string]int{
		"2022-blake3-aes-128-gcm": 16, "2022-blake3-aes-256-gcm": 32, "2022-blake3-chacha20-poly1305": 32,
	} {
		s, err := xuiSecret(model.XUIInbound{Kind: "shadowsocks", Method: method})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(raw) != size {
			t.Fatalf("%s: key %q is %d bytes, want %d", method, s, len(raw), size)
		}
	}
	for _, kind := range []string{"trojan", "hysteria"} {
		s, err := xuiSecret(model.XUIInbound{Kind: kind})
		if err != nil || len(s) != 32 {
			t.Fatalf("%s: %q, %v", kind, s, err)
		}
	}
}

func TestXUIName(t *testing.T) {
	taken := map[string]bool{"wg0": true, "vless": true}
	if got := xuiName(XUIInboundSeen{XUIInbound: model.XUIInbound{Remark: "vless"}}, taken); got != "vless-2" {
		t.Fatalf("a taken name: got %q", got)
	}
	if got := xuiName(XUIInboundSeen{XUIInbound: model.XUIInbound{Kind: "trojan"}, Port: 443}, taken); got != "trojan-443" {
		t.Fatalf("no remark or tag: got %q", got)
	}
	long := "an inbound with a remark far longer than thirty-two characters"
	if got := xuiName(XUIInboundSeen{XUIInbound: model.XUIInbound{Remark: long}}, taken); len([]rune(got)) > 32 {
		t.Fatalf("too long: %q", got)
	}
	if got := xuiName(XUIInboundSeen{XUIInbound: model.XUIInbound{Remark: "سرور آلمان 🇩🇪"}}, taken); got != "سرور آلمان 🇩🇪" {
		t.Fatalf("a Persian remark: got %q", got)
	}
}
