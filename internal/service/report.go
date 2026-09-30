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

// Reporter writes what the Telegram bot sends by itself: the periodic status
// message -- what the panel is carrying, who is about to run out -- and the
// automatic backup.
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
func (r *Reporter) Build(ctx context.Context, lang string) (string, error) {
	ov, err := r.clients.Overview(ctx)
	if err != nil {
		return "", err
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
			fmt.Fprintf(&b, "• %s\n", mdSafe(c.Name))
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
			fmt.Fprintf(&b, "• %s — %s\n", mdSafe(c.Name), humanBytes(left))
		}
	}
	fmt.Fprintf(&b, "\n_%s_", time.Now().Format("2006-01-02 15:04 MST"))

	return b.String(), nil
}

// Backup is the notify.BackupFunc: a new archive, open for the upload, with a
// caption saying which panel it is from and when.
func (r *Reporter) Backup(ctx context.Context, lang string) (*notify.Attachment, error) {
	if r.backups == nil {
		return nil, fmt.Errorf("backups are not available")
	}
	arch, err := r.backups.Create(ctx)
	if err != nil {
		return nil, err
	}
	f, _, err := r.backups.Open(arch.Name)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	when := arch.Taken.Local().Format("2006-01-02 15:04 MST")
	caption := fmt.Sprintf("W-UI %s backup · %s\n%s · %s\nRestore: Overview → Backup & Restore → Upload",
		r.version, host, when, humanBytes(uint64(arch.Size)))
	if lang == "fa" {
		caption = fmt.Sprintf("بکاپ W-UI %s · %s\n%s · %s\nبازگردانی: نمای کلی ← بکاپ و بازگردانی ← آپلود",
			r.version, host, when, humanBytes(uint64(arch.Size)))
	}
	return &notify.Attachment{Name: arch.Name, Body: f, Size: arch.Size, Caption: caption}, nil
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

// mdSafe keeps a name from being read as Markdown, which the report is sent
// in: an underscore in one would make Telegram refuse the whole report.
func mdSafe(s string) string {
	return strings.NewReplacer("_", " ", "*", " ", "`", "'", "[", "(", "]", ")").Replace(s)
}
