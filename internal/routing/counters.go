package routing

import (
	"encoding/json"
	"fmt"
)

// nftCounter is the shape `nft -j list counters` emits.
type nftCounter struct {
	Counter struct {
		Family string `json:"family"`
		Name   string `json:"name"`
		Table  string `json:"table"`
		Bytes  uint64 `json:"bytes"`
	} `json:"counter"`
}

type nftOutput struct {
	Nftables []json.RawMessage `json:"nftables"`
}

// parseCounters turns nft's JSON into per-outbound byte totals.
//
// Counters belonging to another table are skipped rather than merged: this
// panel is not the only thing that may be using nftables on the machine, and a
// counter with a similar name somewhere else is not ours to read.
func parseCounters(raw []byte) (map[uint32]uint64, error) {
	var out nftOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("routing: read counters: %w", err)
	}

	totals := map[uint32]uint64{}
	for _, item := range out.Nftables {
		var c nftCounter
		if err := json.Unmarshal(item, &c); err != nil {
			continue // metadata entries and other object kinds
		}
		if c.Counter.Name == "" || c.Counter.Table != TableName {
			continue
		}
		var mark uint32
		if _, err := fmt.Sscanf(c.Counter.Name, "ob_%08x", &mark); err != nil {
			continue
		}
		if !OwnsMark(mark) {
			continue
		}
		totals[mark] = c.Counter.Bytes
	}
	return totals, nil
}
