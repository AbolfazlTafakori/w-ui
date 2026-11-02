package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
)

func wuiShape(in CreateInterfaceInput, port int, ws WSSettings) CreateInterfaceInput {
	in.Transport, in.TransportPort, in.TransportWS = model.TransportMold, port, &ws
	return in
}

// mold takes relay's settings and one more: the request shape, given
// as a preset name and stored as the full object, in its one canonical form.
func TestAWireGuardTunnelTakesWuiShapeBehindAFront(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 2)
	tcp := freeTCPPort(t)

	wg0, err := svc.Create(ctx, wuiShape(wgInput("wg0", ports[0], "10.66.0.0/24"), tcp, WSSettings{Host: "shape.example.com", Origin: "https://127.0.0.1:8443", Shape: "post-stream"}))
	if err != nil {
		t.Fatal(err)
	}
	p := wg0.Stealth.V
	if wg0.WireGuardTransport() != model.TransportMold || wg0.StealthShape() != tcp || !p.Fronted() || !p.Proven() {
		t.Fatalf("made as %s on %d: %+v", wg0.WireGuardTransport(), wg0.StealthShape(), p)
	}
	if model.TransportNetwork(model.TransportMold) != "tcp" {
		t.Fatal("mold is a tcp transport")
	}
	// The preset was resolved and stored as the full canonical object.
	sh, err := stealth.ParseShape([]byte(p.Shape))
	if err != nil {
		t.Fatalf("the stored shape does not parse: %v (%q)", err, p.Shape)
	}
	if sh.Method != "POST" || sh.Place != stealth.InBody || sh.Mode != stealth.ModeSse {
		t.Fatalf("post-stream resolved wrong: %+v", sh)
	}
	var obj map[string]any
	if json.Unmarshal([]byte(p.Shape), &obj) != nil || obj["up"] == nil {
		t.Fatalf("the shape is not a JSON object: %q", p.Shape)
	}

	// An empty shape defaults to get-poll; a junk one is refused.
	wg1, err := svc.Create(ctx, wuiShape(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), WSSettings{Host: "shape.example.com", Origin: "https://127.0.0.1:8443"}))
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := stealth.ParseShape([]byte(wg1.Stealth.V.Shape)); d.Method != "GET" || d.Mode != stealth.ModePoll {
		t.Fatalf("the default is not get-poll: %+v", d)
	}
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{TransportWS: &WSSettings{Host: "shape.example.com", Origin: "https://127.0.0.1:8443", Shape: "nonsense"}}); FieldOf(err) != "transportWs.shape" {
		t.Fatalf("a junk shape was taken: %v", err)
	}
}
