package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backup"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
)

// Reporter writes the periodic status message the Telegram bot sends: what
// the panel is carrying, who is about to run out, and -- when asked -- the
// database to go with it.
type Reporter struct {
	db       *gorm.DB
	clients  *Clients
	settings *Settings
	backups  *backup.Service
	version  string
}

func NewReporter(db *gorm.DB, clients *Clients, settings *Settings, backups *backup.Service, version string) *Reporter {
	return &Reporter{db: db, clients: clients, settings: settings, backups: backups, version: version}
}

// Build is the notify.ReportFunc.
func (r *Reporter) Build(ctx context.Context, lang string, withBackup bool) (string, *notify.Attachment, error) {
	ov, err := r.clients.Overview(ctx)
	if err != nil {
		return "", nil, err
	}
	cfg, _ := r.settings.Get(ctx)
	fa := lang == "fa"
	l := func(en, per string) string {
		if fa {
			return per
		}
		return en
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*W-UI %s*\n", r.version)
	fmt.Fprintf(&b, "%s: %d\n", l("Clients", "کاربران"), ov.Clients)
	fmt.Fprintf(&b, "%s: %d · %s: %d · %s: %d\n",
		l("Active", "فعال"), ov.Active, l("Online", "آنلاین"), ov.Online, l("Ended", "منقضی"), ov.Exhausted+ov.Expired)
	fmt.Fprintf(&b, "%s: %d\n", l("Inbounds", "ورودی‌ها"), ov.Interfaces)
	fmt.Fprintf(&b, "%s: %s\n", l("Total traffic", "ترافیک کل"), humanBytes(ov.TotalUsed))

	// Who is close to the line, by the thresholds on the settings page.
	var expiring []model.Client
	if cfg.ExpireDiff > 0 {
		limit := time.Now().UTC().Add(time.Duration(cfg.ExpireDiff) * 24 * time.Hour)
		r.db.WithContext(ctx).Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?",
			model.StatusActive, limit).Order("expires_at").Limit(20).Find(&expiring)
	}
	if len(expiring) > 0 {
		fmt.Fprintf(&b, "\n*%s (%d %s)*\n", l("Expiring soon", "در حال انقضا"), cfg.ExpireDiff, l("days", "روز"))
		for _, c := range expiring {
			fmt.Fprintf(&b, "• %s\n", c.Name)
		}
	}
	var lowTraffic []model.Client
	if cfg.TrafficDiff > 0 {
		margin := uint64(cfg.TrafficDiff) * 1024 * 1024 * 1024
		r.db.WithContext(ctx).Where("status = ? AND quota_bytes > 0 AND quota_bytes - used_bytes <= ?",
			model.StatusActive, margin).Order("quota_bytes - used_bytes").Limit(20).Find(&lowTraffic)
	}
	if len(lowTraffic) > 0 {
		fmt.Fprintf(&b, "\n*%s (%d GB)*\n", l("Running out of traffic", "در حال اتمام ترافیک"), cfg.TrafficDiff)
		for _, c := range lowTraffic {
			left := uint64(0)
			if c.QuotaBytes > c.UsedBytes {
				left = c.QuotaBytes - c.UsedBytes
			}
			fmt.Fprintf(&b, "• %s — %s\n", c.Name, humanBytes(left))
		}
	}
	fmt.Fprintf(&b, "\n_%s_", time.Now().Format("2006-01-02 15:04 MST"))

	if !withBackup || r.backups == nil {
		return b.String(), nil, nil
	}
	// The report still goes when the archive cannot be made; the failure is
	// in the log, and a report without a file is better than no report.
	arch, err := r.backups.Create(ctx)
	if err != nil {
		return b.String(), nil, nil
	}
	f, _, err := r.backups.Open(arch.Name)
	if err != nil {
		return b.String(), nil, nil
	}
	return b.String(), &notify.Attachment{Name: arch.Name, Body: closeAfter{f}}, nil
}

// closeAfter closes the file once the upload has read it to the end.
type closeAfter struct{ f *os.File }

func (c closeAfter) Read(p []byte) (int, error) {
	n, err := c.f.Read(p)
	if err != nil {
		c.f.Close()
	}
	return n, err
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
