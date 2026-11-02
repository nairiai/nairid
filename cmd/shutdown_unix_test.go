//go:build !windows

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	ioserver "github.com/zishang520/socket.io/servers/socket/v3"
)

func TestShutdownContextEndsOnSIGTERMAndSIGINT(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, stop := newShutdownContext()
			defer stop()

			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatalf("failed to send %s: %v", sig, err)
			}

			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatalf("shutdown context not cancelled by %s", sig)
			}
		})
	}
}

// The socket.io engine only installs its own SIGTERM handler outside .test binaries, so these
// tests run a built nairid against a fake backend.

func TestSIGTERMExitsWhileConnected(t *testing.T) {
	backend := newShutdownBackend(t, true)
	daemon := startNairid(t, backend.srv.URL)

	select {
	case <-backend.connected:
	case <-time.After(30 * time.Second):
		t.Fatalf("nairid never connected:\n%s", daemon.output())
	}

	daemon.terminateAndWait(t)
	if got := backend.attempts.Load(); got != 1 {
		t.Fatalf("expected 1 connection attempt, got %d:\n%s", got, daemon.output())
	}
}

func TestSIGTERMExitsDuringReconnectBackoff(t *testing.T) {
	backend := newShutdownBackend(t, false)
	daemon := startNairid(t, backend.srv.URL)

	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(daemon.output(), "Retrying in") {
		if time.Now().After(deadline) {
			t.Fatalf("nairid never backed off:\n%s", daemon.output())
		}
		time.Sleep(50 * time.Millisecond)
	}
	attempts := backend.attempts.Load()

	daemon.terminateAndWait(t)
	if got := backend.attempts.Load(); got != attempts {
		t.Fatalf("expected no connection attempt after SIGTERM, got %d more:\n%s", got-attempts, daemon.output())
	}
}

type shutdownBackend struct {
	srv       *httptest.Server
	attempts  atomic.Int32
	connected chan struct{}
}

// newShutdownBackend answers the startup API calls. With acceptSocket false, every socket.io
// handshake fails, so nairid keeps backing off.
func newShutdownBackend(t *testing.T, acceptSocket bool) *shutdownBackend {
	t.Helper()
	b := &shutdownBackend{connected: make(chan struct{})}
	io := ioserver.NewServer(nil, nil)
	var once sync.Once
	if err := io.On("connection", func(...any) { once.Do(func() { close(b.connected) }) }); err != nil {
		t.Fatal(err)
	}
	socketHandler := io.ServeHandler(nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents/env", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"env_vars":[]}`))
	})
	mux.HandleFunc("/api/agents/artifacts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/socket.io/", func(w http.ResponseWriter, r *http.Request) {
		// A new engine.io session has no sid yet; later polls and the upgrade carry one.
		if r.URL.Query().Get("sid") == "" {
			b.attempts.Add(1)
		}
		if !acceptSocket {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		socketHandler.ServeHTTP(w, r)
	})
	b.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		io.Close(nil)
		b.srv.Close()
	})
	return b
}

var (
	nairidBinOnce sync.Once
	nairidBin     string
	nairidBinErr  error
)

// buildNairid builds the daemon once per test run. The name must not end in .test, or the
// socket.io engine skips the signal handler these tests exist for.
func buildNairid(t *testing.T) string {
	t.Helper()
	nairidBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nairid-shutdown-test")
		if err != nil {
			nairidBinErr = err
			return
		}
		nairidBin = filepath.Join(dir, "nairid")
		out, err := exec.Command("go", "build", "-o", nairidBin, ".").CombinedOutput()
		if err != nil {
			nairidBinErr = &buildError{err: err, out: string(out)}
		}
	})
	if nairidBinErr != nil {
		t.Fatalf("failed to build nairid: %v", nairidBinErr)
	}
	return nairidBin
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

type nairidProcess struct {
	cmd  *exec.Cmd
	out  *syncBuffer
	done chan error
}

func startNairid(t *testing.T, backendURL string) *nairidProcess {
	t.Helper()
	bin := buildNairid(t)
	home := t.TempDir()
	workDir := t.TempDir()

	out := &syncBuffer{}
	cmd := exec.Command(bin, "--agent=claude")
	cmd.Dir = workDir
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"NAIRI_API_KEY=nairid_test",
		"NAIRI_AGENT_ID=shutdown-test",
		"NAIRI_WS_API_URL=" + backendURL,
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start nairid: %v", err)
	}

	p := &nairidProcess{cmd: cmd, out: out, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-p.done
	})
	return p
}

func (p *nairidProcess) output() string { return p.out.String() }

// terminateAndWait sends SIGTERM and fails unless nairid exits cleanly in time.
func (p *nairidProcess) terminateAndWait(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}
	select {
	case err := <-p.done:
		p.done <- err
		if err != nil {
			t.Fatalf("nairid exited with %v after SIGTERM:\n%s", err, p.output())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("nairid still running 15s after SIGTERM:\n%s", p.output())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
