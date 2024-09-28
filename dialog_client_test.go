package sipgo

import (
	"context"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialogClientRequestRecordRouteHeaders(t *testing.T) {
	ua, _ := NewUA()
	client, _ := NewClient(ua)

	invite := sip.NewRequest(sip.INVITE, sip.Uri{User: "test", Host: "localhost"})
	err := clientRequestBuildReq(client, invite)
	require.NoError(t, err)

	t.Run("LooseRouting", func(t *testing.T) {

		resp := sip.NewResponseFromRequest(invite, 200, "OK", nil)
		// Fake some proxy headers
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p2.com;lr>"))
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p1.com;lr>"))

		s := DialogClientSession{
			ua: &DialogUA{
				Client: client,
			},
			Dialog: Dialog{
				InviteRequest:  invite,
				InviteResponse: resp,
			},
		}
		req := sip.NewRequest(sip.BYE, sip.Uri{User: "test", Host: "localhost"})

		// Send canceled request
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s.Do(ctx, req)

		assert.Equal(t, "<sip:p1.com;lr>", req.Route().Value())
		assert.Equal(t, "<sip:p2.com;lr>", req.GetHeaders("Route")[1].Value())
	})

	t.Run("StrictRouting", func(t *testing.T) {

		resp := sip.NewResponseFromRequest(invite, 200, "OK", nil)
		// Fake some proxy headers
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p2.com;lr>"))
		resp.AppendHeader(sip.NewHeader("Record-Route", "<sip:p1.com>"))

		s := DialogClientSession{
			ua: &DialogUA{
				Client: client,
			},
			Dialog: Dialog{
				InviteRequest:  invite,
				InviteResponse: resp,
			},
		}
		req := sip.NewRequest(sip.BYE, sip.Uri{User: "test", Host: "localhost"})

		// Send canceled request
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s.Do(ctx, req)

		assert.Equal(t, "sip:p1.com", req.Recipient.String())
		assert.Equal(t, "<sip:p1.com>", req.Route().Value())
		assert.Equal(t, "<sip:p2.com;lr>", req.GetHeaders("Route")[1].Value())
	})

}
