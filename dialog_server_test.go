package sipgo

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/siptest"
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
	// sip.Timer_H = 0

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
		d, err := dialogSrv.ReadInvite(invite, tx)
		require.NoError(t, err)

		err = tx.Receive(newCancelRequest(invite))
		require.NoError(t, err)
		// Context dialog will be terminated and in this case
		// cause of context cancelation could be found
		<-d.Context().Done()
		require.ErrorIs(t, d.err(), sip.ErrTransactionCanceled)
	})

	t.Run("TerminatedByCancelBeforeReadingInvite", func(t *testing.T) {
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
		_, err = dialogSrv.ReadInvite(invite, tx)
		require.ErrorIs(t, err, sip.ErrTransactionCanceled)
	})

}

func TestDialogServerRequestsWithinDialog(t *testing.T) {
	// https://datatracker.ietf.org/doc/html/rfc3261#section-12.2.2

	ua, _ := NewUA()
	defer ua.Close()
	cli, _ := NewClient(ua)

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}
	dialogSrv := NewDialogServerCache(cli, uasContact)

	invite, _, _ := createTestInvite(t, "sip:uas@127.0.0.1", "udp", "127.0.0.1:5090")
	invite.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uas", Port: 1234}})

	t.Run("InvalidCseq", func(t *testing.T) {
		// This covers issue explained as
		// https://github.com/emiago/sipgo/issues/187
		conn := &sip.UDPConnection{
			PacketConn: &fakes.UDPConn{
				Writers: map[string]io.Writer{
					"127.0.0.1:5090": bytes.NewBuffer(make([]byte, 0)),
				},
			},
		}
		tx := sip.NewServerTx("test", invite, conn, slog.Default())
		tx.Init()

		dialog, err := dialogSrv.ReadInvite(invite, tx)
		require.NoError(t, err)
		defer dialog.Close()

		byeWrongCseq := newByeRequestUAC(invite, sip.NewResponseFromRequest(invite, 200, "OK", nil), nil)
		byeWrongCseq.CSeq().SeqNo--
		tx = sip.NewServerTx("test", byeWrongCseq, conn, slog.Default())
		tx.Init()
		err = dialog.ReadBye(byeWrongCseq, tx)
		require.ErrorIs(t, err, ErrDialogInvalidCseq)
	})

	t.Run("TerminateAfterSentRequest", func(t *testing.T) {
		// This covers issue explained as
		// https://github.com/emiago/sipgo/issues/187
		conn := &sip.UDPConnection{
			PacketConn: &fakes.UDPConn{
				Writers: map[string]io.Writer{
					"127.0.0.1:5090": bytes.NewBuffer(make([]byte, 0)),
				},
			},
		}
		tx := sip.NewServerTx("test", invite, conn, slog.Default())
		tx.Init()

		dialog, err := dialogSrv.ReadInvite(invite, tx)
		require.NoError(t, err)
		defer dialog.Close()

		reinvite := sip.NewRequest(sip.INVITE, invite.From().Address)
		_, err = dialog.TransactionRequest(context.TODO(), reinvite)
		require.NoError(t, err)

		bye := newByeRequestUAC(invite, sip.NewResponseFromRequest(invite, 200, "OK", nil), nil)
		tx = sip.NewServerTx("test-bye", bye, conn, slog.Default())
		tx.Init()
		err = dialog.ReadBye(bye, tx)
		require.NoError(t, err)
	})
}

func TestDialogServer2xxRetransmission(t *testing.T) {
	// sip.T1 = 1
	ua, _ := NewUA()
	defer ua.Close()
	cli, _ := NewClient(ua)

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}
	dialogSrv := NewDialogServerCache(cli, uasContact)

	invite, _, _ := createTestInvite(t, "sip:uas@127.0.0.1", "udp", "127.0.0.1:5090")
	invite.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Host: "uas", Port: 1234}})

	// Create a server transcation
	tx := siptest.NewServerTxRecorder(invite)

	// Read Invite
	d, err := dialogSrv.ReadInvite(invite, tx)
	require.NoError(t, err)

	res200 := sip.NewResponseFromRequest(d.InviteRequest, 200, "OK", nil)
	ackReceive := newAckRequestUAC(d.InviteRequest, res200, nil)
	go func() {
		// Delay ACK receiving
		time.Sleep(2 * sip.T1)
		d.ReadAck(ackReceive, tx)
	}()
	// Respond 200
	// This will block until ACK
	err = d.WriteResponse(res200)
	require.NoError(t, err)

	// We must have at least 2 responses
	resps := tx.Result()
	require.Len(t, resps, 2)
}
