package service

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestCheckWGAdvancedNormalisesAndValidates(t *testing.T) {
	t.Run("blank stays blank", func(t *testing.T) {
		p := &model.WGAdvancedParams{}
		if err := checkWGAdvanced(p); err != nil {
			t.Fatalf("empty params rejected: %v", err)
		}
		if *p != (model.WGAdvancedParams{}) {
			t.Errorf("empty params changed: %+v", *p)
		}
	})

	t.Run("allowedIPs is parsed and rejoined", func(t *testing.T) {
		p := &model.WGAdvancedParams{AllowedIPs: " 10.0.0.0/8 ,192.168.0.0/16 "}
		if err := checkWGAdvanced(p); err != nil {
			t.Fatalf("valid CIDRs rejected: %v", err)
		}
		if p.AllowedIPs != "10.0.0.0/8, 192.168.0.0/16" {
			t.Errorf("AllowedIPs = %q, want %q", p.AllowedIPs, "10.0.0.0/8, 192.168.0.0/16")
		}
	})

	t.Run("a bad CIDR is refused", func(t *testing.T) {
		if err := checkWGAdvanced(&model.WGAdvancedParams{AllowedIPs: "not-a-cidr"}); err == nil {
			t.Error("a non-CIDR AllowedIPs was accepted")
		}
	})

	t.Run("client MTU keeps its range", func(t *testing.T) {
		if err := checkWGAdvanced(&model.WGAdvancedParams{MTU: 100}); err == nil {
			t.Error("an out-of-range client MTU was accepted")
		}
		if err := checkWGAdvanced(&model.WGAdvancedParams{MTU: 1280}); err != nil {
			t.Errorf("a valid client MTU was rejected: %v", err)
		}
	})

	t.Run("keepalive keeps its range", func(t *testing.T) {
		if err := checkWGAdvanced(&model.WGAdvancedParams{Keepalive: 70000}); err == nil {
			t.Error("an out-of-range keepalive was accepted")
		}
	})

	t.Run("hooks are trimmed and a null byte is refused", func(t *testing.T) {
		p := &model.WGAdvancedParams{PostUp: "  iptables -A FORWARD -i %i -j ACCEPT  \n"}
		if err := checkWGAdvanced(p); err != nil {
			t.Fatalf("a valid hook was rejected: %v", err)
		}
		if p.PostUp != "iptables -A FORWARD -i %i -j ACCEPT" {
			t.Errorf("PostUp not trimmed: %q", p.PostUp)
		}
		if err := checkWGAdvanced(&model.WGAdvancedParams{PostDown: "echo \x00 bad"}); err == nil {
			t.Error("a hook with a null byte was accepted")
		}
		if err := checkWGAdvanced(&model.WGAdvancedParams{PostUp: strings.Repeat("x", 4001)}); err == nil {
			t.Error("an over-long hook was accepted")
		}
	})
}
