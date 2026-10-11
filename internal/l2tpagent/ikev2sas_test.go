package l2tpagent

import "testing"

// What strongSwan 5.9 prints, as it prints it.
const rawSAs = `list-sa event {wui-ikev2-1-mschapv2 {uniqueid=12 version=2 state=CONNECTING local-host=172.31.99.10 local-port=4500 local-id=vpn.example.test remote-host=172.31.99.112 remote-port=4500 remote-id=s1-dev01 tasks-passive=[IKE_CERT_PRE IKE_AUTH] child-sas {}}}
list-sa event {wui-ikev2-4-eap-tls {uniqueid=4 version=2 state=ESTABLISHED local-host=172.31.99.10 local-port=4500 local-id=vpn.example.test remote-host=172.31.99.104 remote-port=4500 remote-id=tls-s1-dev04 established=47 rekey-time=13880 remote-vips=[10.79.0.5] tasks-active=[IKE_DPD] child-sas {wui-ikev2-4-eap-tls-4 {name=wui-ikev2-4-eap-tls uniqueid=4 reqid=4 state=INSTALLED mode=TUNNEL bytes-in=1200 packets-in=3 bytes-out=880 packets-out=2 local-ts=[0.0.0.0/0] remote-ts=[10.79.0.5/32]} wui-ikev2-4-eap-tls-9 {name=wui-ikev2-4-eap-tls bytes-in=100 bytes-out=20}}}}
list-sa event {wui-l2tp {uniqueid=2 version=1 state=ESTABLISHED remote-host=198.51.100.7 remote-id=198.51.100.7 established=9 child-sas {}}}
something else entirely
`

func TestTheSessionsStrongSwanListsAreRead(t *testing.T) {
	sas := parseSAs([]byte(rawSAs))
	if len(sas) != 3 {
		t.Fatalf("%d sessions: %+v", len(sas), sas)
	}
	by := map[string]IKESA{}
	for _, s := range sas {
		by[s.Conn] = s
	}
	half := by["wui-ikev2-1-mschapv2"]
	if half.Established || half.ID != 12 || half.VIP.IsValid() {
		t.Fatalf("a session being negotiated: %+v", half)
	}
	tls := by["wui-ikev2-4-eap-tls"]
	if !tls.Established || tls.ID != 4 || tls.RemoteHost != "172.31.99.104" || tls.RemoteID != "tls-s1-dev04" ||
		tls.VIP.String() != "10.79.0.5" || tls.Age != 47 || tls.BytesIn != 1300 || tls.BytesOut != 900 {
		t.Fatalf("an established session: %+v", tls)
	}
	if l2tp := by["wui-l2tp"]; !l2tp.Established || l2tp.VIP.IsValid() {
		t.Fatalf("L2TP's: %+v", l2tp)
	}
}
