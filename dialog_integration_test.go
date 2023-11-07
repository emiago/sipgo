package sipgo

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"
)

func TestIntegrationDialog(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	ua, _ := NewUA()
	defer ua.Close()
	srv, _ := NewServer(ua)
	cli, _ := NewClient(ua)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}

	dialogSrv := NewDialogServer(cli, uasContact)

	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		dlg := dialogSrv.ReadInvite(req, tx)

		err := dlg.WriteResponse(sip.StatusTrying, "Trying", nil)
		require.Nil(t, err)

		err = dlg.WriteResponse(sip.StatusRinging, "Ringing", nil)
		require.Nil(t, err)

		err = dlg.WriteResponse(sip.StatusOK, "OK", nil)
		require.Nil(t, err)

		<-tx.Done()
	})

	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
		dialogSrv.ReadAck(req, tx)
	})

	srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		dialogSrv.ReadBye(req, tx)
	})

	srv.ServeRequest(func(r *sip.Request) {
		t.Log("UAS server: ", r.StartLine())
	})

	srvReady := make(chan struct{})
	go srv.ListenAndServe(
		context.WithValue(ctx, ListenReadyCtxKey, ListenReadyCtxValue(srvReady)),
		"udp",
		uasContact.Address.HostPort(),
	)
	// Wait server to be ready
	<-srvReady

	// Client
	{
		ua, _ := NewUA()
		defer ua.Close()

		srv, _ := NewServer(ua)
		cli, _ := NewClient(ua)

		contactHDR := sip.ContactHeader{
			Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5088},
		}
		dialogCli := NewDialogClient(cli, contactHDR)

		// Setup server side
		srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
			err := dialogCli.ReadBye(req, tx)
			require.NoError(t, err)
		})
		srv.ServeRequest(func(r *sip.Request) {
			t.Log("UAC server: ", r.StartLine())
		})

		srvReady := make(chan struct{})
		ctx := context.WithValue(ctx, ListenReadyCtxKey, ListenReadyCtxValue(srvReady))
		go srv.ListenAndServe(ctx, "udp", contactHDR.Address.HostPort())
		// Wait server to be ready
		<-srvReady
		time.Sleep(200 * time.Millisecond)

		t.Run("UAS hangup", func(t *testing.T) {
			dialogSrv.OnSession = func(s *DialogServerSession) {
				time.Sleep(1 * time.Second)
				t.Log("GOT", s.ID)
				ctx, _ := context.WithTimeout(ctx, 1*time.Second)
				err := s.Bye(ctx)
				require.NoError(t, err)
			}

			// INVITE
			t.Log("UAC: INVITE")
			sess, err := dialogCli.Invite(context.TODO(), uasContact.Address.Clone(), nil)
			require.NoError(t, err)
			require.Equal(t, sip.StatusOK, sess.Response.StatusCode)

			// ACK
			t.Log("UAC: ACK")
			err = sess.Ack(context.TODO())
			require.NoError(t, err)

			<-sess.Done()
		})

		t.Run("UAC hangup", func(t *testing.T) {
			// INVITE
			t.Log("UAC: INVITE")
			sess, err := dialogCli.Invite(context.TODO(), uasContact.Address.Clone(), nil)
			require.NoError(t, err)
			require.Equal(t, sip.StatusOK, sess.Response.StatusCode)

			// ACK
			t.Log("UAC: ACK")
			err = sess.Ack(context.TODO())
			require.NoError(t, err)
			// BYE
			t.Log("UAC: BYE")
			err = sess.Bye(context.TODO())
			require.NoError(t, err)

			<-sess.Done()
		})
	}
}
