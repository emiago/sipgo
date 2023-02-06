package sip

import "testing"

func BenchmarkGenerateBranch(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		val := GenerateBranch()
		if len(val) != 32+len(RFC3261BranchMagicCookie)+1 {
			b.Fatal("wrong number of bytes")
		}
	}
}

func BenchmarkGenerateBranch16(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		val := GenerateBranchN(16)
		if len(val) != 32+len(RFC3261BranchMagicCookie)+1 {
			b.Fatal("wrong number of bytes")
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
