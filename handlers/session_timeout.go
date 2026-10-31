package handlers

import (
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
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
