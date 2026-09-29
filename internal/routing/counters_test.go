package routing

import "testing"

// Each outbound's bytes are read from our own counters, by mark. A counter in
// another program's table -- even one named like ours -- is not read, nor is
// a mark outside our range.
func TestCountersAreReadOnlyFromOurTable(t *testing.T) {
	raw := []byte(`{"nftables":[
		{"metainfo":{"version":"1.0.9"}},
		{"counter":{"family":"inet","name":"ob_00a7000b","table":"wui_policy","bytes":1500}},
		{"counter":{"family":"inet","name":"ob_00a7000c","table":"wui_policy","bytes":42}},
		{"counter":{"family":"inet","name":"ob_00a7000d","table":"someone_else","bytes":999}},
		{"counter":{"family":"inet","name":"ob_00000001","table":"wui_policy","bytes":7}},
		{"counter":{"family":"inet","name":"not-a-mark","table":"wui_policy","bytes":7}}
	]}`)
	got, err := parseCounters(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]uint64{0xa7000b: 1500, 0xa7000c: 42}
	if len(got) != len(want) {
		t.Fatalf("parseCounters = %v, want %v", got, want)
	}
	for m, b := range want {
		if got[m] != b {
			t.Errorf("mark %#x carried %d, want %d", m, got[m], b)
		}
	}
	if _, err := parseCounters([]byte("not json")); err == nil {
		t.Error("output that is not nft's was read without complaint")
	}
}
