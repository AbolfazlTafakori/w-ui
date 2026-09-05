package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// Asking a node to update itself.
//
// The word "asking" is the design. Nothing about the release travels from here:
// the node fetches it from the project's repository and checks its signature
// against a key built into its own binary. So the worst an attacker who holds
// this panel can do with this is make nodes install an official release — not
// run code of their choosing on every machine, which is what pushing a binary
// would have meant.
//
// A node that will not update says why, and the reason is passed back rather
// than flattened into a failure: "this build carries no release-signing key" is
// something an operator can act on, and "the node did not answer" is a
// different problem entirely.

// updateAskTimeout bounds the call. Generous, because a node that accepts the
// request downloads a release before it answers.
const updateAskTimeout = 5 * time.Minute

// UpdateResult is what a node said when asked.
type UpdateResult struct {
	Updated bool   `json:"updated"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Notice  string `json:"notice,omitempty"`
}

// AskToUpdate tells one node to fetch and install the newest release.
func AskToUpdate(ctx context.Context, db *gorm.DB, id uint) (*UpdateResult, error) {
	var node model.Node
	if err := db.WithContext(ctx).First(&node, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no node %d", service.ErrNotFound, id)
	}
	if node.Kind == model.KindLocal {
		return nil, fmt.Errorf("%w: this panel updates itself from its own page, not as a node",
			service.ErrInvalid)
	}

	ctx, cancel := context.WithTimeout(ctx, updateAskTimeout)
	defer cancel()

	endpoint := strings.TrimRight(node.Address, "/") + "/api/system/update"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(nil))
	if err != nil {
		return nil, fmt.Errorf("%w: %s has a bad address", service.ErrInvalid, node.Name)
	}
	req.Header.Set("Authorization", "Bearer "+node.Token)
	req.Header.Set("Content-Type", "application/json")

	// Built the same way every other call to a node is: the same certificate
	// checking, the same refusal to reach an address inside this server.
	var id2 *Identity
	if node.TLSMode == model.TLSMutual {
		if id2, err = EnsureIdentity(db); err != nil {
			return nil, fmt.Errorf("%w: could not load this panel's client certificate: %v",
				service.ErrInvalid, err)
		}
	}
	client, err := clientFor(node, updateAskTimeout, id2)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: could not reach %s: %v", service.ErrInvalid, node.Name, err)
	}
	defer resp.Body.Close()

	var out UpdateResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: %s answered, but not like a W-UI panel", service.ErrInvalid, node.Name)
	}

	// A refusal with a reason is not a fault. The node saying it has no signing
	// key, or that it is already current, is the answer.
	if resp.StatusCode >= 300 && out.Notice == "" {
		return nil, fmt.Errorf("%w: %s answered %s", service.ErrInvalid, node.Name, resp.Status)
	}

	return &out, nil
}
