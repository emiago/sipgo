package sipgo

import (
	"testing"
)

func BenchmarkSwitchVsMap(b *testing.B) {

	b.Run("map", func(b *testing.B) {
		m := map[string]RequestHandler{
			"INVITE":    nil,
			"ACK":       nil,
			"CANCEL":    nil,
			"BYE":       nil,
			"REGISTER":  nil,
			"OPTIONS":   nil,
			"SUBSCRIBE": nil,
			"NOTIFY":    nil,
			"REFER":     nil,
			"INFO":      nil,
			"MESSAGE":   nil,
			"PRACK":     nil,
			"UPDATE":    nil,
			"PUBLISH":   nil,
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := m["REGISTER"]; !ok {
				b.FailNow()
			}

			if _, ok := m["PUBLISH"]; !ok {
				b.FailNow()
			}
		}
	})

	b.Run("switch", func(b *testing.B) {
		m := func(s string) (RequestHandler, bool) {
			switch s {
			case "INVITE":
				return nil, true
			case "ACK":
				return nil, true
			case "CANCEL":
				return nil, true
			case "BYE":
				return nil, true
			case "REGISTER":
				return nil, true
			case "OPTIONS":
				return nil, true
			case "SUBSCRIBE":
				return nil, true
			case "NOTIFY":
				return nil, true
			case "REFER":
				return nil, true
			case "INFO":
				return nil, true
			case "MESSAGE":
				return nil, true
			case "PRACK":
				return nil, true
			case "UPDATE":
				return nil, true
			case "PUBLISH":
				return nil, true

			}
			return nil, false
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := m("REGISTER"); !ok {
				b.FailNow()
			}

			if _, ok := m("NOTIFY"); !ok {
				b.FailNow()
			}
		}
	})

}
