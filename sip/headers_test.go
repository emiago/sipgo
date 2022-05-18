package sip

import "testing"

func BenchmarkHeadersPrepend(b *testing.B) {
	callid := CallID("asdas")
	hs := headers{
		headerOrder: []Header{
			&ViaHeader{},
			&FromHeader{},
			&ToHeader{},
			&CSeq{},
			&callid,
			&ContactHeader{},
		},
	}

	var header Header = &ViaHeader{}

	b.Run("Append", func(b *testing.B) {
		newOrder := make([]Header, 1, len(hs.headerOrder)+1)
		newOrder[0] = header
		hs.headerOrder = append(newOrder, hs.headerOrder...)
	})

	// Our version must be faster than GOSIP
	b.Run("Assign", func(b *testing.B) {
		newOrder := make([]Header, len(hs.headerOrder)+1)
		newOrder[0] = header
		for i, h := range hs.headerOrder {
			newOrder[i+1] = h
		}
		hs.headerOrder = newOrder
	})
}
