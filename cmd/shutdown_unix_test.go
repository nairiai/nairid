//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
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
