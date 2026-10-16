package nodes

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWithEgress(t *testing.T) {
	var cfg map[string]any
	_ = json.Unmarshal([]byte(`{"log":{"loglevel":"warning"},
		"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"blocked","protocol":"blackhole"}],
		"routing":{"domainStrategy":"AsIs","rules":[
			{"type":"field","inboundTag":["api"],"outboundTag":"api"},
			{"type":"field","ip":["geoip:private"],"outboundTag":"blocked"}]}}`), &cfg)
	egress := map[string]any{"tag": xuiEgressTag, "protocol": "vless"}

	with := withEgress(cfg, egress)
	rules := with["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 3 || rules[1].(map[string]any)["outboundTag"] != xuiEgressTag {
		t.Fatalf("rules %v", rules)
	}
	if rules[2].(map[string]any)["outboundTag"] != "blocked" {
		t.Fatal("the operator's own rule was lost")
	}
	if u := rules[1].(map[string]any)["user"].([]any); len(u) != 1 || u[0] != xuiEgressRule {
		t.Fatalf("the rule matches %v", u)
	}
	obs := with["outbounds"].([]any)
	if len(obs) != 3 || obs[0].(map[string]any)["tag"] != "direct" {
		t.Fatalf("outbounds %v: the first, the panel's own default, must stay first", obs)
	}
	if with["log"] == nil || with["routing"].(map[string]any)["domainStrategy"] != "AsIs" {
		t.Fatal("the rest of the config was not kept")
	}

	// Again: the same, not a second copy.
	if again := withEgress(with, egress); !reflect.DeepEqual(normalJSON(again), normalJSON(with)) {
		t.Fatal("applying twice changed it")
	}
	// Direct: ours goes, theirs stays exactly as it was.
	if back := withEgress(with, nil); !reflect.DeepEqual(normalJSON(back), normalJSON(cfg)) {
		t.Fatalf("taking it away left %v", normalJSON(back))
	}
}
