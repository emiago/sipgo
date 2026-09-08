package sip

import "log/slog"

var (
	defLogger *slog.Logger
)

// SetDefaultLogger sets default logger that will be used withing sip package
// Must be called before any usage of library
func SetDefaultLogger(l *slog.Logger) {
	defLogger = l
}

func DefaultLogger() *slog.Logger {
	if defLogger != nil {
		return defLogger
	}
	return slog.Default()
}
