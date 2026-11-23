package operators

import (
	"context"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
)

// The rules of a sale an operator is held to (model.Admin.SaleMaxDays,
// SaleMaxBytes, RekeyFree), checked and turned into the columns to write.
// The owner sets them for anyone; an admin sets them for their own
// resellers, never looser than their own: a reseller of an admin held to
// thirty days is held to thirty days or fewer, and one whose admin pays for
// a new link pays for it too.

// MaxSaleBytes bounds the most traffic one sale may be held to: a petabyte,
// far past any plan and far short of overflowing a count.
const MaxSaleBytes = uint64(1) << 50

// saleRules checks what in asks of an operator's sale rules, on top of what
// they have now (cur; zero on a new one), and gives the columns to write. A
// role with no ceiling has none, and nothing is written for it.
func saleRules(ctx context.Context, role model.AdminRole, cur model.Admin, in AdminInput) (map[string]any, error) {
	if !role.Capped() || (in.SaleMaxDays == nil && in.SaleMaxBytes == nil && in.RekeyFree == nil) {
		return nil, nil
	}
	days, bytes, free := cur.SaleMaxDays, cur.SaleMaxBytes, cur.RekeyFree
	if in.SaleMaxDays != nil {
		if *in.SaleMaxDays < 0 || *in.SaleMaxDays > MaxTermDays {
			return nil, fault.Field("saleMaxDays", "the longest sale is 0 (no rule) to %d days", MaxTermDays)
		}
		days = *in.SaleMaxDays
	}
	if in.SaleMaxBytes != nil {
		if *in.SaleMaxBytes > MaxSaleBytes {
			return nil, fault.Field("saleMaxBytes", "the most traffic one sale gives is at most a petabyte")
		}
		bytes = *in.SaleMaxBytes
	}
	if in.RekeyFree != nil {
		free = *in.RekeyFree
	}
	if caller := scopedTo(ctx); caller != nil {
		if caller.SaleMaxDays > 0 && (days == 0 || days > caller.SaleMaxDays) {
			return nil, fault.Field("saleMaxDays", "your own sales are held to %d days, so your reseller's are too: 1 to %d", caller.SaleMaxDays, caller.SaleMaxDays)
		}
		if caller.SaleMaxBytes > 0 && (bytes == 0 || bytes > caller.SaleMaxBytes) {
			return nil, fault.Field("saleMaxBytes", "your own sales are held to a traffic limit, so your reseller's are too, no larger than yours")
		}
		if !caller.RekeyFree && free {
			return nil, fault.Field("rekeyFree", "a new link or password costs you users, so it costs your reseller too")
		}
	}
	return map[string]any{"sale_max_days": days, "sale_max_bytes": bytes, "rekey_free": free}, nil
}
