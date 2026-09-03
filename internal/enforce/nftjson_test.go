package enforce

import "testing"

// Two counters per customer have to come back as one row carrying both
// directions and their sum. Getting this wrong is not visible in the panel —
// the total would still look right — but every customer's upload and download
// figure, and the Subscription-Userinfo header their own app reads, would be
// silently wrong.
func TestDrainedUsageFoldsBothDirections(t *testing.T) {
	raw := []byte(`{"nftables":[
	  {"metainfo":{"version":"1.1.6"}},
	  {"counter":{"family":"inet","name":"nd_c1","table":"wui","handle":1,"packets":10,"bytes":900}},
	  {"counter":{"family":"inet","name":"nu_c1","table":"wui","handle":2,"packets":4,"bytes":100}},
	  {"counter":{"family":"inet","name":"nd_c2","table":"wui","handle":3,"packets":1,"bytes":50}}
	]}`)

	got, err := drainedUsage(raw)
	if err != nil {
		t.Fatalf("drainedUsage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want one per customer: %+v", len(got), got)
	}

	byKey := map[string]Usage{}
	for _, u := range got {
		byKey[u.Key] = u
	}

	c1 := byKey["c1"]
	if c1.Down != 900 || c1.Up != 100 {
		t.Errorf("c1 split = down %d / up %d, want 900 / 100", c1.Down, c1.Up)
	}
	if c1.Bytes != 1000 {
		t.Errorf("c1 total = %d, want 1000 — the allowance is spent by the sum", c1.Bytes)
	}

	// One direction present is not a reason to drop the customer's bytes on the
	// floor: a ruleset caught mid-rebuild can show exactly that.
	c2 := byKey["c2"]
	if c2.Down != 50 || c2.Up != 0 || c2.Bytes != 50 {
		t.Errorf("c2 = %+v, want down 50, up 0, total 50", c2)
	}
}

func TestDrainedUsageIgnoresWhatIsNotOurs(t *testing.T) {
	raw := []byte(`{"nftables":[
	  {"counter":{"family":"inet","name":"nd_c1","table":"other","bytes":900}},
	  {"counter":{"family":"inet","name":"somebody_else","table":"wui","bytes":900}},
	  {"counter":{"family":"inet","name":"nd_notakey","table":"wui","bytes":900}},
	  {"quota":{"family":"inet","name":"q_c1","table":"wui","used":900}},
	  {"counter":{"family":"inet","name":"nd_c1","table":"wui","bytes":7}}
	]}`)

	got, err := drainedUsage(raw)
	if err != nil {
		t.Fatalf("drainedUsage: %v", err)
	}
	// A counter in another table, one we did not name, one whose key is not a
	// key, and a quota object rather than a counter: none of them are usage.
	if len(got) != 1 || got[0].Key != "c1" || got[0].Bytes != 7 {
		t.Fatalf("got %+v, want exactly one row for c1 with 7 bytes", got)
	}
}

func TestDrainedUsageOnEmptyOutput(t *testing.T) {
	// A server with no customers drains an empty list, every tick. It must not
	// be an error, or the tick that would have applied the first ruleset never
	// runs.
	got, err := drainedUsage([]byte(`{"nftables":[]}`))
	if err != nil {
		t.Fatalf("drainedUsage: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want nothing", got)
	}
	if _, err := drainedUsage([]byte(`not json`)); err == nil {
		t.Error("malformed output should be an error, not silently zero usage")
	}
}
