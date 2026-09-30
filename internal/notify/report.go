package notify

import (
	"context"
	"time"
)

// ReportFunc builds the periodic report. Called on the schedule, never
// concurrently.
type ReportFunc func(ctx context.Context, lang string) (text string, err error)

// RunReports sends the periodic report on the configured schedule until ctx
// ends. The schedule is re-read every few minutes, so a change on the
// settings page takes effect without a restart.
func (n *Notifier) RunReports(ctx context.Context, build ReportFunc) {
	go func() {
		var due time.Time
		lastRunTime := ""
		for {
			cfg := n.Config()
			if cfg.RunTime != lastRunTime || due.IsZero() {
				lastRunTime = cfg.RunTime
				due = time.Time{}
				if sc, err := ParseSchedule(cfg.RunTime); err == nil && cfg.RunTime != "" {
					due = sc.Next(time.Now())
				}
			}
			wait := 5 * time.Minute
			if !due.IsZero() && time.Until(due) < wait {
				wait = time.Until(due)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if due.IsZero() || time.Now().Before(due) {
				continue
			}
			due = time.Time{} // recomputed at the top
			cfg = n.Config()
			if !cfg.ready() {
				continue
			}
			text, err := build(ctx, cfg.Lang)
			if err != nil {
				n.log.Warn("could not build the periodic report", "error", err)
				continue
			}
			if err := n.post(ctx, cfg, text); err != nil {
				n.log.Warn("could not send the periodic report", "error", err)
			}
		}
	}()
}
