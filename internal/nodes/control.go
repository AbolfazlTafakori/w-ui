package nodes

import (
	"context"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database"
)

// Control is what an operator does about nodes beyond their rows: this
// panel's client identity, the authority it accepts from a panel managing
// it, and asking a node to update.
type Control struct {
	db *gorm.DB
}

// NewControl builds it.
func NewControl(db *gorm.DB) *Control { return &Control{db: db} }

// Identity is this panel's client identity, minted the first time it is
// asked for.
func (c *Control) Identity() (*Identity, error) { return EnsureIdentity(c.db) }

// Trust is the authority this panel accepts from a panel managing it, as
// stored; empty when none is.
func (c *Control) Trust() (string, error) {
	pem, _, err := database.GetSetting(c.db, KeyMTLSTrustCA)
	return pem, err
}

// SetTrust stores it; empty stops asking for a certificate.
func (c *Control) SetTrust(pem string) error {
	return database.PutSetting(c.db, KeyMTLSTrustCA, pem)
}

// AskToUpdate tells one node to fetch and install the newest release.
func (c *Control) AskToUpdate(ctx context.Context, id uint) (*UpdateResult, error) {
	return AskToUpdate(ctx, c.db, id)
}
