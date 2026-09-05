// Package nodes watches the other panels this one federates with.
//
// The point of it is that an operator running three servers should learn a node
// is down from this page rather than from a customer. So each node is asked, on
// a schedule, whether it is alive and what it is carrying — and what it answers
// is stored, so the page has something to show the moment it opens instead of
// filling in a few seconds later.
package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

const (
	// probeEvery is how often each node is asked. Frequent enough that an
	// outage is noticed in about a minute, rare enough that ten nodes cost
	// nothing worth measuring.
	probeEvery = 30 * time.Second

	// probeTimeout bounds one node. A server that accepts the connection and
	// then stops answering must not hold up the others.
	probeTimeout = 8 * time.Second

	// parallel caps how many are asked at once, so a panel with fifty nodes
	// does not open fifty sockets in the same instant.
	parallel = 8
)

// Prober polls the remote nodes.
type Prober struct {
	db  *gorm.DB
	log *slog.Logger
}

func New(db *gorm.DB, log *slog.Logger) *Prober {
	return &Prober{db: db, log: log}
}

// Start polls until the context ends.
func (p *Prober) Start(ctx context.Context) {
	go func() {
		// Once at startup, so the page is populated before the first tick
		// rather than a minute into the operator's visit.
		p.Round(ctx)

		ticker := time.NewTicker(probeEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.Round(ctx)
			}
		}
	}()
}

// Round probes every enabled remote node once.
func (p *Prober) Round(ctx context.Context) {
	var list []model.Node
	err := p.db.WithContext(ctx).
		Where("kind = ? AND enabled = ?", model.KindRemote, true).Find(&list).Error
	if err != nil {
		p.log.Warn("could not read the node list", "error", err)
		return
	}
	if len(list) == 0 {
		return
	}

	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := range list {
		node := list[i]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p.ProbeOne(ctx, node)
		}()
	}
	wg.Wait()
}

// Snapshot is what a node reports about itself.
type Snapshot struct {
	Version     string
	Clients     int64
	Interfaces  int64
	UptimeSec   int64
	CPUPercent  float64
	MemPercent  float64
	Enforcement string
	LatencyMS   int
}

// ProbeOne asks a single node and records the answer.
func (p *Prober) ProbeOne(ctx context.Context, node model.Node) Snapshot {
	snap, err := p.ask(ctx, node)

	now := time.Now().UTC()
	updates := map[string]any{
		"reachable":  err == nil,
		"latency_ms": snap.LatencyMS,
		"updated_at": now,
	}

	if err != nil {
		// The message is kept so the page can say why rather than only that.
		// "Connection refused" and "401" send an operator to entirely different
		// places, and a red dot sends them to neither.
		msg := err.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		updates["last_error"] = msg
	} else {
		updates["last_error"] = ""
		updates["last_seen_at"] = now
		updates["version"] = snap.Version
		updates["clients"] = snap.Clients
		updates["interfaces"] = snap.Interfaces
		updates["uptime_sec"] = snap.UptimeSec
		updates["cpu_percent"] = snap.CPUPercent
		updates["mem_percent"] = snap.MemPercent
		updates["enforcement"] = snap.Enforcement
	}

	if dbErr := p.db.WithContext(ctx).Model(&model.Node{}).
		Where("id = ?", node.ID).Updates(updates).Error; dbErr != nil {
		p.log.Warn("could not record a probe", "node", node.Name, "error", dbErr)
	}

	// Logged only on a change of state, or a panel with an unreachable node
	// fills its log with the same line twice a minute forever.
	if err != nil && node.Reachable {
		p.log.Warn("node became unreachable", "node", node.Name, "error", err)
	} else if err == nil && !node.Reachable {
		p.log.Info("node is reachable", "node", node.Name, "latency_ms", snap.LatencyMS)
	}
	return snap
}

func (p *Prober) ask(ctx context.Context, node model.Node) (Snapshot, error) {
	var snap Snapshot

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	endpoint := strings.TrimRight(node.Address, "/") + "/api/system"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return snap, fmt.Errorf("bad address: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+node.Token)

	// Per node: see clientFor. A probe that trusted a certificate the sync
	// loop would refuse would report a node as healthy right up until nothing
	// could be pushed to it.
	var id *Identity
	if node.TLSMode == model.TLSMutual {
		var idErr error
		if id, idErr = EnsureIdentity(p.db); idErr != nil {
			return snap, fmt.Errorf("could not load this panel's client certificate: %w", idErr)
		}
	}

	client, err := clientFor(node, probeTimeout, id)
	if err != nil {
		return snap, err
	}

	start := time.Now()
	resp, err := client.Do(req)
	snap.LatencyMS = int(time.Since(start).Milliseconds())
	if err != nil {
		return snap, fmt.Errorf("could not reach it: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return snap, fmt.Errorf("the token was refused; issue a new one on that panel")
	case resp.StatusCode >= 300:
		return snap, fmt.Errorf("it answered %s", resp.Status)
	}

	var payload struct {
		Version           string  `json:"version"`
		UptimeSec         int64   `json:"uptimeSec"`
		Clients           int64   `json:"clients"`
		Interfaces        int64   `json:"interfaces"`
		EnforcementActive bool    `json:"enforcementActive"`
		CPU               float64 `json:"cpuPercent"`
		Mem               float64 `json:"memPercent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		// Reaching something that is not a panel is the common case here —
		// a reverse proxy's error page, or the wrong port.
		return snap, fmt.Errorf("it answered, but not like a W-UI panel")
	}

	snap.Version = payload.Version
	snap.Clients = payload.Clients
	snap.Interfaces = payload.Interfaces
	snap.UptimeSec = payload.UptimeSec
	snap.CPUPercent = payload.CPU
	snap.MemPercent = payload.Mem
	snap.Enforcement = "exact"
	if !payload.EnforcementActive {
		snap.Enforcement = "reduced"
	}
	return snap, nil
}
