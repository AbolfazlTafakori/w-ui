package backup

import (
	"context"
	"time"
)

// Scheduler takes a backup on an interval.
//
// The interval is re-read on every tick rather than captured at start, so an
// operator changing it on the settings page does not have to restart the panel
// for the change to take effect.
type Scheduler struct {
	svc *Service

	// Every returns the current interval. Zero turns scheduling off.
	Every func() time.Duration

	// Keep returns how many archives to retain, also re-read each time.
	Keep func() int

	// OnBackup is called after a successful backup, for the notifier.
	OnBackup func(Archive)

	// checkEvery is how often the schedule is examined. It is far shorter than
	// any sensible backup interval so that a changed setting is picked up in
	// minutes rather than a day later.
	checkEvery time.Duration
}

func NewScheduler(svc *Service) *Scheduler {
	return &Scheduler{svc: svc, checkEvery: time.Minute}
}

// Start runs until the context ends.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.checkEvery)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *Scheduler) tick(ctx context.Context) {
	every := time.Duration(0)
	if s.Every != nil {
		every = s.Every()
	}
	if every <= 0 {
		return
	}
	if s.Keep != nil {
		s.svc.mu.Lock()
		s.svc.keep = s.Keep()
		s.svc.mu.Unlock()
	}

	// The schedule is derived from the newest archive on disk rather than from
	// a timer held in memory. A panel that restarts every hour would otherwise
	// never reach its own daily interval, and would take no backups at all.
	list, err := s.svc.List()
	if err != nil {
		s.svc.log.Warn("could not read the backup directory", "error", err)
		return
	}
	if len(list) > 0 && time.Since(list[0].Taken) < every {
		return
	}

	archive, err := s.svc.Create(ctx)
	if err != nil {
		s.svc.log.Warn("scheduled backup failed", "error", err)
		return
	}
	if s.OnBackup != nil {
		s.OnBackup(archive)
	}
}
