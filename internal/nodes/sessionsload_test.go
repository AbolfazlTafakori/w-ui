package nodes

import (
	"fmt"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fleet"
	"github.com/abolfazl/w-ui/internal/service"
)

// What asking ten 3x-ui nodes who is online costs, with 200 of this panel's
// files online on each: requests sent each minute, and the panel's time per
// round. Logged, for the report; it fails only if a round takes more than
// its own interval.
func TestTheLoadOfAskingTenNodesWhoIsOnline(t *testing.T) {
	if testing.Short() {
		t.Skip("a measurement")
	}
	const nodes, online = 10, 200
	var fakes []*fakeXUI
	r := newRig(t, model.Node{Name: "local-dummy", Kind: model.KindLocal})
	var tag string
	for n := 0; n < nodes; n++ {
		f := newFakeXUI(t, "3.10.2")
		f.addInbound("vless-in", "vless", 31001, map[string]any{"decryption": "none"}, nil)
		node := xuiNode(f, true, false)
		node.Name, node.AllowPrivateAddress = fmt.Sprintf("x%d", n), true
		if err := r.db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		if err := r.s.xuiRound(r.ctx, node); err != nil {
			t.Fatal(err)
		}
		var iface model.Interface
		r.db.Where("node_id = ?", node.ID).First(&iface)
		if tag == "" {
			tag = r.tag()
		}
		var emails []string
		ips := map[string]any{}
		now := time.Now().Unix() * 1000
		for i := 0; i < online; i++ {
			c := model.Client{Name: fmt.Sprintf("n%dc%d", n, i), Status: model.StatusActive, DeviceLimit: 1}
			if err := r.db.Create(&c).Error; err != nil {
				t.Fatal(err)
			}
			a := model.Account{ClientID: c.ID, InterfaceID: iface.ID, NodeID: node.ID, DeviceName: "d", Enabled: true,
				IP: fmt.Sprintf("10.%d.%d.%d", 100+n, i/250, i%250+1), Secret: fmt.Sprintf("s-%d-%d", n, i)}
			if err := r.db.Create(&a).Error; err != nil {
				t.Fatal(err)
			}
			e := service.XUIEmail(tag, &a)
			emails = append(emails, e)
			ips[e] = []map[string]any{{"ip": "198.51.100.7", "lastSeen": now}}
		}
		f.mu.Lock()
		f.online, f.ips, f.requests = emails, ips, 0
		f.mu.Unlock()
		fakes = append(fakes, f)
	}
	reported := 0
	r.s.Sessions = func(_ uint, ss []fleet.NodeSession) { reported += len(ss) }

	// One minute of rounds at the real interval's count: 20 rounds of 3s.
	var longest, total time.Duration
	for round := 0; round < 20; round++ {
		if round%10 == 0 { // addresses are asked every 30s: every tenth round
			r.s.mu.Lock()
			r.s.xuiAddrsAt = map[uint]time.Time{}
			r.s.mu.Unlock()
		}
		t0 := time.Now()
		r.s.sessionsRound(r.ctx)
		d := time.Since(t0)
		r.s.addrsWG.Wait() // the questions beside it, before the next is timed
		total += d
		longest = max(longest, d)
	}
	sent := 0
	for _, f := range fakes {
		f.mu.Lock()
		sent += f.requests
		f.mu.Unlock()
	}
	t.Logf("%d nodes x %d online, one minute: %d requests to the nodes (%d a node), panel time %v in all, longest round %v; sessions reported %d",
		nodes, online, sent, sent/nodes, total.Round(time.Millisecond), longest.Round(time.Millisecond), reported)
	if longest > sessionInterval {
		t.Errorf("a round took %v, longer than its %v interval", longest, sessionInterval)
	}
}
