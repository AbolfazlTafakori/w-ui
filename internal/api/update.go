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

// updateRestartDelay is how long a panel that has installed an update keeps
// answering before it ends, so the page following the install reads that it
// is restarting. The page asks every second.
const updateRestartDelay = 2500 * time.Millisecond

func (s *Server) handleUpdateAvailable(w http.ResponseWriter, r *http.Request) {
	// The overview asks on every visit and is answered from the last check;
	// opening the update dialog asks for a fresh one.
	fresh := r.URL.Query().Get("fresh") == "1"
	rel, isNewer, err := update.Checked(r.Context(), s.version, fresh)
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

	rel, isNewer, err := update.Checked(r.Context(), s.version, true)
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

	by, ip := adminName(r), clientIP(r)
	err = update.Start(rel, s.version, func(err error) {
		if err != nil {
			if errors.Is(err, update.ErrBadSignature) {
				// Loud. A download that fails this is either a broken release
				// or somebody standing between this panel and the project, and
				// both are worth an operator's attention.
				s.log.Error("a downloaded update was not signed by this project; nothing was installed",
					"version", rel.Version, "by", by, "ip", ip)
			} else {
				s.log.Error("the update was not installed", "version", rel.Version, "error", err)
			}
			return
		}
		s.log.Warn("the panel was updated and is restarting", "from", s.version, "to", rel.Version, "by", by, "ip", ip)
		// The binary on disk is now a different one from the one running.
		// Ending the process is the whole of the reload; the service manager
		// brings it back on the new build, the same way a restore does. Not
		// at once: the page following the install is given the time to read
		// that it is restarting rather than finding the panel simply gone.
		time.Sleep(updateRestartDelay)
		s.log.Warn("exiting to come back on the new build")
		os.Exit(0)
	})
	switch {
	case errors.Is(err, update.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, update.ErrNoKey):
		writeError(w, http.StatusPreconditionFailed, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	s.log.Warn("installing an update", "from", s.version, "to", rel.Version, "by", by, "ip", ip)
	// Answered now, while the release downloads: fetching it can take longer
	// than a request is allowed to run. The page follows it at
	// /api/system/update/progress.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": true,
		"from":    s.version,
		"to":      rel.Version,
	})
}

// handleUpdateProgress is the install under way, and the version running --
// which is how a page following an install tells the panel came back on the
// new build.
func (s *Server) handleUpdateProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"current":  s.version,
		"progress": update.Status(),
	})
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
