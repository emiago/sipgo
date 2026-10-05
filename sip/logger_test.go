package sip

import (
	"io"
	"log/slog"
	"testing"
)

// Run with -race.
func TestSetDefaultLoggerConcurrent(t *testing.T) {
	prev := DefaultLogger()
	t.Cleanup(func() { SetDefaultLogger(prev) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			DefaultLogger().Debug("read while set")
		}
	}()
	SetDefaultLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	<-done
}
