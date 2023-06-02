package sip

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrependHeader(t *testing.T) {
	hs := headers{}

	hs.PrependHeader(&ViaHeader{})
	assert.Equal(t, 1, len(hs.headerOrder))

	v := &ViaHeader{}
	hs.PrependHeader(v.Clone())
	assert.Equal(t, 2, len(hs.headerOrder))
	assert.Equal(t, v, hs.GetHeader("via"))
}

func BenchmarkHeadersPrepend(b *testing.B) {
	callID := CallIDHeader("aaaa")
	hs := headers{
		headerOrder: []Header{
			&ViaHeader{},
			&FromHeader{},
			&ToHeader{},
			&CSeqHeader{},
			&callID,
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

func TestMaxForwardIncDec(t *testing.T) {
	maxfwd := MaxForwardsHeader(70)
	maxfwd.Dec()
	assert.Equal(t, uint32(69), maxfwd.Val(), "Value returned %d", maxfwd.Val())
}
