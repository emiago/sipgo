package sip

import (
	"testing"
)

func BenchmarkHeaderToLower(b *testing.B) {
	//BenchmarkHeaderToLower-8   	1000000000	         1.033 ns/op	       0 B/op	       0 allocs/op
	h := "Content-Type"
	for i := 0; i < b.N; i++ {
		s := HeaderToLower(h)
		if s != "content-type" {
			b.Fatal("Header not lowered")
		}
	}
}
