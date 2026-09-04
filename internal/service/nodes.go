package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Nodes manages the other servers this panel watches.
//
// A node is another W-UI panel, reached over the same API this one serves. That
// is a deliberate choice over a purpose-built agent: there is one protocol to
// secure, one to keep working, and a node that is upgraded is still a panel
// somebody can sign in to and fix directly when something has gone wrong.
type Nodes struct {
	db  *gorm.DB
	log *slog.Logger
}

func NewNodes(db *gorm.DB, log *slog.Logger) *Nodes {
	return &Nodes{db: db, log: log}
}

// NodeInput is what the form collects.
type NodeInput struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Token   string `json:"token"`
	Note    string `json:"note"`
	Enabled *bool  `json:"enabled"`

	// UsageCoefficient multiplies what this node reports before it is charged
	// against a customer's allowance. Absent leaves it as it is; zero is read
	// as one, because a node that charged nothing would be free traffic nobody
	// asked for.
	UsageCoefficient *float64 `json:"usageCoefficient"`

	// DataLimitBytes is the machine's own monthly transfer allowance and
	// ResetDay is the day of the month the host starts it again. Absent leaves
	// both as they are; zero in either means no cap and no automatic reset.
	DataLimitBytes *uint64 `json:"dataLimitBytes"`
	ResetDay       *int    `json:"resetDay"`
}

// List returns every node, the local one first.
func (s *Nodes) List(ctx context.Context) ([]model.Node, error) {
	var out []model.Node
	// The local node is not a peer and is always the first row: it is where the
	// operator already is, and sorting it among the others by name would move
	// it around as nodes are added.
	err := s.db.WithContext(ctx).
		Order("CASE WHEN kind = 'local' THEN 0 ELSE 1 END, name").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("service: list nodes: %w", err)
	}
	return out, nil
}

// Create registers a remote panel.
func (s *Nodes) Create(ctx context.Context, in NodeInput) (*model.Node, error) {
	if err := s.validate(&in, true); err != nil {
		return nil, err
	}

	var clash int64
	if err := s.db.WithContext(ctx).Model(&model.Node{}).
		Where("LOWER(name) = LOWER(?)", in.Name).Count(&clash).Error; err != nil {
		return nil, fmt.Errorf("service: check node name: %w", err)
	}
	if clash > 0 {
		return nil, invalidField("name", "a node called %q already exists", in.Name)
	}

	coefficient := 1.0
	if in.UsageCoefficient != nil && *in.UsageCoefficient > 0 {
		coefficient = *in.UsageCoefficient
	}

	node := model.Node{
		Name:             in.Name,
		UsageCoefficient: coefficient,
		DataLimitBytes:   deref(in.DataLimitBytes),
		ResetDay:         deref(in.ResetDay),
		Kind:             model.KindRemote,
		Address:          in.Address,
		Token:            in.Token,
		Note:             in.Note,
		Enabled:          in.Enabled == nil || *in.Enabled,
	}
	if err := s.db.WithContext(ctx).Create(&node).Error; err != nil {
		return nil, fmt.Errorf("service: create node: %w", err)
	}
	s.log.Info("node added", "node", node.Name, "address", node.Address)
	return &node, nil
}

// Update changes a node.
func (s *Nodes) Update(ctx context.Context, id uint, in NodeInput) (*model.Node, error) {
	var node model.Node
	if err := s.db.WithContext(ctx).First(&node, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no node %d", ErrNotFound, id)
	}
	if node.Kind == model.KindLocal {
		return nil, fmt.Errorf("%w: this panel's own entry cannot be edited", ErrInvalid)
	}
	if err := s.validate(&in, false); err != nil {
		return nil, err
	}

	updates := map[string]any{
		"name":       in.Name,
		"address":    in.Address,
		"note":       in.Note,
		"updated_at": time.Now().UTC(),
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	// An empty token means "leave it alone". The form cannot show the stored
	// one, so submitting the page unchanged must not wipe it.
	if strings.TrimSpace(in.Token) != "" {
		updates["token"] = in.Token
	}
	// Zero is refused rather than stored: a node that charged nothing would
	// serve traffic that never appears on any customer's total, which is the
	// one setting here that quietly gives service away.
	if in.UsageCoefficient != nil {
		if *in.UsageCoefficient <= 0 {
			return nil, invalidField("usageCoefficient",
				"a coefficient of %g would charge nothing for traffic through this server",
				*in.UsageCoefficient)
		}
		if *in.UsageCoefficient > 100 {
			return nil, invalidField("usageCoefficient",
				"a coefficient of %g charges a hundred times what the customer used",
				*in.UsageCoefficient)
		}
		updates["usage_coefficient"] = *in.UsageCoefficient
	}

	if in.DataLimitBytes != nil {
		updates["data_limit_bytes"] = *in.DataLimitBytes
	}
	if in.ResetDay != nil {
		updates["reset_day"] = *in.ResetDay
	}

	if err := s.db.WithContext(ctx).Model(&node).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("service: update node: %w", err)
	}
	if err := s.db.WithContext(ctx).First(&node, id).Error; err != nil {
		return nil, fmt.Errorf("service: reload node: %w", err)
	}
	return &node, nil
}

// Delete removes a node. The local one stays.
func (s *Nodes) Delete(ctx context.Context, id uint) error {
	var node model.Node
	if err := s.db.WithContext(ctx).First(&node, id).Error; err != nil {
		return fmt.Errorf("%w: no node %d", ErrNotFound, id)
	}
	if node.Kind == model.KindLocal {
		return fmt.Errorf("%w: this panel's own entry cannot be removed", ErrInvalid)
	}

	// Interfaces on a node this panel no longer watches would be orphaned rows
	// that no reconciler visits, so removal is refused while any remain rather
	// than leaving them behind or deleting a remote server's configuration
	// from here.
	var ifaces int64
	if err := s.db.WithContext(ctx).Model(&model.Interface{}).
		Where("node_id = ?", id).Count(&ifaces).Error; err != nil {
		return fmt.Errorf("service: count interfaces: %w", err)
	}
	if ifaces > 0 {
		return fmt.Errorf("%w: %s still carries %d interface(s); remove them first",
			ErrInvalid, node.Name, ifaces)
	}

	if err := s.db.WithContext(ctx).Delete(&node).Error; err != nil {
		return fmt.Errorf("service: delete node: %w", err)
	}
	s.log.Info("node removed", "node", node.Name)
	return nil
}

func (s *Nodes) validate(in *NodeInput, needToken bool) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)
	in.Token = strings.TrimSpace(in.Token)
	in.Note = strings.TrimSpace(in.Note)

	if in.Name == "" {
		return invalidField("name", "a node needs a name")
	}
	if len(in.Name) > 64 {
		return invalidField("name", "that name is too long")
	}
	if in.Address == "" {
		return invalidField("address", "a node needs an address")
	}

	u, err := url.Parse(in.Address)
	if err != nil || u.Host == "" {
		return invalidField("address", "%q is not a URL. It should look like https://vpn2.example.com:2096",
			in.Address)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return invalidField("address", "the address must start with http:// or https://")
	}
	// Said once, here. A token travelling over plain HTTP to another machine is
	// readable by everything between them, and this is the panel's own key to a
	// whole other server.
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		s.log.Warn("node address is plain HTTP; its token travels unencrypted",
			"node", in.Name, "address", in.Address)
	}

	if needToken && in.Token == "" {
		return invalidField("token", "a node needs an access token. Create one on that "+
			"panel under Settings, then paste it here")
	}

	// 28 rather than 31: a reset day of the 30th would skip February entirely,
	// and a transfer limit that lapses for one month a year lapses in the month
	// nobody is watching for it.
	if in.ResetDay != nil && (*in.ResetDay < 0 || *in.ResetDay > 28) {
		return invalidField("resetDay",
			"the allowance can start again on day 1 to 28 of the month, or 0 to never reset")
	}
	return nil
}

func isLoopback(host string) bool {
	return host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"
}

// deref reads an optional field, treating absent as the zero value.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// ── access tokens ────────────────────────────────────────────────────────────

// TokenPlain is a freshly issued token, shown once.
type TokenPlain struct {
	Token string          `json:"token"`
	Meta  *model.APIToken `json:"meta"`
}

// IssueToken creates a token for another panel to use against this one.
func (s *Nodes) IssueToken(ctx context.Context, name string) (*TokenPlain, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: give the token a name so you know what it is for", ErrInvalid)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("service: generate token: %w", err)
	}
	// Prefixed so it is recognisable in a log or a configuration file as
	// something that should not be there.
	secret := "wui_" + base64.RawURLEncoding.EncodeToString(raw)

	row := model.APIToken{
		Name: name,
		Hash: hashToken(secret),
		// Enough to tell two tokens apart in a list without being enough to
		// use one.
		Prefix: secret[:12],
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, fmt.Errorf("service: store token: %w", err)
	}
	s.log.Info("access token issued", "name", name, "prefix", row.Prefix)
	return &TokenPlain{Token: secret, Meta: &row}, nil
}

// ListTokens returns the tokens, without the secrets.
func (s *Nodes) ListTokens(ctx context.Context) ([]model.APIToken, error) {
	var out []model.APIToken
	if err := s.db.WithContext(ctx).Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("service: list tokens: %w", err)
	}
	return out, nil
}

// RevokeToken deletes one.
func (s *Nodes) RevokeToken(ctx context.Context, id uint) error {
	if err := s.db.WithContext(ctx).Delete(&model.APIToken{}, id).Error; err != nil {
		return fmt.Errorf("service: revoke token: %w", err)
	}
	return nil
}

// VerifyToken reports whether a presented token is one of ours.
func (s *Nodes) VerifyToken(ctx context.Context, presented string) bool {
	if !strings.HasPrefix(presented, "wui_") {
		return false
	}

	// Looked up by hash rather than compared one by one: the hash is unique and
	// indexed, so the cost does not grow with the number of tokens and there is
	// no loop whose duration leaks how many exist.
	var row model.APIToken
	err := s.db.WithContext(ctx).Where("hash = ?", hashToken(presented)).First(&row).Error
	if err != nil {
		return false
	}

	now := time.Now().UTC()
	s.db.WithContext(ctx).Model(&row).UpdateColumn("last_used_at", now)
	return true
}

// hashToken is SHA-256 rather than bcrypt.
//
// A token is 256 bits of randomness from a generator, not a human's choice, so
// there is nothing to brute-force and no reason to pay bcrypt's cost on every
// request a node makes.
func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
