package update

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Installing in the background.
//
// A release is tens of megabytes, and a server whose way to GitHub is slow
// takes minutes to fetch it -- longer than the panel lets one request run. An
// install done inside the request went through, and the browser that asked
// for it was told it had failed. So the request starts the install and answers
// at once, and the page follows it through Status until the panel comes back
// on the new build.

// Stage is how far an install has got.
type Stage string

const (
	StageDownloading Stage = "downloading"
	StageVerifying   Stage = "verifying"
	StageInstalling  Stage = "installing"
	// StageRestarting: the new binary is in place and the process is about to
	// end, for the service manager to start it again on the new build.
	StageRestarting Stage = "restarting"
	StageFailed     Stage = "failed"
)

// Progress is the install in hand, or the last one if it failed. The zero
// value is no install since this process started.
type Progress struct {
	Stage Stage  `json:"stage,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	// Received and Total are bytes of the panel's binary. Total is zero when
	// the server did not say how long it is.
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Error    string `json:"error,omitempty"`
}

// running reports whether an install is under way.
func (p Progress) running() bool {
	switch p.Stage {
	case StageDownloading, StageVerifying, StageInstalling, StageRestarting:
		return true
	}
	return false
}

// ErrBusy is a second install asked for while one is under way.
var ErrBusy = errors.New("an update is already being installed")

// installTimeout bounds one install. A release at 40 KB/s still arrives.
const installTimeout = 30 * time.Minute

var job struct {
	sync.Mutex
	p Progress
}

// Status is the install under way, or how the last one ended.
func Status() Progress {
	job.Lock()
	defer job.Unlock()
	return job.p
}

// Start begins installing rel and returns at once. finished runs when it has
// ended: with nil once the new binary is in place, or with the reason nothing
// was installed. restartSelf says whether the caller has to end the process
// for the service manager to start it on the new build; when the root helper
// installed it, the helper restarts the panel itself.
//
// What can be known without the network is refused here rather than later,
// so the request that asked is the one told.
func Start(rel *Release, from string, finished func(err error, restartSelf bool)) error {
	if _, err := signingKey(); err != nil {
		return err
	}
	m := mode()
	if m == installNone {
		return ErrCannotInstall
	}
	if rel.signatureURL == "" {
		return errNoSignature
	}

	job.Lock()
	if job.p.running() {
		job.Unlock()
		return ErrBusy
	}
	job.p = Progress{Stage: StageDownloading, From: from, To: rel.Version}
	job.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
		defer cancel()
		restartSelf, err := apply(ctx, rel, m, track)

		job.Lock()
		if err != nil {
			job.p.Stage, job.p.Error = StageFailed, err.Error()
		} else {
			job.p.Stage = StageRestarting
		}
		job.Unlock()
		if finished != nil {
			finished(err, restartSelf)
		}
	}()
	return nil
}

// track is apply's report on the install under way.
func track(stage Stage, received, total int64) {
	job.Lock()
	defer job.Unlock()
	job.p.Stage = stage
	if stage == StageDownloading {
		job.p.Received, job.p.Total = received, total
	}
}
