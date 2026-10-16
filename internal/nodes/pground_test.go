package nodes

import (
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// The rounds against a Pasarguard panel, end to end, by an API key and by
// an administrator's password.

func pgNodeFor(f *fakePG, byKey bool) model.Node {
	n := model.Node{Kind: model.KindPG, Address: f.address(), Enabled: true}
	if byKey {
		n.Token = f.apiKey
	} else {
		n.Username, n.Password = f.user, f.pass
	}
	return n
}

func vlessInbound(tag string, port int) map[string]any {
	return map[string]any{"tag": tag, "port": port, "protocol": "vless", "listen": "0.0.0.0",
		"settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": map[string]any{"network": "tcp", "security": "none"}}
}

func TestAPasarguardCustomerFromStartToEnd(t *testing.T) {
	for _, byKey := range []bool{true, false} {
		name := map[bool]string{true: "by API key", false: "by password"}[byKey]
		t.Run(name, func(t *testing.T) {
			f := newFakePG(t)
			f.addInbound(vlessInbound("VLESS TCP", 32001))
			own := f.addGroup("theirs", "VLESS TCP")
			f.addUser(fakePGUser{Username: "owner", Status: "active", Groups: []int{own}})

			r := newRig(t, pgNodeFor(f, byKey))
			r.round()
			c := r.customer("erin", "VLESS TCP")
			r.round()
			acc := r.account(c.ID, "VLESS TCP")
			uname := service.PGUsername(r.tag(), &acc)
			u := f.get(uname)
			if u == nil || u.Status != "active" || len(u.Groups) != 1 {
				t.Fatalf("user there %+v", u)
			}
			r.round()
			if a := r.account(c.ID, "VLESS TCP"); !strings.Contains(a.XUILink, u.Password) {
				t.Fatalf("links here %q", a.XUILink)
			}

			f.mu.Lock()
			f.users[uname].Used += 7000
			f.mu.Unlock()
			r.round()
			r.round()
			if got := r.chargedTo(c.ID); got != 7000 {
				t.Errorf("charged %d, want 7000", got)
			}

			r.setStatus(c.ID, model.StatusDisabled)
			r.round()
			if f.get(uname).Status != "disabled" {
				t.Fatal("switched off here, active there")
			}
			if err := r.clients.Delete(r.ctx, c.ID); err != nil {
				t.Fatal(err)
			}
			r.round()
			if f.get(uname) != nil {
				t.Fatal("removed here, still there")
			}
			if o := f.get("owner"); o == nil || o.Status != "active" || o.Groups[0] != own {
				t.Fatalf("the operator's own user changed: %+v", o)
			}
		})
	}
}

// A Shadowsocks-2022 inbound made here keeps taking users: its node would
// otherwise start it one-key, refuse the first customer, and with that stop
// syncing every user it has.
func TestAShadowsocks2022InboundOnPasarguardKeepsItsNodeSyncing(t *testing.T) {
	defer func(d time.Duration) { pgSettle = d }(pgSettle)
	pgSettle = time.Millisecond
	f := newFakePG(t)
	r := newRig(t, pgNodeFor(f, true))
	_, err := CreateXrayInbound(r.ctx, r.node, service.XrayInboundInput{
		Remark: "SS22", Port: 32002, Protocol: "shadowsocks", Customers: true,
		Settings: map[string]any{"method": "2022-blake3-aes-256-gcm", "password": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "clients": []any{}, "network": "tcp,udp"},
		Stream:   map[string]any{"network": "tcp", "security": "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.syncBroken {
		t.Fatal("making the inbound broke its node's sync, if only once")
	}
	r.round()
	r.customer("frank", "SS22")
	r.round()
	if f.syncBroken {
		t.Fatal("a customer on the new inbound broke its node's sync of every user")
	}
	var keep bool
	for n, u := range f.users {
		keep = keep || strings.HasPrefix(n, "wuikeep_") && u.Status == "active"
	}
	if !keep {
		t.Fatal("no standing user keeps the inbound taking users")
	}
	// It is not a customer's, and no round takes it away.
	r.round()
	r.round()
	for n := range f.users {
		if strings.HasPrefix(n, "wuikeep_") {
			return
		}
	}
	t.Fatal("the standing user was removed")
}

// A Pasarguard panel's own users brought in: same user, same groups, its
// limits cleared and kept here; switched from here; left there when removed.
func TestAPasarguardPanelsOwnUsersAreBroughtIn(t *testing.T) {
	f := newFakePG(t)
	f.addInbound(vlessInbound("VLESS TCP", 32003))
	f.addInbound(vlessInbound("VLESS WS", 32004))
	g := f.addGroup("owners", "VLESS TCP", "VLESS WS")
	exp := time.Now().Add(10 * 24 * time.Hour).UTC().Truncate(time.Second)
	f.addUser(fakePGUser{Username: "dave", Status: "active", Groups: []int{g}, DataLimit: 5 << 30, Expire: &exp, Used: 1234, Note: "Dave"})
	f.addUser(fakePGUser{Username: "erin", Status: "on_hold", Groups: []int{g}, OnHold: 30 * 86400})

	r := newRig(t, pgNodeFor(f, true))
	r.round()
	users, err := r.clients.NodeUsers(r.ctx, r.node.ID)
	if err != nil || len(users) != 2 {
		t.Fatalf("offered %+v, %v", users, err)
	}
	for _, u := range users {
		switch u.Key {
		case "dave":
			if u.QuotaBytes != 5<<30 || u.UsedBytes != 1234 || u.ExpiresAt == nil || !u.ExpiresAt.Equal(exp) || len(u.Inbounds) != 2 {
				t.Fatalf("dave offered as %+v", u)
			}
		case "erin":
			if u.DurationDays != 30 {
				t.Fatalf("erin offered as %+v", u)
			}
		}
	}
	if res, err := r.clients.ImportNodeUsers(r.ctx, r.node.ID, nil); err != nil || len(res) != 2 || res[0].Error != "" || res[1].Error != "" {
		t.Fatalf("brought in %+v, %v", res, err)
	}
	f.mu.Lock()
	f.everyUser = 0
	f.mu.Unlock()
	r.round()
	r.round()
	// The users brought in are asked for by name, not by reading every
	// user there each round.
	if f.everyUser != 0 {
		t.Fatalf("every user there read %d times in two rounds", f.everyUser)
	}
	d := f.get("dave")
	if d.DataLimit != 0 || d.Expire != nil || d.Status != "active" || len(d.Groups) != 1 || d.Groups[0] != g || d.Password != "pw-dave" {
		t.Fatalf("dave there is %+v: same user and groups, limits cleared", d)
	}
	if e := f.get("erin"); e.Status != "active" {
		t.Fatalf("erin there is %s, want active, her clock kept here", e.Status)
	}
	var dave model.Client
	r.db.Where("name = ?", "dave").First(&dave)
	var accs []model.Account
	r.db.Where("client_id = ?", dave.ID).Find(&accs)
	if len(accs) != 1 || !strings.Contains(accs[0].XUILink, "VLESS%20WS") {
		t.Fatalf("dave here: one file, whose links carry both inbounds: %+v", accs)
	}

	f.mu.Lock()
	f.users["dave"].Used += 500
	f.mu.Unlock()
	r.round()
	if got := r.chargedTo(dave.ID); got != 500 {
		t.Fatalf("charged %d, want the 500 since he came", got)
	}

	r.setStatus(dave.ID, model.StatusDisabled)
	r.round()
	if f.get("dave").Status != "disabled" {
		t.Fatal("switched off here, active there")
	}
	r.setStatus(dave.ID, model.StatusActive)
	r.round()
	if f.get("dave").Status != "active" {
		t.Fatal("switched on here, off there")
	}
	f.mu.Lock()
	f.users["dave"].Status = "disabled" // by its operator
	f.mu.Unlock()
	r.round()
	if f.get("dave").Status != "disabled" {
		t.Fatal("switched off there by hand, switched on again")
	}
	if err := r.clients.Delete(r.ctx, dave.ID); err != nil {
		t.Fatal(err)
	}
	r.round()
	if f.get("dave") == nil {
		t.Fatal("removed here, removed there")
	}
}
