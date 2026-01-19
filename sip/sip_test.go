package sip

import (
	"log/slog"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	SIPDebug = os.Getenv("SIP_DEBUG") == "true"
	TransactionFSMDebug = os.Getenv("TRANSACTION_DEBUG") == "true"

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetLogLoggerLevel(lvl)

	m.Run()
}

func BenchmarkGenerateBranch(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		val := GenerateBranch()
		if len(val) != 16+len(RFC3261BranchMagicCookie)+1 {
			b.Fatal("wrong number of bytes: " + val)
		}
	}
}

func BenchmarkGenerateBranch16(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		val := GenerateBranchN(16)
		if len(val) != 16+len(RFC3261BranchMagicCookie)+1 {
			b.Fatal("wrong number of bytes: " + val)
		}
	}
}

func BenchmarkGenerateBranchBufPool(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		val := GenerateBranchN(16)
		if len(val) != 32+len(RFC3261BranchMagicCookie)+1 {
			b.Fatal("wrong number of bytes")
		}
	}
}
