package service

import (
	"context"
	"fmt"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Putting a lot of customers onto a server, or taking them off it, at once.
//
// This is the operation the multi-server work makes necessary and did not
// supply. An operator who rents a new node has to give it to the customers who
// already exist, and the only way to do that was to open each customer and tick
// a box — three hundred times, without losing their place. The same in reverse
// when a server is given up: every customer has to come off it before it can be
// removed, and until then they are being handed a configuration for a machine
// that is going away.
//
// Adding is not the same as replacing. A customer keeps every server they had
// and gains this one, because the point of several servers is that one being
// blocked leaves the rest working — and a bulk action that quietly replaced
// their list would take that away from everybody at once.

// ServerBulkResult says what happened, per customer where it did not.
type ServerBulkResult struct {
	// Changed is how many customers ended up different.
	Changed int `json:"changed"`

	// Unchanged is how many already had it that way. Not a failure, and worth
	// separating: an operator who selected everybody wants to know the action
	// reached the ones it needed to.
	Unchanged int `json:"unchanged"`

	// Failures is why a customer could not be moved, by name. A device limit,
	// an address pool with nothing left — the kind of thing that stops one
	// customer and should not stop the rest.
	Failures map[string]string `json:"failures,omitempty"`
}

// AttachServers gives every selected customer the named tunnels, in addition to
// whatever they already have.
func (s *Clients) AttachServers(ctx context.Context, ids, interfaceIDs []uint) (*ServerBulkResult, error) {
	return s.moveServers(ctx, ids, interfaceIDs, true)
}

// DetachServers takes the named tunnels away from every selected customer.
func (s *Clients) DetachServers(ctx context.Context, ids, interfaceIDs []uint) (*ServerBulkResult, error) {
	return s.moveServers(ctx, ids, interfaceIDs, false)
}

func (s *Clients) moveServers(ctx context.Context, ids, interfaceIDs []uint, add bool) (*ServerBulkResult, error) {
	ids, interfaceIDs = dedupe(ids), dedupe(interfaceIDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no customers selected", ErrInvalid)
	}
	if len(interfaceIDs) == 0 {
		return nil, fmt.Errorf("%w: no servers chosen", ErrInvalid)
	}

	// Checked once rather than per customer: naming a tunnel that does not
	// exist is a mistake about the request, not about any one of them.
	if _, err := s.loadInterfaces(ctx, interfaceIDs); err != nil {
		return nil, err
	}

	var clients []model.Client
	if err := s.db.WithContext(ctx).Preload("Accounts").
		Where("id IN ?", ids).Find(&clients).Error; err != nil {
		return nil, fmt.Errorf("service: load customers: %w", err)
	}

	out := &ServerBulkResult{Failures: map[string]string{}}

	for i := range clients {
		c := clients[i]
		before := clientInterfaces(c.Accounts)
		want := combine(before, interfaceIDs, add)

		if sameSet(before, want) {
			out.Unchanged++
			continue
		}
		// Refused rather than silently ignored. A customer with no server at
		// all has a subscription that renders nothing, and finding that out
		// from the customer is the worst way to find it out.
		if len(want) == 0 {
			out.Failures[c.Name] = "this would leave them with no server at all"
			continue
		}

		if err := s.setInterfaces(ctx, &c, want); err != nil {
			// One customer's pool being full must not stop the rest being
			// moved; an operator doing this to three hundred people wants the
			// two hundred and ninety-nine.
			out.Failures[c.Name] = humanReason(err)
			continue
		}
		out.Changed++
	}

	if len(out.Failures) == 0 {
		out.Failures = nil
	}
	s.log.Info("customers moved between servers",
		"add", add, "servers", len(interfaceIDs),
		"changed", out.Changed, "unchanged", out.Unchanged, "failed", len(out.Failures))
	return out, nil
}

// combine works out the list a customer should end up with.
func combine(have, change []uint, add bool) []uint {
	in := map[uint]bool{}
	for _, id := range have {
		in[id] = true
	}
	for _, id := range change {
		if add {
			in[id] = true
		} else {
			delete(in, id)
		}
	}

	out := make([]uint, 0, len(in))
	// Built from the two inputs rather than by ranging the map, so the result
	// is in a stable order and two identical requests produce identical rows.
	for _, id := range have {
		if in[id] {
			out = append(out, id)
			in[id] = false
		}
	}
	for _, id := range change {
		if in[id] {
			out = append(out, id)
			in[id] = false
		}
	}
	return out
}

func sameSet(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[uint]int{}
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		seen[id]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// humanReason strips the package prefixes off an error so it can be shown
// beside a customer's name without reading like a stack trace.
func humanReason(err error) string {
	msg := err.Error()
	for _, prefix := range []string{"service: ", "ipam: ", "backend: "} {
		if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
			msg = msg[len(prefix):]
		}
	}
	return msg
}
