package wgdriver

import (
	"net/netip"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// Real WireGuard keys, because wgtypes parses and validates them.
const (
	keyA = "kte0/Opmhlud6z6Y/fjY7ZA2c5+7PsuWt/ZdIexq8lE="
	keyB = "L0vbLTAiIieCehmdcs54oQQTW+ZJwtrdZcbsjjRUr2o="
	keyC = "6h08A00kP7oLue/9R2GX5tTlkpNiyyew4GcC1ouUPEw="
)

func acc(id uint, key, ip string) backend.DesiredAccount {
	return backend.DesiredAccount{ID: id, PublicKey: key, IP: netip.MustParseAddr(ip)}
}

func TestNewPeerIsAdded(t *testing.T) {
	d := computeDiff(toPeerSet([]backend.DesiredAccount{acc(1, "K1", "10.66.0.2")}), map[string]string{})
	if len(d.add) != 1 || d.add[0].ID != 1 {
		t.Fatalf("add = %+v, want the one new account", d.add)
	}
	if len(d.remove) != 0 || len(d.update) != 0 {
		t.Error("nothing should be removed or updated on an empty interface")
	}
}

func TestUnknownPeerIsRemoved(t *testing.T) {
	// A key the kernel holds but the database no longer lists belongs to a
	// deleted or cut-off customer; leaving it would keep them connected.
	d := computeDiff(peerSet{}, map[string]string{"STALE": "10.66.0.9"})
	if len(d.remove) != 1 || d.remove[0] != "STALE" {
		t.Fatalf("remove = %v, want the stale key", d.remove)
	}
}

func TestUnchangedPeerIsLeftAlone(t *testing.T) {
	want := toPeerSet([]backend.DesiredAccount{acc(1, "K1", "10.66.0.2")})
	d := computeDiff(want, map[string]string{"K1": "10.66.0.2"})
	if len(d.add)+len(d.remove)+len(d.update) != 0 {
		t.Errorf("a matching peer produced work: %+v", d)
	}
	if d.report(1).Changed() {
		t.Error("an unchanged interface should not report a change")
	}
}

func TestMovedAddressIsAnUpdate(t *testing.T) {
	// Same key, different address: the allowed-ips filter has to follow or the
	// peer can no longer send anything.
	want := toPeerSet([]backend.DesiredAccount{acc(1, "K1", "10.66.0.5")})
	d := computeDiff(want, map[string]string{"K1": "10.66.0.2"})
	if len(d.update) != 1 {
		t.Fatalf("update = %+v, want one", d.update)
	}
	if len(d.add) != 0 || len(d.remove) != 0 {
		t.Error("moving an address should not add or remove a peer")
	}
}

func TestRotatedKeyRevokesTheOldOne(t *testing.T) {
	// Reissuing a device must make the old key stop working immediately, so a
	// rotation has to read as a removal plus an addition.
	want := toPeerSet([]backend.DesiredAccount{acc(1, "NEW", "10.66.0.2")})
	d := computeDiff(want, map[string]string{"OLD": "10.66.0.2"})

	if len(d.add) != 1 || d.add[0].PublicKey != "NEW" {
		t.Errorf("the new key was not added: %+v", d.add)
	}
	if len(d.remove) != 1 || d.remove[0] != "OLD" {
		t.Errorf("the old key was not revoked: %v", d.remove)
	}
}

func TestAccountWithoutAKeyIsSkipped(t *testing.T) {
	// An OpenVPN account carries no public key. Letting it through would send
	// an empty peer to the kernel and fail the whole sync.
	set := toPeerSet([]backend.DesiredAccount{
		acc(1, "", "10.66.0.2"),
		acc(2, "K2", "10.66.0.3"),
	})
	if len(set) != 1 {
		t.Errorf("peer set has %d entries, want only the one with a key", len(set))
	}
}

func TestAmneziaSelectsItsOwnToolAndLinkType(t *testing.T) {
	std := &model.Interface{Mode: model.ModeStandard}
	amn := &model.Interface{Mode: model.ModeAmnezia}

	if toolFor(std) != "wg" || linkType(std) != "wireguard" {
		t.Error("standard interface should use wg / wireguard")
	}
	// Driving an AmneziaWG interface with plain wg silently drops every
	// obfuscation parameter, leaving a tunnel that looks fine and fingerprints
	// as ordinary WireGuard.
	if toolFor(amn) != "awg" || linkType(amn) != "amneziawg" {
		t.Error("amnezia interface should use awg / amneziawg")
	}
}

func TestPeerIsConfinedToASingleAddress(t *testing.T) {
	got := allowedIP(acc(1, "K", "10.66.0.7"))
	// A wider mask here would let one customer send as another and have the
	// traffic billed to them.
	if got != "10.66.0.7/32" {
		t.Errorf("allowedIP = %q, want a /32", got)
	}
}

func TestReportCountsEveryCategory(t *testing.T) {
	want := toPeerSet([]backend.DesiredAccount{
		acc(1, "KEEP", "10.66.0.2"),
		acc(2, "MOVE", "10.66.0.9"),
		acc(3, "NEW", "10.66.0.4"),
	})
	d := computeDiff(want, map[string]string{
		"KEEP":  "10.66.0.2",
		"MOVE":  "10.66.0.3",
		"STALE": "10.66.0.8",
	})
	r := d.report(len(want) - len(d.add) - len(d.update))

	if r.Added != 1 || r.Updated != 1 || r.Removed != 1 || r.Unchanged != 1 {
		t.Errorf("report = %+v, want 1 of each", r)
	}
}

// The reconciler calls Sync every two seconds, almost always with nothing to do.
// If an unchanged interface still produced peer operations, every customer's
// handshake would be torn down on a two-second cycle and no traffic would ever
// pass. This is invisible to a config diff and only shows up as "connected but
// nothing loads", so it is pinned here.
func TestUnchangedInterfaceIssuesNoPeerOperations(t *testing.T) {
	want := toPeerSet([]backend.DesiredAccount{acc(1, keyA, "10.66.0.2")})
	got := peerConfigs(computeDiff(want, map[string]string{keyA: "10.66.0.2"}))

	if len(got) != 0 {
		t.Fatalf("an unchanged peer produced %d operations; live sessions would be reset every tick", len(got))
	}
}

func TestOnlyChangedPeersAreTouched(t *testing.T) {
	want := toPeerSet([]backend.DesiredAccount{
		acc(1, keyA, "10.66.0.2"), // unchanged
		acc(2, keyB, "10.66.0.9"), // moved
	})
	got := peerConfigs(computeDiff(want, map[string]string{
		keyA: "10.66.0.2",
		keyB: "10.66.0.3",
		keyC: "10.66.0.8", // stale
	}))

	if len(got) != 2 {
		t.Fatalf("got %d operations, want one update and one removal", len(got))
	}

	byKey := map[string]wgtypes.PeerConfig{}
	for _, pc := range got {
		byKey[pc.PublicKey.String()] = pc
	}
	if _, touched := byKey[keyA]; touched {
		t.Error("the unchanged peer was included; its session would be disturbed")
	}
	if pc, ok := byKey[keyB]; !ok || pc.Remove {
		t.Error("the moved peer should be updated, not removed")
	} else if len(pc.AllowedIPs) != 1 || pc.AllowedIPs[0].String() != "10.66.0.9/32" {
		t.Errorf("moved peer allowed-ips = %v, want 10.66.0.9/32", pc.AllowedIPs)
	}
	if pc, ok := byKey[keyC]; !ok || !pc.Remove {
		t.Error("the stale peer was not marked for removal; a cut-off customer would stay connected")
	}
}

func TestRemovalCarriesNoAddressOrKey(t *testing.T) {
	got := peerConfigs(computeDiff(peerSet{}, map[string]string{keyA: "10.66.0.2"}))
	if len(got) != 1 || !got[0].Remove {
		t.Fatalf("got %+v, want a single removal", got)
	}
	// A removal that also carried allowed-ips would re-add the peer on some
	// kernels instead of deleting it.
	if len(got[0].AllowedIPs) != 0 || got[0].PresharedKey != nil {
		t.Error("the removal carried peer attributes")
	}
}

func TestPresharedKeyIsAppliedToNewPeers(t *testing.T) {
	a := acc(1, keyA, "10.66.0.2")
	a.PresharedKey = keyB
	got := peerConfigs(computeDiff(toPeerSet([]backend.DesiredAccount{a}), map[string]string{}))

	if len(got) != 1 {
		t.Fatalf("got %d operations, want one addition", len(got))
	}
	// Without the preshared key the server and client derive different session
	// keys and the handshake fails with nothing logged to explain it.
	if got[0].PresharedKey == nil || got[0].PresharedKey.String() != keyB {
		t.Error("the preshared key was not applied")
	}
}

func TestUnparseableKeyIsSkippedWithoutFailingTheBatch(t *testing.T) {
	// One corrupt row must not stop every other customer from being configured.
	want := toPeerSet([]backend.DesiredAccount{
		acc(1, "not-a-key", "10.66.0.2"),
		acc(2, keyA, "10.66.0.3"),
	})
	got := peerConfigs(computeDiff(want, map[string]string{}))

	if len(got) != 1 || got[0].PublicKey.String() != keyA {
		t.Fatalf("got %+v, want only the valid peer", got)
	}
}
