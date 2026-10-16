package nodes

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

func TestWithEgress(t *testing.T) {
	var cfg map[string]any
	_ = json.Unmarshal([]byte(`{"log":{"loglevel":"warning"},
		"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"blocked","protocol":"blackhole"},
			{"tag":"wui-out-0b0b0b","protocol":"vless"}],
		"routing":{"domainStrategy":"AsIs","rules":[
			{"type":"field","inboundTag":["api"],"outboundTag":"api"},
			{"type":"field","user":["regexp:^wui-0b0b0b-[0-9]+$"],"outboundTag":"wui-out-0b0b0b"},
			{"type":"field","ip":["geoip:private"],"outboundTag":"blocked"}]}}`), &cfg)
	route := egressRoute("a1b2c3", []string{"zed", "alice"})
	egress := map[string]any{"protocol": "vless"}

	with := withEgress(cfg, egress, route)
	rules := with["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 4 || rules[1].(map[string]any)["outboundTag"] != route.tag {
		t.Fatalf("rules %v", rules)
	}
	if rules[2].(map[string]any)["outboundTag"] != "wui-out-0b0b0b" || rules[3].(map[string]any)["outboundTag"] != "blocked" {
		t.Fatal("another panel's rule, or the operator's own, was lost")
	}
	if u := rules[1].(map[string]any)["user"].([]any); len(u) != 3 || u[1] != "alice" || u[2] != "zed" {
		t.Fatalf("the rule matches %v; want the pattern, then the users brought in, in order", u)
	}
	obs := with["outbounds"].([]any)
	if len(obs) != 4 || obs[0].(map[string]any)["tag"] != "direct" || obs[3].(map[string]any)["tag"] != route.tag {
		t.Fatalf("outbounds %v: the first, the panel's own default, must stay first", obs)
	}
	if with["log"] == nil || with["routing"].(map[string]any)["domainStrategy"] != "AsIs" {
		t.Fatal("the rest of the config was not kept")
	}

	// Again: the same, not a second copy.
	if again := withEgress(with, egress, route); !reflect.DeepEqual(normalJSON(again), normalJSON(with)) {
		t.Fatal("applying twice changed it")
	}
	// Direct: ours goes, the rest stays exactly as it was.
	if back := withEgress(with, nil, route); !reflect.DeepEqual(normalJSON(back), normalJSON(cfg)) {
		t.Fatalf("taking it away left %v", normalJSON(back))
	}
}

// The rule is what sends customers out, so it is tested against the names
// clients are really given, not against itself: this panel's match, another
// panel's and the operator's own do not.
func TestTheEgressRuleMatchesThisPanelsClientsAndNoOneElse(t *testing.T) {
	route := egressRoute("a1b2c3", nil)
	pattern := regexp.MustCompile(strings.TrimPrefix(route.users[0], "regexp:"))
	acc := &model.Account{ID: 42}
	for name, want := range map[string]bool{
		service.XUIEmail("a1b2c3", acc): true,
		service.XUIEmail("ffffff", acc): false, // another W-UI panel's
		"wui-42":                        false, // before panels had tags
		"alice":                         false,
		"wui-a1b2c3-42x":                false,
	} {
		if got := pattern.MatchString(name); got != want {
			t.Errorf("%q matched %v, want %v", name, got, want)
		}
	}
}

// The outbound an older panel left, untagged, is taken away with ours.
func TestTheUntaggedOutboundIsTakenAway(t *testing.T) {
	cfg := map[string]any{
		"outbounds": []any{map[string]any{"tag": "direct"}, map[string]any{"tag": xuiEgressLegacyTag}},
		"routing":   map[string]any{"rules": []any{map[string]any{"user": []any{"regexp:^wui-[0-9]+$"}, "outboundTag": xuiEgressLegacyTag}}},
	}
	got := normalJSON(withEgress(cfg, nil, egressRoute("a1b2c3", nil))).(map[string]any)
	if obs := got["outbounds"].([]any); len(obs) != 1 {
		t.Fatalf("outbounds %v", obs)
	}
	if rules, _ := got["routing"].(map[string]any)["rules"].([]any); len(rules) != 0 {
		t.Fatalf("rules %v", rules)
	}
}
