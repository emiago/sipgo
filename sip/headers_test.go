package sip

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		for i := 0; i < b.N; i++ {
			newOrder := make([]Header, 1, len(hs.headerOrder)+1)
			newOrder[0] = header
			hs.headerOrder = append(newOrder, hs.headerOrder...)
		}
	})

	// Our version must be faster than GOSIP
	b.Run("Assign", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			newOrder := make([]Header, len(hs.headerOrder)+1)
			newOrder[0] = header
			for i, h := range hs.headerOrder {
				newOrder[i+1] = h
			}
			hs.headerOrder = newOrder
		}
	})
}

func TestLazyParsing(t *testing.T) {
	headers := new(headers)

	t.Run("Contact", func(t *testing.T) {
		headers.AppendHeader(NewHeader("Contact", "<sip:alice@example.com>"))
		h := headers.Contact()
		require.NotNil(t, h)
		require.Equal(t, "<sip:alice@example.com>", h.Value())
	})

	t.Run("Via", func(t *testing.T) {
		headers.AppendHeader(NewHeader("Via", "SIP/2.0/UDP 10.1.1.1:5060;branch=z9hG4bKabcdef"))
		h := headers.Via()
		require.NotNil(t, h)
		require.Equal(t, "SIP/2.0/UDP 10.1.1.1:5060;branch=z9hG4bKabcdef", h.Value())
	})

}

func BenchmarkLazyParsing(b *testing.B) {
	headers := new(headers)
	headers.AppendHeader(NewHeader("Contact", "<sip:alice@example.com>"))

	for i := 0; i < b.N; i++ {
		c := headers.Contact()
		if c == nil {
			b.Fatal("contact is nil")
		}
		headers.contact = nil
	}
}

func TestMaxForwardIncDec(t *testing.T) {
	maxfwd := MaxForwardsHeader(70)
	maxfwd.Dec()
	assert.Equal(t, uint32(69), maxfwd.Val(), "Value returned %d", maxfwd.Val())
}
