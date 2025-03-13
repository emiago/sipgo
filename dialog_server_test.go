package sipgo

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/emiago/sipgo/fakes"
	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialogServerByeRequest(t *testing.T) {
	ua, _ := NewUA()
	defer ua.Close()
	cli, _ := NewClient(ua)

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}
	dialogSrv := NewDialogServerCache(cli, uasContact)

	invite, _, _ := createTestInvite(t, "sip:uas@uas.com", "udp", "uas.com:5090")
	invite.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uas", Port: 1234}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P1", Port: 5060}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P2", Port: 5060}})
	invite.AppendHeader(&sip.RecordRouteHeader{Address: sip.Uri{Host: "P3", Port: 5060}})

	dialog, err := dialogSrv.ReadInvite(invite, sip.NewServerTx("test", invite, nil, slog.Default()))
	require.NoError(t, err)

	res := sip.NewResponseFromRequest(invite, sip.StatusOK, "OK", nil)
	res.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uac", Port: 9876}})

	bye := sip.NewRequest(sip.BYE, invite.Contact().Address)
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	// No execution
	dialog.TransactionRequest(ctxCanceled, bye)
	require.Equal(t, invite.CallID(), bye.CallID())

	routes := bye.GetHeaders("Route")
	assert.Equal(t, "<sip:P1:5060>", routes[0].Value())
	assert.Equal(t, "<sip:P2:5060>", routes[1].Value())
	assert.Equal(t, "<sip:P3:5060>", routes[2].Value())
}

func TestDialogServerTransactionCanceled(t *testing.T) {
	sip.Timer_H = 0

	ua, _ := NewUA()
	defer ua.Close()
	cli, _ := NewClient(ua)

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}
	dialogSrv := NewDialogServerCache(cli, uasContact)

	invite, _, _ := createTestInvite(t, "sip:uas@127.0.0.1", "udp", "127.0.0.1:5090")
	invite.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uas", Port: 1234}})

	t.Run("TerminatedEarly", func(t *testing.T) {
		tx := sip.NewServerTx("test", invite, nil, slog.Default())
		tx.Terminate()
		_, err := dialogSrv.ReadInvite(invite, tx)
		require.Error(t, err)
		require.ErrorIs(t, err, sip.ErrTransactionTerminated)
	})

	t.Run("TerminatedByCancel", func(t *testing.T) {
		conn := &sip.UDPConnection{
			PacketConn: &fakes.UDPConn{
				Writers: map[string]io.Writer{
					"127.0.0.1:5090": bytes.NewBuffer(make([]byte, 0)),
				},
			},
		}
		tx := sip.NewServerTx("test", invite, conn, slog.Default())
		tx.Init()
		err := tx.Receive(newCancelRequest(invite))
		require.NoError(t, err)
		d, err := dialogSrv.ReadInvite(invite, tx)
		require.NoError(t, err)
		<-d.Context().Done()
		require.ErrorIs(t, context.Cause(d.Context()), sip.ErrTransactionCanceled)
	})

}
