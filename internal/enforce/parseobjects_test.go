package enforce

import "testing"

// Quotas are read from our own table, by our own prefix; anything else nft
// lists -- another table's, a name without the prefix, a key that is not one
// of ours, a counter where a quota was asked for -- is left out.
func TestParseObjectsKeepsOnlyOurs(t *testing.T) {
	raw := []byte(`{"nftables":[
		{"metainfo":{"version":"1.0.9"}},
		{"quota":{"family":"inet","name":"q_c1","table":"` + TableName + `","used":1000}},
		{"quota":{"family":"inet","name":"q_c2","table":"` + TableName + `","used":5}},
		{"quota":{"family":"inet","name":"q_c3","table":"other","used":9}},
		{"quota":{"family":"inet","name":"c4","table":"` + TableName + `","used":9}},
		{"quota":{"family":"inet","name":"q_cx","table":"` + TableName + `","used":9}},
		{"counter":{"family":"inet","name":"q_c5","table":"` + TableName + `","bytes":9}}
	]}`)
	got, err := parseObjects(raw, func(e struct {
		Counter *nftObject `json:"counter"`
		Quota   *nftObject `json:"quota"`
	}) (*nftObject, uint64, bool) {
		if e.Quota == nil {
			return nil, 0, false
		}
		return e.Quota, e.Quota.Used, true
	}, "q_")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{"c1": 1000, "c2": 5}
	if len(got) != len(want) {
		t.Fatalf("parseObjects = %+v, want %v", got, want)
	}
	for _, u := range got {
		if want[u.Key] != u.Bytes {
			t.Errorf("%s = %d, want %d", u.Key, u.Bytes, want[u.Key])
		}
	}
}
