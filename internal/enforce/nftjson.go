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

// drainedUsage folds the per-file, per-direction counters into one row per
// client, carrying what each file carried.
//
// The kernel keeps upload and download, and one device from another, apart
// because only it can tell them apart. A client's total is the sum of its
// files' counters, so what the files carried and what the client used are the
// same bytes counted once. A client with only some of its counters present --
// a half-applied ruleset, or a rebuild caught mid-flight -- still contributes
// what it has rather than being skipped.
//
// A counter with no file in its name is a whole client's, as a panel before
// per-file counting wrote them: in the kernel for the first tick after an
// update, its bytes are the client's and no file's.
func drainedUsage(raw []byte) ([]Usage, error) {
	var doc nftOutput
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode nft json: %w", err)
	}

	byKey := map[string]*Usage{}
	fileAt := map[string]map[uint]int{} // key -> account -> index in Files
	order := make([]string, 0, len(doc.Nftables))

	for _, e := range doc.Nftables {
		if e.Counter == nil || e.Counter.Table != TableName {
			continue
		}
		key, account, down, ok := parseCounter(e.Counter.Name)
		if !ok {
			continue // not one of ours
		}
		n := e.Counter.Bytes

		u, seen := byKey[key]
		if !seen {
			u = &Usage{Key: key}
			byKey[key] = u
			order = append(order, key)
		}
		if down {
			u.Down += n
		} else {
			u.Up += n
		}
		u.Bytes += n

		if account == 0 {
			continue
		}
		if fileAt[key] == nil {
			fileAt[key] = map[uint]int{}
		}
		i, have := fileAt[key][account]
		if !have {
			i = len(u.Files)
			fileAt[key][account] = i
			u.Files = append(u.Files, FileUsage{Account: account})
		}
		if down {
			u.Files[i].Down += n
		} else {
			u.Files[i].Up += n
		}
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
