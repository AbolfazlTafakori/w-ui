package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// This panel's outbound, for its customers on a 3x-ui node.
//
// A customer on one of this panel's own servers leaves by the outbound at the
// top of the list. A customer on a 3x-ui node is carried by that panel's
// xray, which knows nothing of the list; so the outbound is put in that xray
// as one of its own, tagged wui-out- and this panel's tag, with a rule that
// sends this panel's clients into it and nobody else's: the ones it made, by
// the pattern of their emails (wui-<tag>-<number>), and the ones brought in
// from that panel, by name. The 3x-ui operator's own users leave as they
// always did, and so do another W-UI panel's on the same node, which has a
// tag, an outbound and a rule of its own. The pattern is written once, not
// again for every customer; the rule changes only when the outbound does or a
// user is brought in, and the outbound at the top being direct takes both
// away.
//
// Nothing of that panel's is left broken: the config is changed, xray is
// restarted, and if it does not come back running the config as it was is
// put back.

// xuiEgressLegacyTag is the outbound a panel put there before panels had
// tags. It was this panel's, and is taken away.
const xuiEgressLegacyTag = "wui-out"

// xuiEgressRoute is this panel's outbound on one node: its tag there, and
// the users its rule sends into it.
type xuiEgressRoute struct {
	tag   string
	users []string
}

// egressRoute is the route for this panel's tag and the users brought in
// from that panel.
func egressRoute(panelTag string, adopted []string) xuiEgressRoute {
	brought := append([]string(nil), adopted...)
	// The same list every round, so the config is not rewritten for nothing.
	sort.Strings(brought)
	users := append([]string{"regexp:^wui-" + regexp.QuoteMeta(panelTag) + "-[0-9]+$"}, brought...)
	return xuiEgressRoute{tag: "wui-out-" + panelTag, users: users}
}

// egressFor is the outbound customers on an xray node leave by, as an xray
// outbound without its tag: nil for direct, and an error for a kind xray
// cannot carry.
func egressFor(ctx context.Context, db *gorm.DB) (map[string]any, string, error) {
	var ob model.Outbound
	if err := db.WithContext(ctx).Order("position, id").Limit(1).Find(&ob).Error; err != nil {
		return nil, "", err
	}
	if ob.ID == 0 || ob.Kind == model.OutboundDirect {
		return nil, "direct", nil
	}
	if !ob.Enabled || ob.Kind == model.OutboundBlock {
		// Off, or blocked: nothing leaves, as on this panel's own servers.
		return map[string]any{"protocol": "blackhole"}, ob.Tag, nil
	}
	var out map[string]any
	switch ob.Kind {
	case model.OutboundWireGuard:
		peer := map[string]any{
			"publicKey":  ob.PeerPubKey,
			"endpoint":   ob.Address,
			"allowedIPs": []string{"0.0.0.0/0", "::/0"},
		}
		if ob.PresharedKey != "" {
			peer["preSharedKey"] = ob.PresharedKey
		}
		if ob.Keepalive > 0 {
			peer["keepAlive"] = ob.Keepalive
		}
		settings := map[string]any{"secretKey": ob.PrivateKey, "peers": []any{peer}}
		if ob.HopAddress != "" {
			var addrs []string
			for _, a := range strings.Split(ob.HopAddress, ",") {
				if a = strings.TrimSpace(a); a != "" {
					addrs = append(addrs, a)
				}
			}
			settings["address"] = addrs
		}
		if ob.HopMTU > 0 {
			settings["mtu"] = ob.HopMTU
		}
		out = map[string]any{"protocol": "wireguard", "settings": settings}
	case model.OutboundOpenVPN:
		return nil, ob.Tag, fmt.Errorf("the outbound at the top, %s, is OpenVPN, which xray cannot carry; its customers on this node leave from the node itself", ob.Tag)
	default:
		if err := json.Unmarshal([]byte(ob.Config), &out); err != nil || out == nil {
			return nil, ob.Tag, fmt.Errorf("the outbound %s could not be read for xray", ob.Tag)
		}
	}
	// Keys this panel adds for its own hops are not xray's.
	delete(out, "panelSkipVerify")
	if st, ok := out["streamSettings"].(map[string]any); ok {
		if ts, ok := st["tlsSettings"].(map[string]any); ok {
			// xray refuses allowInsecure now (see routing/hopinsecure.go);
			// a hop here pins instead, which a remote xray cannot be asked
			// to do for us, so the check is left to verify.
			delete(ts, "allowInsecure")
		}
	}
	return out, ob.Tag, nil
}

// xrayConfigPath is where the panel keeps its xray config: under the API
// from 3.3, and before it under /panel, reached signed in.
func (x *xuiAPI) xrayConfigPath() (string, error) {
	if x.version != "" && !service.VersionBelow(x.version, 3, 3) {
		return x.base + "xray/", nil
	}
	if !x.session {
		return "", errors.New("before 3.3 its xray settings are reached signed in: give it the panel's username and password too, for the outbound")
	}
	return x.root + "panel/xray/", nil
}

// xrayTemplate is the xray config a 3x-ui panel runs, as JSON.
func (x *xuiAPI) xrayTemplate(ctx context.Context) (map[string]any, error) {
	path, err := x.xrayConfigPath()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := x.call(ctx, http.MethodPost, path, nil, &raw); err != nil {
		return nil, err
	}
	var wrap struct {
		XraySetting json.RawMessage `json:"xraySetting"`
	}
	if err := objectOrString(raw, &wrap); err != nil {
		return nil, fmt.Errorf("its xray settings could not be read: %w", err)
	}
	var cfg map[string]any
	if err := objectOrString(wrap.XraySetting, &cfg); err != nil || cfg == nil {
		return nil, errors.New("its xray settings could not be read")
	}
	return cfg, nil
}

func (x *xuiAPI) saveXrayTemplate(ctx context.Context, cfg map[string]any) error {
	path, err := x.xrayConfigPath()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := x.call(ctx, http.MethodPost, path+"update", url.Values{"xraySetting": {string(raw)}}, nil); err != nil {
		return fmt.Errorf("it refused the xray settings: %w", err)
	}
	return x.restartXray(ctx)
}

// withEgress is cfg with this panel's outbound and rule as they should be:
// added or replaced when there is an outbound, taken away when there is none.
// The rest of the config is the 3x-ui operator's, or another panel's, and is
// left as it is.
func withEgress(cfg map[string]any, egress map[string]any, route xuiEgressRoute) map[string]any {
	ours := func(tag any) bool { return tag == route.tag || tag == xuiEgressLegacyTag }
	out := map[string]any{}
	for k, v := range cfg {
		out[k] = v
	}
	var obs []any
	if list, ok := cfg["outbounds"].([]any); ok {
		for _, o := range list {
			if m, ok := o.(map[string]any); ok && ours(m["tag"]) {
				continue
			}
			obs = append(obs, o)
		}
	}
	routing, _ := cfg["routing"].(map[string]any)
	r := map[string]any{}
	for k, v := range routing {
		r[k] = v
	}
	var rules []any
	if list, ok := r["rules"].([]any); ok {
		for _, rule := range list {
			if m, ok := rule.(map[string]any); ok && ours(m["outboundTag"]) {
				continue
			}
			rules = append(rules, rule)
		}
	}
	if egress != nil {
		ob := map[string]any{}
		for k, v := range egress {
			ob[k] = v
		}
		ob["tag"] = route.tag
		obs = append(obs, ob)
		users := make([]any, len(route.users))
		for i, u := range route.users {
			users[i] = u
		}
		rule := map[string]any{"type": "field", "user": users, "outboundTag": route.tag}
		// After the panel's own api rule, which has to see its traffic
		// first, and before everything else.
		at := 0
		for i, existing := range rules {
			if m, ok := existing.(map[string]any); ok && m["outboundTag"] == "api" {
				at = i + 1
			}
		}
		rules = append(rules[:at], append([]any{rule}, rules[at:]...)...)
	}
	out["outbounds"] = obs
	r["rules"] = rules
	out["routing"] = r
	return out
}

// xuiEgress puts the outbound in a 3x-ui node's xray when it is not there as
// it should be, and checks xray came back. A config whose xray does not run
// is replaced by the one before.
func (s *Syncer) xuiEgress(ctx context.Context, api *xuiAPI, node model.Node, route xuiEgressRoute) error {
	egress, tag, egressErr := egressFor(ctx, s.db)
	if egressErr != nil {
		egress = nil // left to leave directly, and said so below
	}
	cfg, err := api.xrayTemplate(ctx)
	if err != nil {
		if egress == nil {
			// Nothing to put there; a panel whose xray settings this one
			// cannot reach (3.0 to 3.2 by token alone) is no fault then.
			return egressErr
		}
		return fmt.Errorf("reading its xray settings: %w", err)
	}
	next := withEgress(cfg, egress, route)
	if reflect.DeepEqual(normalJSON(next), normalJSON(cfg)) {
		return egressErr
	}
	if err := api.saveXrayTemplate(ctx, next); err != nil {
		return fmt.Errorf("putting the outbound in its xray: %w", err)
	}
	// Restarting takes a moment; it is given a few to come back.
	var st xuiStatus
	for i := 0; i < 10; i++ {
		time.Sleep(xuiSettle)
		if st, err = api.status(ctx); err == nil && st.Xray.State == "running" {
			s.log.Info("the outbound is in a 3x-ui node's xray", "node", node.Name, "outbound", tag)
			return egressErr
		}
	}
	// Put back as it was: that panel's own users must not lose their service
	// because of ours.
	if rerr := api.saveXrayTemplate(ctx, cfg); rerr != nil {
		s.log.Error("a 3x-ui node's xray stopped with this panel's outbound, and could not be put back", "node", node.Name, "error", rerr)
	}
	return fmt.Errorf("its xray did not run with the outbound %s (%s); its settings were put back as they were", tag, st.Xray.Error)
}

// xuiSettle is how long a 3x-ui panel's xray is given between looks after a
// restart; a variable so tests need not wait.
var xuiSettle = time.Second

// normalJSON is v through JSON, so a config read back compares equal to the
// same config built here.
func normalJSON(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}
