package service

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// An OpenVPN profile is read whatever its lines end with: pasted from a
// Windows editor, saved on an old Mac, or as Linux writes it.
func TestAnOpenVPNProfileIsReadWhateverItsLineEndings(t *testing.T) {
	profile := strings.Join([]string{
		"client", "dev tun", "proto udp", "remote vpn.example.com 1194", "nobind",
		"<ca>", "-----BEGIN CERTIFICATE-----", "MIIB", "-----END CERTIFICATE-----", "</ca>",
	}, "\n")
	for name, text := range map[string]string{
		"linux":   profile,
		"windows": strings.ReplaceAll(profile, "\n", "\r\n"),
		"old mac": strings.ReplaceAll(profile, "\n", "\r"),
	} {
		in, err := ParseOutbound(text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if in.Kind != model.OutboundOpenVPN || in.Address != "vpn.example.com:1194" || in.Tag == "" {
			t.Fatalf("%s: read as %s at %q", name, in.Kind, in.Address)
		}
		if strings.Contains(in.Config, "\r") {
			t.Fatalf("%s: the stored profile keeps carriage returns", name)
		}
	}
}
