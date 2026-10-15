package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestPinFirstCertReplacesAllowInsecure(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	var ob map[string]any
	json.Unmarshal([]byte(`{"protocol":"trojan","streamSettings":{"security":"tls","tlsSettings":{"serverName":"example.com","allowInsecure":true,"pinnedPeerCertSha256":""}}}`), &ob)
	if err := pinFirstCert(context.Background(), ob, host, n); err != nil {
		t.Fatal(err)
	}
	ts := ob["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
	if _, ok := ts["allowInsecure"]; ok {
		t.Fatal("allowInsecure kept; xray refuses it")
	}
	sum := sha256.Sum256(srv.Certificate().Raw)
	if ts["pinnedPeerCertSha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("pin %v", ts["pinnedPeerCertSha256"])
	}
}

func TestPinFirstCertLeavesVerifiedAlone(t *testing.T) {
	var ob map[string]any
	json.Unmarshal([]byte(`{"streamSettings":{"tlsSettings":{"serverName":"example.com"}}}`), &ob)
	if err := pinFirstCert(context.Background(), ob, "192.0.2.1", 1); err != nil {
		t.Fatal(err)
	}
}

func TestHysteriaForSingbox(t *testing.T) {
	var ob map[string]any
	json.Unmarshal([]byte(`{"protocol":"hysteria","settings":{"address":"h.example","port":443,"version":2},
		"streamSettings":{"network":"hysteria","hysteriaSettings":{"auth":"pw"},"tlsSettings":{"serverName":"s.example","alpn":["h3"],"allowInsecure":true},
		"finalmask":{"udp":[{"type":"salamander","settings":{"password":"ob"}}]}}}`), &ob)
	out, err := hysteriaForSingbox(ob)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	want := `{"obfs":{"password":"ob","type":"salamander"},"password":"pw","server":"h.example","server_port":443,"tag":"proxy","tls":{"alpn":["h3"],"enabled":true,"insecure":true,"server_name":"s.example"},"type":"hysteria2"}`
	if string(raw) != want {
		t.Fatalf("got %s", raw)
	}
}
