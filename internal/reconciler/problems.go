package reconciler

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// What is going wrong right now, for the panel's pages, not only its log.
//
// A failure the loop meets -- the firewall table it could not write, usage
// it could not store, a held device whose session it could not end -- is
// kept here under a fixed code while it lasts and taken away when the
// thing works again. The owner is shown every one; an operator those about
// their own customers (ClientID) and, with no detail, those that stop the
// panel holding anybody to their plan.

// Problem codes. Fixed: a page and a script tell them apart by these.
const (
	ProblemLoopFailed       = "loop.failed"
	ProblemEnforceFailed    = "enforce.failed"
	ProblemUsageNotWritten  = "usage.notWritten"
	ProblemDisconnectFailed = "limit.disconnectFailed"
)

// Problem is one thing going wrong.
type Problem struct {
	Code string `json:"code"`
	// Detail is what failed, for the owner: the error itself. An operator
	// is never sent it.
	Detail string `json:"detail,omitempty"`
	// ClientID is the customer it is about, 0 for the whole panel; Name
	// their name and Device the device, when it is about one.
	ClientID uint      `json:"clientId,omitempty"`
	Name     string    `json:"name,omitempty"`
	Device   string    `json:"device,omitempty"`
	Since    time.Time `json:"since"`
}

// problems is the set going wrong now, by a key of code and subject.
type problems struct {
	mu  sync.Mutex
	now map[string]Problem
}

func (p *problems) set(key string, pr Problem) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.now == nil {
		p.now = map[string]Problem{}
	}
	if have, ok := p.now[key]; ok {
		pr.Since = have.Since // going wrong since it first did
	} else if pr.Since.IsZero() {
		pr.Since = time.Now().UTC()
	}
	p.now[key] = pr
}

func (p *problems) clear(key string) {
	p.mu.Lock()
	delete(p.now, key)
	p.mu.Unlock()
}

// list is every problem now, oldest first.
func (p *problems) list() []Problem {
	p.mu.Lock()
	out := make([]Problem, 0, len(p.now))
	for _, pr := range p.now {
		out = append(out, pr)
	}
	p.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since.Equal(out[j].Since) {
			return out[i].Code < out[j].Code
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// Problems is what is going wrong in the loop right now.
func (r *Reconciler) Problems() []Problem {
	out := r.problems.list()
	if failing, _, held := r.writer.health(); failing {
		out = append(out, Problem{Code: ProblemUsageNotWritten, Since: time.Now().UTC(),
			Detail: fmt.Sprintf("usage could not be written to the database; %d customers' usage is kept and written at the next try", held)})
	}
	return out
}
