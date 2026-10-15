package service

import (
	"net/url"
	"testing"
)

func TestWireGuardLink(t *testing.T) {
	conf := "[Interface]\nPrivateKey = QD9d+Y51/TNS=\nAddress = 10.66.0.3/32\nDNS = 10.66.0.1\nMTU = 1420\n\n[Peer]\nPublicKey = gT06/sed+ISq=\nPresharedKey = AkOw+IQH/LM=\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = 87.236.38.145:51820\nPersistentKeepalive = 25\n"
	link, ok := WireGuardLink(conf, "wg0")
	if !ok {
		t.Fatal("no link")
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	priv := u.User.Username()
	q := u.Query()
	if u.Scheme != "wireguard" || priv != "QD9d+Y51/TNS=" || u.Host != "87.236.38.145:51820" ||
		q.Get("publickey") != "gT06/sed+ISq=" || q.Get("presharedkey") != "AkOw+IQH/LM=" ||
		q.Get("address") != "10.66.0.3/32" || q.Get("mtu") != "1420" || u.Fragment != "wg0" {
		t.Fatalf("link %s", link)
	}
	if _, ok := WireGuardLink("[Interface]\nPrivateKey = x\n", "a"); ok {
		t.Fatal("a file with no peer made a link")
	}
}
