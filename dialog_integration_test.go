package sipgo

import (
	"context"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"
)

func TestIntegrationDialog(t *testing.T) {
	ua, _ := NewUA()
	defer ua.Close()
	srv, _ := NewServer(ua)
	cli, _ := NewClient(ua)

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}

	dialogSrv := NewDialogServer(cli, uasContact)

	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		dtx := dialogSrv.ReadInvite(req, tx)

		err := dtx.WriteResponse(sip.StatusTrying, "Trying", nil)
		require.Nil(t, err)

		err = dtx.WriteResponse(sip.StatusRinging, "Ringing", nil)
		require.Nil(t, err)

		err = dtx.WriteResponse(sip.StatusOK, "OK", nil)
		require.Nil(t, err)

		// <-dtx.Done()
	})

	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
		dialogSrv.ReadAck(req, tx)
	})

	srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		dialogSrv.ReadBye(req, tx)
	})

	srv.ServeRequest(func(r *sip.Request) {
		t.Log("UAS: ", r.StartLine())
	})

	srvReady := make(chan struct{})
	ctx := context.WithValue(context.Background(), ListenReadyCtxKey, ListenReadyCtxValue(srvReady))
	go srv.ListenAndServe(ctx, "udp", uasContact.Address.HostPort())
	// Wait server to be ready
	<-srvReady

	// Client
	{
		ua, err := NewUA()
		defer ua.Close()

		srv, _ := NewServer(ua)
		cli, _ := NewClient(ua)

		contactHDR := sip.ContactHeader{
			Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5088},
		}

		srvReady := make(chan struct{})
		ctx := context.WithValue(context.Background(), ListenReadyCtxKey, ListenReadyCtxValue(srvReady))
		go srv.ListenAndServe(ctx, "udp", contactHDR.Address.HostPort())
		// Wait server to be ready
		<-srvReady

		dialogCli := NewDialogClient(cli, contactHDR)

		// INVITE
		req := sip.NewRequest(sip.INVITE, uasContact.Address.Clone())
		t.Log("UAC: ", req.StartLine())

		sess, err := dialogCli.WriteInvite(context.TODO(), req)
		require.NoError(t, err)
		require.Equal(t, sip.StatusOK, sess.Response.StatusCode)

		// ACK
		{
			t.Log("UAC: ACK")
			err := sess.Ack(context.TODO())
			require.NoError(t, err)
		}
		// BYE
		{
			t.Log("UAC: BYE")
			err := sess.Bye(context.TODO())
			require.NoError(t, err)
		}
	}

}

func readResponse(tx sip.ClientTransaction) (*sip.Response, error) {
	for {
		select {
		case r := <-tx.Responses():
			return r, nil
		case <-tx.Done():
			return nil, tx.Err()
		}
	}
}
