package api

import (
	"net/http"
	"os"
	"time"
)

// Restoring a backup, and taking one in from outside.
//
// These are the two halves that were missing. Everything else about backups
// worked — one could be taken, listed, downloaded and deleted — and none of it
// amounted to a backup, because there was no way to put one back. An archive
// you cannot restore is a file that makes an operator feel safe and is not one.

// uploadLimit bounds what will be accepted from a browser.
//
// Generous, because a panel with years of traffic history and a certificate
// authority behind it makes a large archive, and refusing the real thing would
// make the feature useless for exactly the servers that need it most.
const uploadLimit = 512 << 20 // 512 MiB

// restartDelay is how long the process waits before ending itself, so the
// response describing the restore reaches the browser first. A page that gets a
// dropped connection instead cannot tell a restore that worked from one that
// destroyed the panel.
const restartDelay = 750 * time.Millisecond

func (s *Server) handleUploadBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest,
			"that file could not be read, or it is larger than this panel accepts")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("archive")
	if err != nil {
		writeError(w, http.StatusBadRequest, "no file was sent")
		return
	}
	defer file.Close()

	archive, err := s.backups.Accept(file)
	if err != nil {
		// The archive is checked before it is kept, so this is a refusal with a
		// reason rather than a fault.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.log.Warn("a backup archive was uploaded to this panel",
		"stored", archive.Name, "sentAs", header.Filename, "by", adminName(r), "ip", clientIP(r))
	writeJSON(w, http.StatusCreated, archive)
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		http.NotFound(w, r)
		return
	}

	report, err := s.backups.Restore(r.Context(), r.PathValue("name"))
	if err != nil {
		fail(w, s.log, err)
		return
	}

	// Worth a line at warning level whatever else is being logged. This is the
	// single most consequential thing anybody can do from this panel, and the
	// log is where it is answered for afterwards.
	s.log.Warn("the panel was restored from a backup and is restarting",
		"archive", r.PathValue("name"), "files", len(report.Files),
		"safetyCopy", report.SafetyCopy, "by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, map[string]any{
		"files":      report.Files,
		"safetyCopy": report.SafetyCopy,
		"restarting": true,
	})

	// Everything this process holds — the open database, the drivers, the
	// settings it read at startup — belongs to files that have just been
	// replaced. Carrying on with them would write the old state back over the
	// restored one.
	//
	// Ending the process is the whole of the reload: systemd brings it back,
	// which is how the installer sets it up. A panel started some other way has
	// to be started again by hand, and the page says so.
	go func() {
		time.Sleep(restartDelay)
		s.log.Warn("exiting to come back with the restored data")
		os.Exit(0)
	}()
}

// adminName is who is signed in, for a log line that has to name somebody.
func adminName(r *http.Request) string {
	if a := adminFrom(r.Context()); a != nil {
		return a.Username
	}
	return "unknown"
}
