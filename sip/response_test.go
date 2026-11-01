package sip

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewResponseFromRequestRecordRoute(t *testing.T) {
	// RFC 3261 Table 3, with the early-INVITE rule from §12.1–12.1.1.
	for _, tc := range []struct {
		method RequestMethod
		status int
		copy   bool
	}{
		{INVITE, 100, false},
		{INVITE, 101, true},
		{INVITE, 180, true},
		{INVITE, 183, true},
		{INVITE, 200, true},
		{INVITE, 302, false},
		{INVITE, 487, false},
		{INVITE, 500, false},
		{INVITE, 603, false},
		{BYE, 200, true},
		{CANCEL, 200, true},
		{OPTIONS, 200, true},
		{OPTIONS, 183, true},
		{OPTIONS, 199, false},
		{REGISTER, 200, false},
		{ACK, 200, false},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.method, tc.status), func(t *testing.T) {
			req := NewRequest(tc.method, Uri{Host: "example.com"})
			req.AppendHeader(NewHeader("Record-Route", "<sip:p1.example.com;lr;transport=udp>"))
			req.AppendHeader(NewHeader("Record-Route", "<sip:p2.example.com;lr>"))

			res := NewResponseFromRequest(req, tc.status, "Test", nil)
			routes := res.GetHeaders("Record-Route")
			if !tc.copy {
				require.Empty(t, routes)
				return
			}

			require.Len(t, routes, 2)
			require.Equal(t, "<sip:p1.example.com;lr;transport=udp>", routes[0].Value())
			require.Equal(t, "<sip:p2.example.com;lr>", routes[1].Value())
		})
	}
}
