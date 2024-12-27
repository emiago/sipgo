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
	assert.Equal(t, v, hs.Via())
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

func TestCopyHeaders(t *testing.T) {
	invite, _, _ := testCreateInvite(t, "sip:bob@example.com", "udp", "test.com")
	invite.AppendHeader(NewHeader("Record-Route", "<sip:p1:5060;lr;transport=udp>"))
	invite.AppendHeader(NewHeader("Record-Route", "<sip:p2:5060;lr>"))

	res := NewResponse(StatusOK, "OK")
	CopyHeaders("Record-Route", invite, res)

	hdrs := res.GetHeaders("Record-Route")
	require.Equal(t, "Record-Route: <sip:p1:5060;lr;transport=udp>", hdrs[0].String())
	require.Equal(t, "Record-Route: <sip:p2:5060;lr>", hdrs[1].String())
}
