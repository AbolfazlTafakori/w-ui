package operators

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
)

// An operator's own account: signing in, the session, the second factor and
// the password. What is asked of the person -- the password, the code, how
// often they may try -- is the API's; what is stored, and what a sign-in
// starts, is here.

// ByUsername is the operator signing in under this name, fault.ErrNotFound when
// there is none.
func (s *Admins) ByUsername(ctx context.Context, username string) (*model.Admin, error) {
	var a model.Admin
	err := s.db.WithContext(ctx).Where("username = ?", username).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fault.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("service: load operator: %w", err)
	}
	return &a, nil
}

// ForSession is the operator a session names, by the id it carries; the row
// alone, as the session check needs it.
func (s *Admins) ForSession(ctx context.Context, subject string) (*model.Admin, error) {
	id, err := strconv.ParseUint(subject, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: operator %q", fault.ErrNotFound, subject)
	}
	var a model.Admin
	err = s.db.WithContext(ctx).First(&a, uint(id)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fault.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("service: load operator: %w", err)
	}
	return &a, nil
}

// SignedIn records a sign-in: when and from where, and the start of a
// reseller's term that was on hold until now. admin is updated to match.
//
// The term is started with a condition on the row, so two sign-ins racing
// each other cannot both start the clock. Neither write failing stops the
// sign-in: the person has proved who they are.
func (s *Admins) SignedIn(ctx context.Context, admin *model.Admin, ip string, now time.Time) {
	now = now.UTC()
	if admin.Role.Capped() && admin.ExpiresAt == nil && admin.DurationDays > 0 {
		ends := now.Add(time.Duration(admin.DurationDays) * 24 * time.Hour)
		res := s.db.WithContext(ctx).Model(&model.Admin{}).
			Where("id = ? AND expires_at IS NULL AND duration_days > 0", admin.ID).
			Updates(map[string]any{"expires_at": ends, "duration_days": 0})
		if res.Error != nil {
			s.log.Error("start a reseller's term", "username", admin.Username, "error", res.Error)
		} else if res.RowsAffected > 0 {
			s.log.Info("reseller's term started on first sign-in",
				"username", admin.Username, "days", admin.DurationDays, "until", ends)
			admin.ExpiresAt, admin.DurationDays = &ends, 0
		}
	}
	if err := s.db.WithContext(ctx).Model(admin).
		Updates(map[string]any{"last_login_at": now, "last_login_ip": ip}).Error; err != nil {
		s.log.Error("record sign-in", "error", err)
	}
}

// Held is how many customers an operator has.
func (s *Admins) Held(ctx context.Context, adminID uint) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&model.Client{}).Where("owner_id = ?", adminID).Count(&n).Error
	return n, err
}

// SetSecondFactor stores an operator's authenticator secret; empty removes
// it.
func (s *Admins) SetSecondFactor(ctx context.Context, adminID uint, secret string) error {
	return s.db.WithContext(ctx).Model(&model.Admin{}).
		Where("id = ?", adminID).Update("totp_secret", secret).Error
}

// ChangeCredentials gives an operator a new name, a new password, or both,
// and ends every session they have. Empty leaves that one as it is.
//
// The session generation moves with the password, in the same statement:
// somebody changes it because they think a session is not theirs, and a
// signed token that outlives the change means the intruder keeps working
// while the operator believes they have shut the door. One statement, so a
// failure cannot leave the password changed and the old sessions alive.
func (s *Admins) ChangeCredentials(ctx context.Context, admin *model.Admin, username, password string) error {
	updates := map[string]any{"session_epoch": gorm.Expr("session_epoch + 1")}
	if username != "" {
		updates["username"] = username
	}
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		updates["password_hash"] = string(hash)
	}
	if err := s.db.WithContext(ctx).Model(admin).Updates(updates).Error; err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}
	return nil
}

// SetLocale keeps the operator's language, so it follows them to another
// browser.
func (s *Admins) SetLocale(ctx context.Context, admin *model.Admin, locale string) error {
	if err := s.db.WithContext(ctx).Model(admin).Update("locale", locale).Error; err != nil {
		return fmt.Errorf("store locale: %w", err)
	}
	admin.Locale = locale
	return nil
}
