package socketio

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zishang520/socket.io/clients/engine/v3"
	socket "github.com/zishang520/socket.io/clients/socket/v3"
	"github.com/zishang520/socket.io/parsers/engine/v3/packet"
	ioserver "github.com/zishang520/socket.io/servers/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

// fakeEngine is a polling-only Engine.IO server that holds every poll until the test answers it.
type fakeEngine struct {
	srv      *httptest.Server
	release  chan string
	holdPost chan struct{}
	done     chan struct{}
	polls    atomic.Int32
	inflight atomic.Int32
}

func newFakeEngine(t *testing.T, holdPosts bool) *fakeEngine {
	t.Helper()
	f := &fakeEngine{release: make(chan string), done: make(chan struct{})}
	if holdPosts {
		f.holdPost = make(chan struct{})
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		close(f.done)
		f.srv.CloseClientConnections()
		f.srv.Close()
	})
	return f
}

func (f *fakeEngine) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		f.servePost(w, r)
		return
	}
	if r.URL.Query().Get("sid") == "" {
		_, _ = w.Write([]byte(`0{"sid":"sid_123","upgrades":[],"pingInterval":25000,"pingTimeout":20000,"maxPayload":1000000}`))
		return
	}
	f.polls.Add(1)
	f.inflight.Add(1)
	defer f.inflight.Add(-1)
	select {
	case body := <-f.release:
		_, _ = w.Write([]byte(body))
	case <-f.done:
	case <-r.Context().Done():
	}
}

// servePost accepts a write, holding it until the test lets it through when holdPost is set.
func (f *fakeEngine) servePost(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	f.inflight.Add(1)
	defer f.inflight.Add(-1)
	if f.holdPost == nil {
		_, _ = w.Write([]byte("ok"))
		return
	}
	select {
	case <-f.holdPost:
		_, _ = w.Write([]byte("ok"))
	case <-f.done:
	}
}

// open connects a socket and waits until its first long poll is held by the server.
func (f *fakeEngine) open(t *testing.T, ctor engine.TransportCtor) engine.Socket {
	t.Helper()
	opts := engine.DefaultSocketOptions()
	opts.SetTransports(types.NewSet(ctor))
	sock := engine.NewSocket(f.srv.URL, opts)
	t.Cleanup(func() { sock.Close() })
	eventually(t, "first poll held", func() bool { return f.polls.Load() == 1 && f.inflight.Load() == 1 })
	return sock
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if !waitFor(5*time.Second, cond) {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestPauseRacingPollCompletion(t *testing.T) {
	for i := range 100 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			pauseWhilePollCompletes(t, time.Duration(i%10)*30*time.Microsecond)
		})
	}
}

// pauseWhilePollCompletes answers the held poll while pausing, delay apart.
func pauseWhilePollCompletes(t *testing.T, delay time.Duration) {
	f := newFakeEngine(t, false)
	tr := f.open(t, Polling).Transport()

	go func() { f.release <- "6" }()
	time.Sleep(delay)
	inflightAtPause := make(chan int32, 1)
	tr.Pause(func() { inflightAtPause <- f.inflight.Load() })

	if n := f.awaitPause(t, inflightAtPause); n != 0 {
		t.Fatalf("paused with %d requests in flight", n)
	}
	polls := f.polls.Load()
	time.Sleep(20 * time.Millisecond)
	if got := f.polls.Load(); got != polls {
		t.Fatalf("a poll started after the pause: %d -> %d", polls, got)
	}
}

// awaitPause returns the in-flight count seen by the pause, answering earlier polls meanwhile.
func (f *fakeEngine) awaitPause(t *testing.T, paused <-chan int32) int32 {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case n := <-paused:
			return n
		case f.release <- "6":
			// A poll that began before the pause was seen must finish first.
		case <-timeout:
			t.Fatal("pause never completed")
		}
	}
}

func TestPauseWaitsForWrite(t *testing.T) {
	f := newFakeEngine(t, true)
	tr := f.open(t, Polling).Transport()

	tr.Send([]*packet.Packet{{Type: packet.MESSAGE, Data: strings.NewReader("hi")}})
	eventually(t, "write held", func() bool { return f.inflight.Load() == 2 })

	paused := make(chan struct{})
	tr.Pause(func() { close(paused) })
	f.release <- "6"
	select {
	case <-paused:
		t.Fatal("paused while a write was in flight")
	case <-time.After(100 * time.Millisecond):
	}

	f.holdPost <- struct{}{}
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("pause never completed after the write")
	}
}

func TestStuckPauseClosesSocket(t *testing.T) {
	f := newFakeEngine(t, false)
	sock := f.open(t, pollingCtor{pauseTimeout: 50 * time.Millisecond})
	closed := make(chan struct{})
	if err := sock.Once("close", func(...any) { close(closed) }); err != nil {
		t.Fatal(err)
	}

	// Paused, but no upgrade ever takes over the session.
	sock.Transport().Pause(func() {})
	f.release <- "6"
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("socket stayed open on a paused transport")
	}
}

func TestUpgradeAndRoundTrip(t *testing.T) {
	io := ioserver.NewServer(nil, nil)
	err := io.On("connection", func(clients ...any) {
		client, ok := clients[0].(*ioserver.Socket)
		if !ok {
			return
		}
		_ = client.On("echo", func(args ...any) {
			if ack, ok := args[len(args)-1].(func([]any, error)); ok {
				ack(args[:len(args)-1], nil)
			}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(io.ServeHandler(nil))
	t.Cleanup(func() {
		io.Close(nil)
		srv.Close()
	})

	const rounds = 30
	upgraded, dropped := 0, 0
	for i := range rounds {
		up, drop := roundTrip(t, srv.URL, i)
		if up {
			upgraded++
		}
		if drop {
			dropped++
		}
	}
	// The rc.11 test server sometimes ends a session or ignores a probe on its own, mostly on
	// WebSocket, so a few drops pass; a polling session that stops answering fails at once.
	if dropped > 3 || upgraded < rounds/2 {
		t.Fatalf("%d of %d sessions dropped, %d upgraded", dropped, rounds, upgraded)
	}
}

// roundTrip connects, echoes, waits for the upgrade and echoes again, on one session.
func roundTrip(t *testing.T, url string, i int) (upgraded, dropped bool) {
	t.Helper()
	opts := socket.DefaultOptions()
	opts.SetTransports(types.NewSet(Polling, socket.WebSocket))
	opts.SetReconnection(false)
	manager := socket.NewManager(url, opts)
	client := manager.Socket("/", opts)
	defer client.Disconnect()

	if !waitFor(5*time.Second, client.Connected) || !echo(t, client, i) {
		return false, true
	}
	// A probe the server ignored leaves the session on polling, which the next echo covers.
	upgraded = waitFor(3*time.Second, func() bool { return manager.Engine().Transport().Name() == "websocket" })
	return upgraded, !echo(t, client, i)
}

// echo reports whether the reply came back; a session that ended returns false.
func echo(t *testing.T, client *socket.Socket, i int) bool {
	t.Helper()
	type reply struct {
		args []any
		err  error
	}
	got := make(chan reply, 1)
	if err := client.Emit("echo", i, func(args []any, err error) { got <- reply{args, err} }); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		if r.err != nil {
			return false
		}
		if len(r.args) != 1 {
			t.Fatalf("round %d: echo returned %v", i, r.args)
		}
		return true
	case <-time.After(5 * time.Second):
		// A pause that never completes holds the echo on polling: the hang this package fixes.
		if client.Io().Engine().Transport().Name() == pollingName {
			t.Fatalf("round %d: no echo on polling", i)
		}
		return false
	}
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}
