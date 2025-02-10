package sip

import "testing"

type httpHeader map[string][]string

func (h httpHeader) Add(key, val string) {
	h[key] = append(h[key], val)
}

func (h httpHeader) Get(key string) string {
	v := h[key]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

type header struct {
	key   string
	value string
}

type newStyleHeader []header

func (h *newStyleHeader) Add(key, val string) {
	*h = append(*h, header{key, val})
}

func (h newStyleHeader) Get(key string) string {
	for _, v := range h {
		if v.key == key {
			return v.value
		}
	}
	return ""
}

func BenchmarkSIPHeader(b *testing.B) {
	keys := []string{"Via", "Via", "Route", "Route", "Record-Route4", "Max-Forwards5", "Content6", "Content-Type7", "Contact8", "From10", "To11", "X-Custom-Header", "PAI"}

	b.ResetTimer()
	b.Run("httpAdd", func(b *testing.B) {
		// Be more relasti
		for i := 0; i < b.N; i++ {
			h := httpHeader{}
			for _, v := range keys {
				h.Add(v, v)
			}
		}
	})

	b.Run("httpGet", func(b *testing.B) {
		// Be more relasti
		h := httpHeader{}
		for _, v := range keys {
			h.Add(v, v)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			str := keys[i%len(keys)]
			if h.Get(str) == "" {
				b.Fatal("key empty")
			}
		}
	})

	b.Run("newAdd", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			h := newStyleHeader{}
			for _, v := range keys {
				h.Add(v, v)
			}
		}
	})

	b.Run("newAddOptimized", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			h := make(newStyleHeader, 0, 10)
			for _, v := range keys {
				h.Add(v, v)
			}
		}
	})

	b.Run("newGet", func(b *testing.B) {
		// Be more relasti
		h := newStyleHeader{}
		for _, v := range keys {
			h.Add(v, v)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			str := keys[i%len(keys)]
			if h.Get(str) == "" {
				b.Fatal("key empty")
			}
		}
	})

}
