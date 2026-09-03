package enforce

// Reading what `nft -j` says, and turning it into usage.
//
// Deliberately not in the Linux-only file next door. Nothing about parsing
// JSON needs a kernel, and while it lived there the one piece of real
// arithmetic in the enforcer -- folding two directional counters back into
// one row per customer -- could not be tested anywhere but on Linux.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// nftObject is the shape `nft -j` emits for a stateful object.
type nftObject struct {
	Family string `json:"family"`
	Name   string `json:"name"`
	Table  string `json:"table"`
	Bytes  uint64 `json:"bytes"`
	Used   uint64 `json:"used"`
}

type nftOutput struct {
	Nftables []struct {
		Counter *nftObject `json:"counter"`
		Quota   *nftObject `json:"quota"`
	} `json:"nftables"`
}

// drainedUsage folds the two directional counters back into one row per client.
//
// The kernel keeps upload and download apart because only it can tell them
// apart; everything above here wants both the split and the total, and the
// total is the sum. A client with only one direction's counter present -- a
// half-applied ruleset, or a rebuild caught mid-flight -- still contributes
// what it has rather than being skipped.
func drainedUsage(raw []byte) ([]Usage, error) {
	var doc nftOutput
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode nft json: %w", err)
	}

	byKey := map[string]*Usage{}
	order := make([]string, 0, len(doc.Nftables))

	for _, e := range doc.Nftables {
		if e.Counter == nil || e.Counter.Table != TableName {
			continue
		}
		var key string
		var down bool
		switch {
		case strings.HasPrefix(e.Counter.Name, "nd_"):
			key, down = strings.TrimPrefix(e.Counter.Name, "nd_"), true
		case strings.HasPrefix(e.Counter.Name, "nu_"):
			key = strings.TrimPrefix(e.Counter.Name, "nu_")
		default:
			continue
		}
		if !validKey(key) {
			continue // not one of ours
		}

		u, ok := byKey[key]
		if !ok {
			u = &Usage{Key: key}
			byKey[key] = u
			order = append(order, key)
		}
		if down {
			u.Down += e.Counter.Bytes
		} else {
			u.Up += e.Counter.Bytes
		}
		u.Bytes += e.Counter.Bytes
	}

	out := make([]Usage, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out, nil
}

func parseObjects(
	raw []byte,
	pick func(struct {
		Counter *nftObject `json:"counter"`
		Quota   *nftObject `json:"quota"`
	}) (*nftObject, uint64, bool),
	prefix string,
) ([]Usage, error) {
	var doc nftOutput
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode nft json: %w", err)
	}

	out := make([]Usage, 0, len(doc.Nftables))
	for _, e := range doc.Nftables {
		obj, value, ok := pick(e)
		if !ok || obj.Table != TableName {
			continue
		}
		key := strings.TrimPrefix(obj.Name, prefix)
		if key == obj.Name || !validKey(key) {
			continue // not one of ours
		}
		out = append(out, Usage{Key: key, Bytes: value})
	}
	return out, nil
}
