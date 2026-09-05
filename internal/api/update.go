package api

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/abolfazl/w-ui/internal/nodes"
	"github.com/abolfazl/w-ui/internal/update"
)

// Updating this panel, and asking a node to update itself.
//
// The managing panel never sends a node a binary. It asks the node to update
// itself, and the node fetches the release from the project's repository and
// checks its signature before installing it. The difference is the whole
// security of the arrangement: taking the managing panel gets you nodes running
// an official release, not code of your choosing on every machine.

// updateRestartDelay lets the answer reach the browser before the process ends.
const updateRestartDelay = 750 * time.Millisecond

func (s *Server) handleUpdateAvailable(w http.ResponseWriter, r *http.Request) {
	rel, isNewer, err := update.Available(r.Context(), s.version)
	if err != nil {
		// Not a failure of the panel: the release list being unreachable is
		// something to report, not something to fail a page over.
		writeJSON(w, http.StatusOK, map[string]any{
			"current": s.version,
			"signed":  update.Signed(),
			"notice":  err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current":   s.version,
		"latest":    rel.Version,
		"available": isNewer,
		"notes":     rel.Notes,
		"published": rel.Published,
		// Said before the button is offered. A build with no key cannot install
		// anything, and finding that out by pressing update is worse than being
		// told.
		"signed": update.Signed(),
	})
}

func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	// Checked before anything is fetched, so the answer names the real blocker.
	// Asking the release list first would tell an operator their repository has
	// no releases when the actual reason is that this build could not install
	// one anyway.
	if !update.Signed() {
		writeError(w, http.StatusPreconditionFailed, update.ErrNoKey.Error())
		return
	}

	rel, isNewer, err := update.Available(r.Context(), s.version)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !isNewer {
		writeJSON(w, http.StatusOK, map[string]any{
			"updated": false,
			"current": s.version,
			"notice":  update.ErrUpToDate.Error(),
		})
		return
	}

	if err := update.Apply(r.Context(), rel); err != nil {
		switch {
		case errors.Is(err, update.ErrNoKey):
			writeError(w, http.StatusPreconditionFailed, err.Error())
		case errors.Is(err, update.ErrBadSignature):
			// Loud. A download that fails this is either a broken release or
			// somebody standing between this panel and the project, and both
			// are worth an operator's attention.
			s.log.Error("a downloaded update was not signed by this project; nothing was installed",
				"version", rel.Version, "by", adminName(r), "ip", clientIP(r))
			writeError(w, http.StatusBadGateway, err.Error())
		default:
			fail(w, s.log, err)
		}
		return
	}

	s.log.Warn("the panel was updated and is restarting",
		"from", s.version, "to", rel.Version, "by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, map[string]any{
		"updated":    true,
		"from":       s.version,
		"to":         rel.Version,
		"restarting": true,
	})

	// The binary on disk is now a different one from the one running. Ending
	// the process is the whole of the reload; the service manager brings it
	// back on the new build, the same way a restore does.
	go func() {
		time.Sleep(updateRestartDelay)
		s.log.Warn("exiting to come back on the new build")
		os.Exit(0)
	}()
}

// handleUpgradeNode asks one node to update its own panel.
//
// Named apart from handleUpdateNode, which edits a node's settings: one changes
// a row, the other replaces a binary on another machine.
func (s *Server) handleUpgradeNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	res, err := nodes.AskToUpdate(r.Context(), s.db, id)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	s.log.Warn("a node was asked to update itself",
		"node", id, "result", res, "by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, res)
}
