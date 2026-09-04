package ovpnconf

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// TCP on 443 is what gets through a network that inspects traffic and drops
// what it does not recognise: to anything watching, the connection is a machine
// talking HTTPS to a web server. Real commercial profiles use it for exactly
// that reason, and both halves of ours have to agree about which it is — a
// server on UDP and a customer's file saying tcp is a tunnel that never comes
// up, with nothing in either log to say why.
func TestTransportReachesBothHalvesOfTheConfiguration(t *testing.T) {
	for _, want := range []string{"udp", "tcp"} {
		t.Run(want, func(t *testing.T) {
			iface := &model.Interface{
				Name: "ovpn0", Protocol: model.ProtocolOpenVPN,
				ListenPort: 443, EndpointHost: "vpn.example.com",
				Subnet: "10.9.0.0/24",
				OpenVPN: model.JSON(model.OpenVPNParams{
					Transport:   want,
					CipherSuite: "AES-256-GCM",
					Auth:        "SHA256",
					CACert:      "-----BEGIN CERTIFICATE-----\nAA\n-----END CERTIFICATE-----",
					TLSCryptKey: "-----BEGIN OpenVPN Static key V1-----\nff\n-----END OpenVPN Static key V1-----",
				}),
			}
			acc := &model.Account{Username: "ali", IP: "10.9.0.2"}

			client := RenderClient(acc, iface)
			if !strings.Contains(client, "proto "+want+"\n") {
				t.Errorf("the customer's file does not say proto %s:\n%s", want, client)
			}
			other := map[string]string{"udp": "tcp", "tcp": "udp"}[want]
			if strings.Contains(client, "proto "+other+"\n") {
				t.Errorf("the customer's file also says proto %s", other)
			}
		})
	}
}

// The default stays UDP. TCP costs throughput — every lost packet is retried by
// two stacks at once — so it is the answer when UDP is blocked, not before.
func TestTransportDefaultsToUDP(t *testing.T) {
	p, err := NewPKI("ovpn0")
	if err != nil {
		t.Fatalf("NewPKI: %v", err)
	}
	if p.Transport != "udp" {
		t.Errorf("a new interface starts on %q, want udp", p.Transport)
	}
}

// A profile is only usable if it carries everything a client needs to connect
// without being handed anything else. These are the lines real commercial
// profiles all have, checked here so none of them can be dropped quietly.
func TestClientProfileIsSelfContained(t *testing.T) {
	iface := &model.Interface{
		Name: "ovpn0", Protocol: model.ProtocolOpenVPN,
		ListenPort: 1194, EndpointHost: "vpn.example.com", Subnet: "10.9.0.0/24",
		OpenVPN: model.JSON(model.OpenVPNParams{
			Transport: "udp", CipherSuite: "AES-256-GCM", Auth: "SHA256",
			CACert:      "-----BEGIN CERTIFICATE-----\nAA\n-----END CERTIFICATE-----",
			TLSCryptKey: "-----BEGIN OpenVPN Static key V1-----\nff\n-----END OpenVPN Static key V1-----",
		}),
	}
	got := RenderClient(&model.Account{Username: "ali", IP: "10.9.0.2"}, iface)

	for _, want := range []string{
		"client",
		"dev tun",
		"remote vpn.example.com 1194",
		"nobind",
		"persist-key",
		"persist-tun",
		// Without this a client accepts any certificate the authority issued,
		// including another customer's, as if it were the server.
		"remote-cert-tls server",
		"auth-user-pass",
		"<ca>",
		// Not in the commercial samples, and the reason ours is harder to
		// block: it wraps the handshake so the port does not announce itself
		// as OpenVPN to anything watching.
		"<tls-crypt>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the profile is missing %q:\n%s", want, got)
		}
	}

	// Named twice on purpose: a 2.4 client does not know data-ciphers and would
	// negotiate nothing at all from a file that only had it.
	if !strings.Contains(got, "data-ciphers AES-256-GCM") || !strings.Contains(got, "cipher AES-256-GCM") {
		t.Errorf("the cipher must be named for both old and current clients:\n%s", got)
	}

	// The customer's own key material must never be in a file handed to them
	// as a server secret.
	if strings.Contains(got, "PRIVATE KEY") {
		t.Error("a private key reached the customer's profile")
	}
}
