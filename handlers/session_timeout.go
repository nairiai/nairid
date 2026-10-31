package handlers

import (
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
	"nairid/models"
)

// timeoutForTurn is how long one CLI run of the job may take. It is resolved on every turn so
// a change to NAIRI_MAX_SESSION_MS applies to jobs without their own value.
func timeoutForTurn(jobID string, sessionTimeoutMs int64) time.Duration {
	timeout := clients.ResolveSessionTimeout(sessionTimeoutMs)
	source := "default"
	if sessionTimeoutMs > 0 {
		source = "agent setting"
	}
	log.Info("⏱️ Session timeout for job %s: %s (%s)", jobID, core.FormatTimeout(timeout), source)
	return timeout
}

// latestSessionTimeoutMs prefers the value on the incoming message, so raising the timeout and
// replying "continue" runs the next turn on the new value. An old backend sends none.
func latestSessionTimeoutMs(messageMs, storedMs int64) int64 {
	if messageMs > 0 {
		return messageMs
	}
	return storedMs
}

// A turn killed at its session timeout leaves its session to resume, so a reply after raising
// the timeout continues it instead of starting over in a fresh worktree.
func sessionLeftByTimeout(runErr error) string {
	timeoutErr, ok := core.IsSessionTimeout(runErr)
	if !ok {
		return ""
	}
	return timeoutErr.SessionID
}

// The turn that resumes a timed-out session runs `git pull --rebase`, which refuses a dirty
// worktree, so the work is committed first.
func (mh *MessageHandler) saveTimedOutWork(jobData models.JobData, runErr error) {
	if sessionLeftByTimeout(runErr) == "" {
		return
	}
	mh.saveUnfinishedWork(jobData, "The run timed out, and saving its work so far failed")
}
