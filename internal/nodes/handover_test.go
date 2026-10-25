package nodes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fleet"
	"github.com/abolfazl/w-ui/internal/handover"
	"github.com/abolfazl/w-ui/internal/service"
)

// wuiNode is a W-UI panel used as a node, in this process: its own database
// and the node half of the sync, behind the endpoints the managing panel
// calls.
type wuiNode struct {
	db         *gorm.DB
	sync       *fleet.NodeSync
	srv        *httptest.Server
	claimFails atomic.Bool
}

func newWUINode(t *testing.T) *wuiNode {
	t.Helper()
	n := &wuiNode{db: testDB(t)}
	if err := n.db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	n.sync = fleet.NewNodeSync(n.db, quietLog())
	refuse := func(w http.ResponseWriter, err error) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/node/handover", func(w http.ResponseWriter, r *http.Request) {
		h, err := n.sync.Handover(r.Context(), 1)
		if err != nil {
			refuse(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(h)
	})
	mux.HandleFunc("POST /api/node/handover/claim", func(w http.ResponseWriter, r *http.Request) {
		if n.claimFails.Load() {
			refuse(w, errString("the claim was refused"))
			return
		}
		var c handover.Claim
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			refuse(w, err)
			return
		}
		if err := n.sync.Claim(r.Context(), 1, c); err != nil {
			refuse(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/node/sync", func(w http.ResponseWriter, r *http.Request) {
		var st fleet.NodeState
		if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
			refuse(w, err)
			return
		}
		if err := n.sync.Apply(r.Context(), 1, st); err != nil {
			refuse(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
	})
	mux.HandleFunc("POST /api/node/usage", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Keep *[]uint `json:"keep"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Keep != nil {
			if _, err := n.sync.Prune(r.Context(), 1, *in.Keep); err != nil {
				refuse(w, err)
				return
			}
		}
		u, d, err := n.sync.Drain(r.Context())
		if err != nil {
			refuse(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"usage": u, "devices": d})
	})
	n.srv = httptest.NewServer(mux)
	t.Cleanup(n.srv.Close)
	return n
}

type errString string

func (e errString) Error() string { return string(e) }

// seed gives the node tunnels and customers of its own:
//
//	wg0 (WireGuard):   alice (two devices), bob
//	ovpn1 (OpenVPN):   alice, carol      -- alice ties wg0 and ovpn1
//	wg9 (WireGuard):   dave              -- on his own
//	wg8 (WireGuard):   eve, who also has a file on an Xray inbound
func (n *wuiNode) seed(t *testing.T) map[string]uint {
	t.Helper()
	mk := func(i *model.Interface) {
		if err := n.db.Create(i).Error; err != nil {
			t.Fatal(err)
		}
	}
	wg0 := &model.Interface{NodeID: 1, Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51820,
		Subnet: "10.70.0.0/24", EndpointHost: "node.example.com", MTU: 1420, PrivateKey: "wg0-priv", PublicKey: "wg0-pub"}
	ovpn := &model.Interface{NodeID: 1, Name: "ovpn1", Protocol: model.ProtocolOpenVPN, Enabled: true, ListenPort: 1194,
		Subnet: "10.71.0.0/24", EndpointHost: "node.example.com", MTU: 1500,
		OpenVPN: model.JSON(model.OpenVPNParams{CACert: "ca", TLSCryptKey: "tc", ServerCert: "sc", ServerKey: "sk"})}
	wg9 := &model.Interface{NodeID: 1, Name: "wg9", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51829,
		Subnet: "10.79.0.0/24", EndpointHost: "node.example.com", MTU: 1420, PrivateKey: "wg9-priv", PublicKey: "wg9-pub"}
	wg8 := &model.Interface{NodeID: 1, Name: "wg8", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51828,
		Subnet: "10.78.0.0/24", EndpointHost: "node.example.com", MTU: 1420, PrivateKey: "wg8-priv", PublicKey: "wg8-pub"}
	x1 := &model.Interface{NodeID: 1, Name: "x1", Protocol: model.ProtocolXray, Enabled: true, ListenPort: 443,
		Subnet: "10.90.0.0/24", EndpointHost: "node.example.com"}
	for _, i := range []*model.Interface{wg0, ovpn, wg9, wg8, x1} {
		mk(i)
	}
	ids := map[string]uint{}
	client := func(name string, c model.Client) uint {
		c.Name, c.Status, c.SubToken = name, model.StatusActive, "sub-"+name+"-token"
		if err := n.db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		ids[name] = c.ID
		return c.ID
	}
	acc := func(cid uint, iface *model.Interface, dev, ip string, extra model.Account) {
		extra.ClientID, extra.InterfaceID, extra.NodeID, extra.DeviceName, extra.IP, extra.Enabled = cid, iface.ID, 1, dev, ip, true
		if err := n.db.Create(&extra).Error; err != nil {
			t.Fatal(err)
		}
		ids[dev+"@"+iface.Name] = extra.ID
	}
	alice := client("alice", model.Client{QuotaBytes: 50 << 30, UsedBytes: 1000, UpBytes: 400, DownBytes: 600, DeviceLimit: 2})
	bob := client("bob", model.Client{QuotaBytes: 10 << 30, DeviceLimit: 1, TelegramID: 77})
	carol := client("carol", model.Client{DeviceLimit: 1, StartOnFirstUse: true, DurationDays: 30})
	dave := client("dave", model.Client{DeviceLimit: 1})
	eve := client("eve", model.Client{DeviceLimit: 1})
	if err := n.db.Create(&model.ClientGroup{ClientID: bob, Name: "vip"}).Error; err != nil {
		t.Fatal(err)
	}
	acc(alice, wg0, "phone", "10.70.0.2", model.Account{PrivateKey: "a1-priv", PublicKey: "a1-pub", PresharedKey: "a1-psk", UpBytes: 300, DownBytes: 500})
	acc(alice, wg0, "laptop", "10.70.0.3", model.Account{PrivateKey: "a2-priv", PublicKey: "a2-pub", PresharedKey: "a2-psk", UpBytes: 100, DownBytes: 100})
	acc(alice, ovpn, "phone", "10.71.0.2", model.Account{Username: "s1-phone", Secret: "alice-pass"})
	acc(bob, wg0, "bob-phone", "10.70.0.4", model.Account{PrivateKey: "b-priv", PublicKey: "b-pub"})
	acc(carol, ovpn, "phone", "10.71.0.3", model.Account{Username: "s3-phone", Secret: "carol-pass"})
	acc(dave, wg9, "phone", "10.79.0.2", model.Account{PrivateKey: "d-priv", PublicKey: "d-pub"})
	acc(eve, wg8, "phone", "10.78.0.2", model.Account{PrivateKey: "e-priv", PublicKey: "e-pub"})
	acc(eve, x1, "phone", "10.90.0.2", model.Account{Secret: "uuid"})
	return ids
}

func usersByName(c []service.ImportCandidate) map[string]service.ImportCandidate {
	out := map[string]service.ImportCandidate{}
	for _, x := range c {
		out[x.Name] = x
	}
	return out
}

// A W-UI node's own customers are brought in with the tunnels they are on,
// made here exactly as they are there; the node hands its rows over to the
// sync, which then finds them where they are, and nothing is charged twice.
func TestAWUINodesOwnCustomersAreHandedOver(t *testing.T) {
	n := newWUINode(t)
	ids := n.seed(t)
	r := newRig(t, model.Node{Kind: model.KindRemote, Address: n.srv.URL, Token: "t", Enabled: true})

	cands, err := r.clients.NodeUsers(r.ctx, r.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	by := usersByName(cands)
	if len(by) != 5 {
		t.Fatalf("candidates = %v, want alice, bob, carol, dave and eve", cands)
	}
	if got := by["alice"].Inbounds; len(got) != 2 {
		t.Fatalf("alice is on %v, want wg0 and ovpn1", got)
	}
	if by["eve"].Reason == "" {
		t.Fatal("eve, with a file on an Xray inbound, was offered as one that can be brought in")
	}
	if by["carol"].DurationDays != 30 || by["alice"].UsedBytes != 1000 {
		t.Fatalf("the plans did not come over: carol %+v, alice %+v", by["carol"], by["alice"])
	}

	// Bob alone is asked for; alice is on his tunnel and on carol's, so all
	// three come, with both tunnels.
	res, err := r.clients.ImportNodeUsers(r.ctx, r.node.ID, []string{by["bob"].Key})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range res {
		if x.Error != "" {
			t.Fatalf("%s: %s", x.Name, x.Error)
		}
		names = append(names, x.Name)
	}
	sort.Strings(names)
	if len(names) != 3 || names[0] != "alice" || names[1] != "bob" || names[2] != "carol" {
		t.Fatalf("brought in %v, want alice, bob and carol", names)
	}

	// Here: the same tunnels, files, keys and addresses.
	var here []model.Interface
	r.db.Where("node_id = ?", r.node.ID).Order("name").Find(&here)
	if len(here) != 2 || here[0].Name != "ovpn1" || here[1].Name != "wg0" || here[1].PrivateKey != "wg0-priv" ||
		here[0].OpenVPN.V.TLSCryptKey != "tc" || here[0].Managed {
		t.Fatalf("tunnels here = %+v", here)
	}
	var aliceHere model.Client
	r.db.Where("name = ?", "alice").First(&aliceHere)
	if aliceHere.UsedBytes != 1000 || aliceHere.QuotaBytes != 50<<30 || aliceHere.SubToken != "sub-alice-token" {
		t.Fatalf("alice here = %+v", aliceHere)
	}
	var phone model.Account
	r.db.Where("client_id = ? AND public_key = ?", aliceHere.ID, "a1-pub").First(&phone)
	if phone.IP != "10.70.0.2" || phone.PrivateKey != "a1-priv" || phone.PresharedKey != "a1-psk" || phone.UpBytes != 300 {
		t.Fatalf("alice's phone here = %+v", phone)
	}
	var bobHere model.Client
	r.db.Where("name = ?", "bob").First(&bobHere)
	var groups []model.ClientGroup
	r.db.Where("client_id = ?", bobHere.ID).Find(&groups)
	if bobHere.TelegramID != 77 || len(groups) != 1 || groups[0].Name != "vip" {
		t.Fatalf("bob here = %+v, groups %v", bobHere, groups)
	}

	// There: the same rows, now managed under this panel's ids, the plan and
	// the counters handed over gone from them.
	var nodeAlice model.Client
	n.db.First(&nodeAlice, ids["alice"])
	if nodeAlice.OriginID != aliceHere.ID || nodeAlice.UsedBytes != 0 || nodeAlice.QuotaBytes != 0 {
		t.Fatalf("alice on the node = %+v", nodeAlice)
	}
	var nodeWG0 model.Interface
	n.db.Where("name = ?", "wg0").First(&nodeWG0)
	if !nodeWG0.Managed || nodeWG0.OriginID != here[1].ID {
		t.Fatalf("wg0 on the node = %+v", nodeWG0)
	}

	// A round: every tunnel and file is found where it is, none made twice,
	// and nothing that was handed over is charged again.
	r.round()
	var count int64
	n.db.Model(&model.Interface{}).Count(&count)
	if count != 5 {
		t.Fatalf("the node has %d tunnels after a round, want the same 5", count)
	}
	n.db.Model(&model.Account{}).Count(&count)
	if count != 8 {
		t.Fatalf("the node has %d files after a round, want the same 8", count)
	}
	var nodePhone model.Account
	n.db.First(&nodePhone, ids["phone@wg0"])
	if nodePhone.IP != "10.70.0.2" || nodePhone.PublicKey != "a1-pub" || nodePhone.OriginID != phone.ID {
		t.Fatalf("alice's phone on the node after a round = %+v", nodePhone)
	}
	for id, b := range r.charged {
		if b != 0 {
			t.Fatalf("customer %d was charged %d bytes for what was handed over", id, b)
		}
	}

	// Spent on the node from now on is this panel's to count.
	n.db.Model(&model.Client{}).Where("id = ?", ids["alice"]).Update("used_bytes", 2048)
	r.round()
	if r.charged[aliceHere.ID] != 2048 {
		t.Fatalf("alice was charged %d, want 2048", r.charged[aliceHere.ID])
	}

	// What is left to bring in is what the node still has of its own.
	cands, err = r.clients.NodeUsers(r.ctx, r.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	by = usersByName(cands)
	if len(by) != 2 || by["dave"].Key == "" || by["eve"].Key == "" {
		t.Fatalf("left on the node = %v, want dave and eve", cands)
	}

	// Eve is refused, with why.
	res, err = r.clients.ImportNodeUsers(r.ctx, r.node.ID, []string{by["eve"].Key})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Error == "" {
		t.Fatalf("eve = %+v, want refused", res)
	}

	// A node that refuses the claim keeps dave, and nothing of him is left here.
	n.claimFails.Store(true)
	if _, err := r.clients.ImportNodeUsers(r.ctx, r.node.ID, nil); err == nil {
		t.Fatal("a refused claim was reported as done")
	}
	r.db.Model(&model.Interface{}).Where("node_id = ?", r.node.ID).Count(&count)
	if count != 2 {
		t.Fatalf("%d tunnels here after a refused claim, want 2", count)
	}
	r.db.Model(&model.Client{}).Where("name = ?", "dave").Count(&count)
	if count != 0 {
		t.Fatal("dave was left here after the node refused to hand him over")
	}

	// Asked again, he comes.
	n.claimFails.Store(false)
	res, err = r.clients.ImportNodeUsers(r.ctx, r.node.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	brought := 0
	for _, x := range res {
		if x.Error == "" {
			brought++
		}
	}
	if brought != 1 {
		t.Fatalf("brought in %+v, want dave", res)
	}
	r.round()
}

// A node asked to claim a tunnel without every customer on it refuses: the
// next sync would cut off whoever was left out.
func TestAHandoverLeavingSomeoneOutIsRefused(t *testing.T) {
	n := newWUINode(t)
	ids := n.seed(t)
	var wg0 model.Interface
	n.db.Where("name = ?", "wg0").First(&wg0)
	err := n.sync.Claim(t.Context(), 1, handover.Claim{
		Tunnels:  []handover.IDPair{{LocalID: wg0.ID, OriginID: 100}},
		Clients:  []handover.IDPair{{LocalID: ids["bob"], OriginID: 200}},
		Accounts: []handover.IDPair{{LocalID: ids["bob-phone@wg0"], OriginID: 300}},
	})
	if err == nil {
		t.Fatal("a claim leaving alice out of wg0 was taken")
	}
	n.db.First(&wg0, wg0.ID)
	if wg0.Managed {
		t.Fatal("wg0 was handed over by a refused claim")
	}
}
