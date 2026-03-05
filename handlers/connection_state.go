package handlers

import (
	"sync"

	"nairid/core/log"
)

// ConnectionState manages the Socket.IO connection state and provides
// blocking mechanisms for goroutines that need to wait for connection.
type ConnectionState struct {
	mutex     sync.Mutex
	cond      *sync.Cond
	connected bool
}

// NewConnectionState creates a new ConnectionState instance.
// Initial state is disconnected.
func NewConnectionState() *ConnectionState {
	cs := &ConnectionState{
		connected: false,
	}
	cs.cond = sync.NewCond(&cs.mutex)
	return cs
}

// SetConnected updates the connection state and broadcasts to waiting goroutines.
// When transitioning to connected, all waiting goroutines are unblocked.
func (cs *ConnectionState) SetConnected(connected bool) {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()

	oldState := cs.connected
	cs.connected = connected

	if connected && !oldState {
		log.Info("🔗 ConnectionState: Now connected, broadcasting to waiting senders")
		cs.cond.Broadcast() // Wake up all waiting goroutines
	} else if !connected && oldState {
		log.Info("🔌 ConnectionState: Now disconnected, future sends will block")
	}
}

// WaitForConnection blocks the caller until the connection state is connected.
// If already connected, returns immediately.
func (cs *ConnectionState) WaitForConnection() {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()

	for !cs.connected {
		log.Info("⏸️  ConnectionState: Waiting for connection...")
		cs.cond.Wait() // Releases mutex and blocks until Broadcast/Signal
	}
}

// IsConnected returns the current connection state (thread-safe read).
func (cs *ConnectionState) IsConnected() bool {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	return cs.connected
}
