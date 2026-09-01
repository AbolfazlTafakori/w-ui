package ovpnconf

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func testIface(t *testing.T) *model.Interface {
	t.Helper()
	params, err := NewPKI("vpn0")
	if err != nil {
		t.Fatalf("pki: %v", err)
	}
	return &model.Interface{
		Name: "vpn0", Subnet: "10.67.0.0/16", ListenPort: 1194,
		EndpointHost: "vpn.example.com", MTU: 1500, DNS: "1.1.1.1, 8.8.8.8",
		NATInterface: "eth0", Protocol: model.ProtocolOpenVPN,
		OpenVPN: model.JSON(params),
	}
}

func testLayout() Layout { return NewLayout("/var/lib/wui", "vpn0") }

func TestNetworkConvertsCIDRToNetmask(t *testing.T) {
	cases := []struct{ subnet, network, netmask string }{
		{"10.67.0.0/16", "10.67.0.0", "255.255.0.0"},
		{"10.8.0.0/24", "10.8.0.0", "255.255.255.0"},
		{"172.16.0.0/12", "172.16.0.0", "255.240.0.0"},
		{"10.0.0.4/30", "10.0.0.4", "255.255.255.252"},
	}
	for _, c := range cases {
		network, netmask, err := Network(c.subnet)
		if err != nil {
			t.Errorf("%s: %v", c.subnet, err)
			continue
		}
		if network != c.network || netmask != c.netmask {
			t.Errorf("%s -> %s %s, want %s %s", c.subnet, network, netmask, c.network, c.netmask)
		}
	}
}

func TestNetworkRejectsWhatOpenVPNCannotUse(t *testing.T) {
	for _, bad := range []string{"not-a-subnet", "10.0.0.1/31", "10.0.0.1/32", "fd00::/64"} {
		if _, _, err := Network(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestClientAsksForAUsernameAndPassword(t *testing.T) {
	got := RenderClient(&model.Account{DeviceName: "Laptop"}, testIface(t))

	// Without this line the client offers an empty certificate instead of
	// prompting, and every login fails.
	if !strings.Contains(got, "auth-user-pass\n") {
		t.Error("the client will not prompt for credentials")
	}
	// Without this, any certificate the authority signed is accepted as the
	// server's.
	if !strings.Contains(got, "remote-cert-tls server") {
		t.Error("the client does not verify that it reached a server")
	}
	if !strings.Contains(got, "remote vpn.example.com 1194") {
		t.Error("the client has no endpoint to dial")
	}
}

func TestClientCarriesTheKeysItNeedsAndNoOthers(t *testing.T) {
	iface := testIface(t)
	got := RenderClient(&model.Account{DeviceName: "Laptop"}, iface)

	if !strings.Contains(got, "<ca>") || !strings.Contains(got, "<tls-crypt>") {
		t.Error("the client is missing the CA or the tls-crypt key")
	}
	// The server's private key would let whoever holds it impersonate the
	// server to every other customer.
	if strings.Contains(got, iface.OpenVPN.V.ServerKey) {
		t.Error("the server private key leaked into a customer's profile")
	}
	if strings.Contains(got, "<cert>") || strings.Contains(got, "<key>") {
		t.Error("the profile carries a client certificate; this is a credential-only deployment")
	}
}

func TestServerAcceptsCredentialsInsteadOfCertificates(t *testing.T) {
	got, err := RenderServer(testIface(t), testLayout())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, want := range []string{
		"verify-client-cert none", // no per-client PKI to issue or revoke
		"username-as-common-name", // so the address directory is keyed by username
		"auth-user-pass-verify",
		"script-security 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("server configuration is missing %q", want)
		}
	}
	// Level 3 would additionally put the password in the environment, where any
	// process on the machine can read it.
	if strings.Contains(got, "script-security 3") {
		t.Error("script-security 3 exposes customer passwords to every process on the host")
	}
}

func TestServerDoesNotAllowOneCredentialTwice(t *testing.T) {
	got, err := RenderServer(testIface(t), testLayout())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// duplicate-cn present would let one account be sold to two people and used
	// by both at once.
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) == "duplicate-cn" {
			t.Fatal("duplicate-cn is enabled; one credential would serve two people")
		}
	}
}

func TestServerPinsAddressesSoQuotasCanFollow(t *testing.T) {
	l := testLayout()
	got, err := RenderServer(testIface(t), l)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Without a client-config-dir the server hands out addresses from a pool,
	// a customer gets a different one on each reconnect, and the quota attached
	// to the old address stops counting them.
	if !strings.Contains(got, "client-config-dir "+l.ClientDir()) {
		t.Error("addresses are not pinned; quota accounting would follow the wrong customer")
	}
	if !strings.Contains(got, "topology subnet") {
		t.Error("without topology subnet each client burns a /30 and addresses are not predictable")
	}
	if !strings.Contains(got, "server 10.67.0.0 255.255.0.0") {
		t.Error("the tunnel subnet is wrong")
	}
}

func TestServerReferencesKeysByPathRatherThanInlining(t *testing.T) {
	iface := testIface(t)
	got, err := RenderServer(iface, testLayout())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// This file is rewritten whenever the interface changes. Inlining the key
	// would rewrite the private key with it, and every rewrite is a chance to
	// leave it half-written or world-readable.
	if strings.Contains(got, "BEGIN PRIVATE KEY") {
		t.Error("the server private key was inlined into the configuration")
	}
	if !strings.Contains(got, "key /var/lib/wui/openvpn/vpn0/server.key") {
		t.Error("the server configuration does not point at its key")
	}
}

func TestHumanCopyWithholdsSecrets(t *testing.T) {
	iface := testIface(t)
	got, err := RenderServerHuman(iface, testLayout())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// This text is displayed in a browser and pasted into chat windows.
	if strings.Contains(got, "BEGIN PRIVATE KEY") ||
		strings.Contains(got, "BEGIN OpenVPN Static key") {
		t.Error("a server secret leaked into the copyable configuration")
	}
	if !strings.Contains(got, "masquerade") {
		t.Error("missing NAT guidance; customers would connect and reach nothing")
	}
}

func TestCredentialsFileIsOneLinePerAccountAndStable(t *testing.T) {
	accounts := []Account{
		{Username: "bravo", Secret: "s2", IP: "10.67.0.3"},
		{Username: "alpha", Secret: "s1", IP: "10.67.0.2"},
	}
	got := RenderCredentials(accounts)

	if got != "alpha:s1\nbravo:s2\n" {
		t.Errorf("credentials = %q, want a sorted username:secret list", got)
	}
	// A different input order must produce an identical file, or the driver
	// rewrites it on every tick and cannot tell that nothing changed.
	reversed := RenderCredentials([]Account{accounts[1], accounts[0]})
	if reversed != got {
		t.Error("the credential file is not stable across input orderings")
	}
}

func TestIncompleteAccountsNeverReachTheCredentialFile(t *testing.T) {
	// A WireGuard account has no username or secret. A blank field would write
	// a line like ":" that an empty login could match.
	got := RenderCredentials([]Account{
		{Username: "", Secret: "s", IP: "10.67.0.2"},
		{Username: "u", Secret: "", IP: "10.67.0.3"},
		{Username: "real", Secret: "ok", IP: "10.67.0.4"},
	})
	if got != "real:ok\n" {
		t.Errorf("credentials = %q, want only the complete account", got)
	}
}

func TestAuthScriptRejectsAnythingOutsideTheIssuedAlphabet(t *testing.T) {
	got := RenderAuthScript(testLayout())

	if !strings.HasPrefix(got, "#!/bin/sh") {
		t.Error("the auth script has no interpreter line and will not run")
	}
	// The username and password arrive from the network. Without these guards a
	// login could carry a shell metacharacter or a newline into the comparison.
	if !strings.Contains(got, `case "$username" in *[!A-Za-z0-9_-]*) exit 1 ;; esac`) {
		t.Error("the username is not validated before use")
	}
	if !strings.Contains(got, `case "$password" in *[!A-Za-z0-9]*) exit 1 ;; esac`) {
		t.Error("the password is not validated before use")
	}
	// -F is what stops a password being treated as a regular expression, and -x
	// stops a prefix matching a longer stored credential.
	if !strings.Contains(got, "grep -qxF --") {
		t.Error("the credential comparison is not a fixed whole-line match")
	}
}

func TestClientConfigPinsTheAccountToItsAddress(t *testing.T) {
	got, err := RenderClientConfig(Account{Username: "u", IP: "10.67.0.5"}, "10.67.0.0/16")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "ifconfig-push 10.67.0.5 255.255.0.0\n" {
		t.Errorf("client config = %q", got)
	}
}

func TestClientConfigRejectsAnUnusableAddress(t *testing.T) {
	if _, err := RenderClientConfig(Account{Username: "u", IP: "nonsense"}, "10.67.0.0/16"); err == nil {
		t.Error("an unparseable address was accepted")
	}
}

func TestPKIProducesAChainAClientWillAccept(t *testing.T) {
	params, err := NewPKI("vpn0")
	if err != nil {
		t.Fatalf("pki: %v", err)
	}

	caBlock, _ := pem.Decode([]byte(params.CACert))
	serverBlock, _ := pem.Decode([]byte(params.ServerCert))
	if caBlock == nil || serverBlock == nil {
		t.Fatal("the PKI did not produce valid PEM")
	}

	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	server, err := x509.ParseCertificate(serverBlock.Bytes)
	if err != nil {
		t.Fatalf("parse server certificate: %v", err)
	}

	if !ca.IsCA {
		t.Error("the authority is not marked as one and will be rejected")
	}
	// This is what `remote-cert-tls server` checks. Without it every client
	// refuses to connect, with a message that does not say why.
	hasServerAuth := false
	for _, u := range server.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	if !hasServerAuth {
		t.Error("the server certificate is not marked for server authentication")
	}

	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := server.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("the server certificate does not verify against its own CA: %v", err)
	}
}

func TestEachInterfaceGetsAnUnrelatedAuthority(t *testing.T) {
	a, err := NewPKI("vpn0")
	if err != nil {
		t.Fatalf("pki: %v", err)
	}
	b, err := NewPKI("vpn1")
	if err != nil {
		t.Fatalf("pki: %v", err)
	}

	// Sharing an authority between interfaces would mean a key leaked from one
	// could be used to impersonate the other.
	if a.CACert == b.CACert || a.ServerKey == b.ServerKey || a.TLSCryptKey == b.TLSCryptKey {
		t.Error("two interfaces were issued the same key material")
	}
}

func TestStaticKeyIsInTheFormatClientsParse(t *testing.T) {
	got, err := NewStaticKey()
	if err != nil {
		t.Fatalf("static key: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if lines[0] != "-----BEGIN OpenVPN Static key V1-----" {
		t.Errorf("first line = %q", lines[0])
	}
	if lines[len(lines)-1] != "-----END OpenVPN Static key V1-----" {
		t.Errorf("last line = %q", lines[len(lines)-1])
	}
	// 256 bytes of key material as hex, 32 characters to a line.
	body := lines[1 : len(lines)-1]
	if len(body) != 16 {
		t.Errorf("key body has %d lines, want 16", len(body))
	}
	for i, l := range body {
		if len(l) != 32 {
			t.Errorf("line %d is %d characters, want 32", i+1, len(l))
		}
	}
}

func TestGeneratedSecretsAreAlwaysAcceptedByTheAuthScript(t *testing.T) {
	// The generator and the script's validation are two halves of one rule. If
	// they disagree, the panel issues credentials that are refused at login and
	// nothing in any log says why. This ties them together.
	allowed := map[rune]bool{}
	for _, r := range SecretAlphabet {
		allowed[r] = true
	}

	for i := 0; i < 200; i++ {
		secret, err := NewSecret(16)
		if err != nil {
			t.Fatalf("secret: %v", err)
		}
		if len(secret) != 16 {
			t.Fatalf("secret %q is %d characters, want 16", secret, len(secret))
		}
		for _, r := range secret {
			// The script rejects anything outside [A-Za-z0-9].
			isAlnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !isAlnum || !allowed[r] {
				t.Fatalf("secret %q contains %q, which the auth script would reject", secret, r)
			}
		}
	}
}

func TestSecretsDoNotRepeat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s, err := NewSecret(16)
		if err != nil {
			t.Fatalf("secret: %v", err)
		}
		if seen[s] {
			t.Fatalf("the generator repeated %q", s)
		}
		seen[s] = true
	}
}
