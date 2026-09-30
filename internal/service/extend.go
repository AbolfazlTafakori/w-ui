package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Adding time or traffic to many customers at once, or taking it back.
//
// The shape of it is what an owner or a reseller does after an outage, a
// promotion or a mistake: every customer selected gets the same amount more
// -- or less -- of what they already have, each from where they stand. The
// rules, each decided with the people who sell plans on this panel:
//
//   - A customer without a limit keeps having none. Adding three days to
//     someone with no end date would give them one; adding traffic to an
//     unlimited allowance would cap it. They are passed over, and counted.
//   - Time moves the customer's own end date, never "from today". A plan
//     that ended five days ago and is given two ends three days ago, still
//     over; one that ended a day ago and is given two runs one more day.
//   - A plan that starts on first connection and has not started yet is
//     passed over, unless asked for: then the time goes onto the plan's
//     length, to the hour, and it still starts on first connection.
//   - Traffic moves the allowance, never the usage.
//   - Taking back is allowed, for undoing a mistake. Time taken back can end
//     a plan. Traffic taken back stops at what the customer has already used
//     -- the allowance cannot go below that, and never to nothing, which
//     would read as no limit at all.
//   - A customer switched off stays off, with the new time or traffic there
//     for when they are switched on. Everyone else's state follows from their
//     new date and allowance: running out ends them, getting more back starts
//     them again.
//   - Only the customers selected. There is no "everyone" here: selecting
//     every customer, or every one a filter matches, is how that is said.

// ExtendKind is what is being added or taken back.
type ExtendKind string

const (
	ExtendTime    ExtendKind = "time"
	ExtendTraffic ExtendKind = "traffic"
)

// Units, as plans are sold: a month is thirty days and a day twenty-four hours;
// traffic counts in 1024s, as the panel shows it.
const (
	hoursPerDay   = 24
	daysPerMonth  = 30
	bytesPerMB    = 1 << 20
	bytesPerGB    = 1 << 30
	bytesPerTB    = 1 << 40
	maxExtendTime = 100 * 12 * daysPerMonth * hoursPerDay * time.Hour // a hundred years of 360 days
	maxExtendData = 1 << 50                                           // a pebibyte
)

// maxExtendIDs bounds one request, as the matching selection does.
const maxExtendIDs = maxMatchingIDs

// ExtendInput is a selection and an amount.
type ExtendInput struct {
	IDs  []uint     `json:"ids"`
	Kind ExtendKind `json:"kind"`

	// For time: months of thirty days, days, hours. Fractions are allowed.
	Months float64 `json:"months"`
	Days   float64 `json:"days"`
	Hours  float64 `json:"hours"`

	// For traffic: in 1024s.
	TB float64 `json:"tb"`
	GB float64 `json:"gb"`
	MB float64 `json:"mb"`

	// Subtract takes the amount back instead of adding it.
	Subtract bool `json:"subtract"`
	// IncludeWaiting adds time to plans that start on first connection and
	// have not started yet. Traffic ignores it: an allowance is the same
	// whether or not the clock has started.
	IncludeWaiting bool `json:"includeWaiting"`
	// DryRun says what would happen, and changes nothing.
	DryRun bool `json:"dryRun"`
}

// Why a selected customer was not changed.
const (
	SkipUnlimited = "unlimited" // no end date, or no traffic limit
	SkipWaiting   = "waiting"   // starts on first connection, not started, not asked for
	SkipGone      = "gone"      // not found: deleted meanwhile, or not this operator's
	SkipTooShort  = "tooShort"  // taking back would leave a waiting plan no time at all
)

// ExtendResult says what happened, or would.
type ExtendResult struct {
	// Changed is how many customers were given or had taken back the amount.
	Changed int `json:"changed"`
	// Skipped counts those passed over, by reason.
	Skipped map[string]int `json:"skipped"`
	// Revived are customers who were ended and now run again.
	Revived int `json:"revived"`
	// Ended are customers who were running and now are not: their time or
	// traffic was taken back past what they had.
	Ended int `json:"ended"`
	// StillEnded were given time and are still past their end date.
	StillEnded int `json:"stillEnded"`
	// Floored had traffic taken back down to what they have used.
	Floored int `json:"floored"`
	// Amount is what each changed customer got, as the server understood
	// it: seconds of time or bytes of traffic, negative when taken back.
	Amount int64 `json:"amount"`
}

// Extend adds (or takes back) time or traffic for the selected customers.
func (s *Clients) Extend(ctx context.Context, in ExtendInput) (*ExtendResult, error) {
	if err := checkBarred(ctx); err != nil {
		return nil, err
	}
	ids := dedupe(in.IDs)
	if len(ids) == 0 {
		return nil, invalidField("ids", "select the customers first")
	}
	if len(ids) > maxExtendIDs {
		return nil, invalidField("ids", "%d customers selected; at most %d at once", len(ids), maxExtendIDs)
	}

	var delta time.Duration
	var bytes int64
	switch in.Kind {
	case ExtendTime:
		d, err := in.duration()
		if err != nil {
			return nil, err
		}
		delta = d
	case ExtendTraffic:
		b, err := in.traffic()
		if err != nil {
			return nil, err
		}
		bytes = b
	default:
		return nil, invalidField("kind", "kind is time or traffic")
	}
	if in.Subtract {
		delta, bytes = -delta, -bytes
	}

	res := &ExtendResult{Skipped: map[string]int{}}
	if in.Kind == ExtendTime {
		res.Amount = int64(delta / time.Second)
	} else {
		res.Amount = bytes
	}

	now := time.Now().UTC()
	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		found := 0
		for start := 0; start < len(ids); start += 1000 {
			chunk := ids[start:min(start+1000, len(ids))]
			var members []model.Client
			// Through the scope: a reseller's selection can only ever reach
			// their own customers, whatever ids were sent.
			if err := tx.Where("id IN ?", chunk).Find(&members).Error; err != nil {
				return fmt.Errorf("service: load the selection: %w", err)
			}
			found += len(members)
			for i := range members {
				fields, why := extendOne(&members[i], in, delta, bytes, now, res)
				if why != "" {
					res.Skipped[why]++
					continue
				}
				res.Changed++
				if in.DryRun {
					continue
				}
				if err := tx.Model(&model.Client{}).Where("id = ?", members[i].ID).Updates(fields).Error; err != nil {
					return fmt.Errorf("service: change %s: %w", members[i].Name, err)
				}
			}
		}
		if gone := len(ids) - found; gone > 0 {
			res.Skipped[SkipGone] += gone
		}
		if in.DryRun {
			// Nothing was written; nothing to keep.
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return nil, err
	}

	if !in.DryRun && res.Changed > 0 {
		s.log.Info("customers' plans changed in bulk",
			"kind", in.Kind, "subtract", in.Subtract, "amount", res.Amount,
			"changed", res.Changed, "skipped", res.Skipped,
			"revived", res.Revived, "ended", res.Ended)
		if SubscriptionsChanged != nil {
			SubscriptionsChanged()
		}
	}
	return res, nil
}

// errDryRun rolls a dry run's transaction back.
var errDryRun = errors.New("dry run")

// extendOne works out one customer's change: the fields to write, or why
// they are passed over. It also counts the change into res.
func extendOne(c *model.Client, in ExtendInput, delta time.Duration, bytes int64, now time.Time, res *ExtendResult) (map[string]any, string) {
	before := autoStatus(*c, now)
	fields := map[string]any{}
	floored := false

	switch in.Kind {
	case ExtendTime:
		switch {
		case c.Waiting():
			if !in.IncludeWaiting {
				return nil, SkipWaiting
			}
			// Kept to the hour: whole days, and the hours past them.
			total := c.Duration() + delta.Round(time.Hour)
			if total < time.Hour {
				return nil, SkipTooShort
			}
			hours := int(total / time.Hour)
			c.DurationDays, c.DurationHours = hours/hoursPerDay, hours%hoursPerDay
			fields["duration_days"] = c.DurationDays
			fields["duration_hours"] = c.DurationHours
		case c.ExpiresAt == nil:
			return nil, SkipUnlimited
		default:
			// From their own end date, whether that has passed or not.
			next := c.ExpiresAt.Add(delta)
			c.ExpiresAt = &next
			fields["expires_at"] = next
			if !in.Subtract && !next.After(now) {
				res.StillEnded++
			}
		}

	case ExtendTraffic:
		if c.QuotaBytes == 0 {
			return nil, SkipUnlimited
		}
		next := int64(c.QuotaBytes) + bytes
		// Never below what is already used, and never to nothing: a quota of
		// zero is no limit at all.
		floor := max(int64(c.UsedBytes), 1)
		if next < floor {
			next = floor
			floored = true
			res.Floored++
		}
		c.QuotaBytes = uint64(next)
		fields["quota_bytes"] = c.QuotaBytes
	}

	after := autoStatus(*c, now)
	// Taken back to the floor, nothing is left to use: a customer who had
	// used nothing is held at a single byte -- the limit cannot be zero,
	// which would read as none -- and is out of traffic, not running.
	if floored && after == model.StatusActive {
		after = model.StatusExhausted
	}
	if after != c.Status {
		fields["status"] = after
	}
	switch {
	case before != model.StatusActive && after == model.StatusActive:
		res.Revived++
	case before == model.StatusActive && after != model.StatusActive:
		res.Ended++
	}
	c.Status = after
	return fields, ""
}

// autoStatus is the state a customer's own date and allowance put them in. A
// customer switched off stays off: that is the operator's word, not the
// plan's.
func autoStatus(c model.Client, now time.Time) model.ClientStatus {
	switch {
	case c.Status == model.StatusDisabled:
		return model.StatusDisabled
	case c.ExpiresAt != nil && !c.ExpiresAt.After(now):
		return model.StatusExpired
	case c.QuotaBytes > 0 && c.UsedBytes >= c.QuotaBytes:
		return model.StatusExhausted
	default:
		return model.StatusActive
	}
}

// duration is the time asked for, as a duration.
func (in ExtendInput) duration() (time.Duration, error) {
	for name, v := range map[string]float64{"months": in.Months, "days": in.Days, "hours": in.Hours} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, invalidField(name, "%s is a number of zero or more; choose add or take back for the direction", name)
		}
	}
	hours := (in.Months*daysPerMonth+in.Days)*hoursPerDay + in.Hours
	if hours*float64(time.Hour) > float64(maxExtendTime) {
		return 0, invalidField("months", "that is more than a hundred years")
	}
	d := time.Duration(hours * float64(time.Hour)).Round(time.Second)
	if d <= 0 {
		return 0, invalidField("days", "enter an amount of time")
	}
	return d, nil
}

// traffic is the traffic asked for, in bytes.
func (in ExtendInput) traffic() (int64, error) {
	for name, v := range map[string]float64{"tb": in.TB, "gb": in.GB, "mb": in.MB} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, invalidField(name, "%s is a number of zero or more; choose add or take back for the direction", name)
		}
	}
	total := in.TB*bytesPerTB + in.GB*bytesPerGB + in.MB*bytesPerMB
	if total > maxExtendData {
		return 0, invalidField("tb", "that is more than a pebibyte")
	}
	b := int64(math.Round(total))
	if b <= 0 {
		return 0, invalidField("gb", "enter an amount of traffic")
	}
	return b, nil
}
