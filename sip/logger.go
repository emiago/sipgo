package sip

import (
	"log/slog"
	"sync/atomic"
)

var defLogger atomic.Pointer[slog.Logger]

// SetDefaultLogger sets default logger that will be used withing sip package
// It is safe to call while the library is in use.
func SetDefaultLogger(l *slog.Logger) {
	defLogger.Store(l)
}

func DefaultLogger() *slog.Logger {
	if l := defLogger.Load(); l != nil {
		return l
	}
	return slog.Default()
}
