package sip

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSepToString(t *testing.T) {
	hp := NewParams()
	hp.Add("tag", "aaa")
	hp.Add("branch", "bbb")

	for _, sep := range []uint8{';', '&', '?'} {
		str := hp.ToString(sep)
		arr := strings.Split(str, string(sep))
		assert.Equal(t, strings.Join(arr, string(sep)), str)
	}
}

func BenchmarkHeaderParams(b *testing.B) {

	testParams := func(b *testing.B, hp HeaderParams) {
		hp = hp.Add("branch", "assadkjkgeijdas")
		hp = hp.Add("received", "127.0.0.1")
		hp = hp.Add("toremove", "removeme")
		hp = hp.Remove("toremove")

		if _, exists := hp.Get("received"); !exists {
			b.Fatal("received does not exists")
		}

		s := hp.ToString(';')
		if len(s) == 0 {
			b.Fatal("Params empty")
		}

		if s != "branch=assadkjkgeijdas;received=127.0.0.1" && s != "received=127.0.0.1;branch=assadkjkgeijdas" {
			b.Fatal("Bad parsing")
		}
	}

	// Our version must be faster than GOSIP
	b.Run("MAP", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			hp := NewParams()
			testParams(b, hp)
		}
	})

}

func BenchmarkStringConcetationVsBuffer(b *testing.B) {
	name := "Callid"
	value := "abcdefge1234566"
	// expected := name + ":" + value
	b.ResetTimer()

	b.Run("Concat", func(b *testing.B) {
		var buf strings.Builder
		for i := 0; i < b.N; i++ {
			buf.WriteString(name + ":" + value)
		}
		if buf.Len() == 0 {
			b.FailNow()
		}
	})

	// Our version must be faster than GOSIP
	b.Run("Buffer", func(b *testing.B) {
		var buf strings.Builder
		for i := 0; i < b.N; i++ {
			buf.WriteString(name)
			buf.WriteString(":")
			buf.WriteString(value)
		}
		if buf.Len() == 0 {
			b.FailNow()
		}
	})
}
