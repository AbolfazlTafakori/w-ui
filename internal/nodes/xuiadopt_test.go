package nodes

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A 3x-ui panel's own users brought in as customers here: offered with what
// they have, taken over where they are -- same client, same credentials,
// same links -- their limits there cleared and kept here, switched from
// here, counted from the moment they came, and left there when removed here.
func TestA3xuiPanelsOwnUsersAreBroughtIn(t *testing.T) {
	for _, version := range []string{"2.9.4", "3.10.2"} {
		t.Run(version, func(t *testing.T) {
			f := newFakeXUI(t, version)
			vless := f.addInbound("vless-in", "vless", 31101, map[string]any{"decryption": "none"}, nil)
			trojan := f.addInbound("trojan-in", "trojan", 31102, nil, nil)
			expiry := time.Now().Add(20 * 24 * time.Hour).UnixMilli()
			// One user on two inbounds, by one subscription id, as 3x-ui
			// makes it: one client each before 3.1.
			aliceV := map[string]any{"id": "a11ce000-0000-4000-8000-000000000001", "email": "alice-v", "enable": true, "subId": "alicesub",
				"totalGB": float64(10 << 30), "expiryTime": float64(expiry), "limitIp": float64(2), "comment": "Alice", "tgId": "12345"}
			aliceT := map[string]any{"password": "alice-trojan-pw", "email": "alice-t", "enable": true, "subId": "alicesub",
				"totalGB": float64(10 << 30), "expiryTime": float64(expiry), "limitIp": float64(2), "comment": "Alice"}
			f.addOwnClient(vless.ID, aliceV)
			f.addOwnClient(trojan.ID, aliceT)
			// Off there, and starting on first use.
			f.addOwnClient(vless.ID, map[string]any{"id": "b0b00000-0000-4000-8000-000000000002", "email": "bob", "enable": false,
				"subId": "bobsub", "expiryTime": float64(-7 * 24 * time.Hour / time.Millisecond)})
			f.count(vless.ID, "alice-v", 300, 700)

			n := xuiNode(f, version >= "3", true)
			r := newRig(t, n)
			r.round()

			users, err := r.clients.NodeUsers(r.ctx, r.node.ID)
			if err != nil {
				t.Fatal(err)
			}
			byKey := map[string]int{}
			for i, u := range users {
				byKey[u.Key] = i
			}
			a := users[byKey["alicesub"]]
			if len(users) != 2 || len(a.Files) != 2 || a.Name != "Alice" || a.QuotaBytes != 10<<30 || a.UsedBytes != 1000 ||
				a.DeviceLimit != 2 || a.ExpiresAt == nil || !a.Enabled {
				t.Fatalf("alice offered as %+v (of %d)", a, len(users))
			}
			if b := users[byKey["bobsub"]]; b.Enabled || b.DurationDays != 7 {
				t.Fatalf("bob offered as %+v", b)
			}

			res, err := r.clients.ImportNodeUsers(r.ctx, r.node.ID, nil)
			if err != nil || len(res) != 2 || res[0].Error != "" || res[1].Error != "" {
				t.Fatalf("brought in %+v, %v", res, err)
			}
			again, _ := r.clients.ImportNodeUsers(r.ctx, r.node.ID, []string{"alicesub"})
			if len(again) != 1 || !strings.Contains(again[0].Error, "already") {
				t.Fatalf("brought in twice: %+v", again)
			}
			var alice model.Client
			r.db.Where("name = ?", "Alice").First(&alice)
			if alice.QuotaBytes != 10<<30 || alice.UsedBytes != 1000 || alice.ExpiresAt == nil {
				t.Fatalf("alice here is %+v", alice)
			}

			r.round()
			r.round()
			got := f.client(vless.ID, "alice-v")
			if got["id"] != aliceV["id"] || got["enable"] != true || got["totalGB"] != float64(0) || got["expiryTime"] != float64(0) {
				t.Fatalf("alice-v there is %v: same id, on, limits cleared", got)
			}
			// What this panel does not know of is sent back as it was.
			if got["subId"] != "alicesub" || got["comment"] != "Alice" || got["limitIp"] != float64(2) || got["tgId"] != "12345" {
				t.Fatalf("alice-v lost fields of its own: %v", got)
			}
			if f.client(trojan.ID, "alice-t")["password"] != "alice-trojan-pw" {
				t.Fatal("alice-t's password changed")
			}
			if f.client(vless.ID, "bob")["enable"] != false {
				t.Fatal("bob, off there, was switched on")
			}
			// Nothing of this panel's own was made for them.
			for _, e := range append(f.emails(vless.ID), f.emails(trojan.ID)...) {
				if strings.HasPrefix(e, "wui-") {
					t.Fatalf("a client %s was made for a user brought in", e)
				}
			}
			// The same links, in the customer's subscription here.
			if acc := r.account(alice.ID, "vless-in"); !strings.Contains(acc.XUILink, aliceV["id"].(string)) {
				t.Fatalf("alice's links here %q", acc.XUILink)
			}
			if acc := r.account(alice.ID, "trojan-in"); !strings.Contains(acc.XUILink, "alice-trojan-pw") {
				t.Fatalf("alice's trojan links here %q", acc.XUILink)
			}

			// Counted from when she came, not again for before.
			f.count(vless.ID, "alice-v", 50, 150)
			r.round()
			if got := r.chargedTo(alice.ID); got != 200 {
				t.Fatalf("charged %d, want the 200 bytes since she was brought in", got)
			}

			r.setStatus(alice.ID, model.StatusDisabled)
			r.round()
			if f.client(vless.ID, "alice-v")["enable"] != false || f.client(trojan.ID, "alice-t")["enable"] != false {
				t.Fatal("switched off here, still on there")
			}
			r.setStatus(alice.ID, model.StatusActive)
			r.round()
			if f.client(vless.ID, "alice-v")["enable"] != true {
				t.Fatal("switched on here, still off there")
			}

			// Switched off there by hand: kept off, and here too.
			off := f.client(vless.ID, "alice-v")
			f.mu.Lock()
			off["enable"] = false
			f.mu.Unlock()
			r.round()
			if f.client(vless.ID, "alice-v")["enable"] != false {
				t.Fatal("switched off there by hand, switched on again")
			}
			if r.account(alice.ID, "vless-in").Enabled {
				t.Fatal("switched off there by hand, still on here")
			}

			// Removed here: left there as it was.
			if err := r.clients.Delete(r.ctx, alice.ID); err != nil {
				t.Fatal(err)
			}
			r.round()
			if f.client(vless.ID, "alice-v") == nil || f.client(trojan.ID, "alice-t") == nil {
				t.Fatal("a user removed here was removed there")
			}
		})
	}
}

// Two asked to bring the same users in at once -- a button pressed twice --
// bring each in once.
func TestUsersAskedInTwiceAtOnceAreBroughtInOnce(t *testing.T) {
	f := newFakeXUI(t, "3.10.2")
	in := f.addInbound("vless-in", "vless", 31401, map[string]any{"decryption": "none"}, nil)
	for i := 0; i < 5; i++ {
		f.addOwnClient(in.ID, map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-00000000000%d", i), "email": fmt.Sprintf("u%d", i),
			"enable": true, "subId": fmt.Sprintf("sub%d", i)})
	}
	r := newRig(t, xuiNode(f, true, false))
	r.round()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.clients.ImportNodeUsers(r.ctx, r.node.ID, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int64
	r.db.Model(&model.Client{}).Count(&n)
	if n != 5 {
		t.Fatalf("%d customers for 5 users", n)
	}
}
