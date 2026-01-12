package sipgo

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
	"github.com/stretchr/testify/assert"
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

	dialogSrv := NewDialogServerCache(cli, uasContact)
	// digestChal := digest.Challenge{
	// 	Username: "alice",
	// 	Password: "alice123",
	// }
	digestChal := digest.Challenge{
		Realm:     "sipgo-server",
		Nonce:     fmt.Sprintf("%d", time.Now().UnixMicro()),
		Opaque:    "sipgo",
		Algorithm: "MD5",
	}
	auth := digest.Options{
		Method:   "INVITE",
		URI:      uasContact.Address.Addr(),
		Username: "alice",
		Password: "1234",
	}

	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		dlg, err := dialogSrv.ReadInvite(req, tx)
		require.NoError(t, err)
		// defer dlg.Close()

		if err := dlg.authDigest(&digestChal, auth); err != nil {
			// TODO check what is error
			t.Log(err)
			return
		}

		err = dlg.Respond(sip.StatusTrying, "Trying", nil)
		require.NoError(t, err)

		err = dlg.Respond(sip.StatusRinging, "Ringing", nil)
		require.NoError(t, err)

		err = dlg.Respond(sip.StatusOK, "OK", nil)
		require.NoError(t, err)

		state := dlg.LoadState()
		if state == sip.DialogStateEnded {
			return
		}

		time.Sleep(1 * time.Second)
		ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
		dlg.Bye(ctx)

		// ctx, _ := context.WithTimeout(ctx, 3*time.Second)
		// for state := range dlg.StateRead() {
		// 	if state == sip.DialogStateEnded {
		// 		return
		// 	}

		// 	time.Sleep(1 * time.Second)
		// 	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
		// 	dlg.Bye(ctx)
		// 	return
		// }
	})

	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
		if req.Recipient.Addr() != uasContact.Address.Addr() {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Not valid SIP uri", nil))
			return
		}
		if err := dialogSrv.ReadAck(req, tx); err != nil {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, err.Error(), nil))
		}
	})

	srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		if req.Recipient.Addr() != uasContact.Address.Addr() {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Not valid SIP uri", nil))
			return
		}

		if err := dialogSrv.ReadBye(req, tx); err != nil {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, err.Error(), nil))
		}
	})

	srv.serveRequest(func(r *sip.Request) {
		t.Log("UAS server: ", r.StartLine())
	})

	startTestServer(ctx, srv, uasContact.Address.HostPort())

	// Client
	{
		ua, _ := NewUA()
		defer ua.Close()

		srv, _ := NewServer(ua)
		cli, _ := NewClient(ua, WithClientConnectionAddr("127.0.0.200:0"))

		// Use for now empheral contact based on client connection
		contactHDR := sip.ContactHeader{}
		dialogCli := NewDialogClientCache(cli, contactHDR)

		// Setup server side
		srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
			err := dialogCli.ReadBye(req, tx)
			require.NoError(t, err)
		})
		srv.serveRequest(func(r *sip.Request) {
			t.Log("UAC server: ", r.StartLine())
		})

		t.Run("UAShangup", func(t *testing.T) {
			// INVITE
			t.Log("UAC: INVITE")
			sess, err := dialogCli.Invite(context.TODO(), uasContact.Address, nil)
			require.NoError(t, err)
			defer sess.Close()

			err = sess.WaitAnswer(ctx, AnswerOptions{
				Username: auth.Username,
				Password: auth.Password,
			})
			require.NoError(t, err)
			require.Equal(t, sip.StatusOK, sess.InviteResponse.StatusCode)

			// ACK
			t.Log("UAC: ACK")
			err = sess.Ack(context.TODO())
			require.NoError(t, err)

			<-sess.inviteTx.Done()
		})

		t.Run("UAC hangup", func(t *testing.T) {
			// INVITE
			t.Log("UAC: INVITE")
			sess, err := dialogCli.Invite(context.TODO(), uasContact.Address, nil)
			require.NoError(t, err)
			defer sess.Close()

			err = sess.WaitAnswer(ctx, AnswerOptions{
				Username: auth.Username,
				Password: auth.Password,
			})
			require.NoError(t, err)
			require.Equal(t, sip.StatusOK, sess.InviteResponse.StatusCode)

			// ACK
			t.Log("UAC: ACK")
			err = sess.Ack(context.TODO())
			require.NoError(t, err)
			// BYE
			t.Log("UAC: BYE")
			err = sess.Bye(context.TODO())
			require.NoError(t, err)

			<-sess.inviteTx.Done()
		})

		require.Empty(t, dialogCli.dialogsLen())
	}

}

func TestIntegrationDialogBrokenUAC(t *testing.T) {
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
		Address: sip.Uri{User: "test", Host: "127.0.0.201", Port: 5099},
	}

	dialogSrv := NewDialogServerCache(cli, uasContact)

	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		dlg, err := dialogSrv.ReadInvite(req, tx)
		require.NoError(t, err)
		// defer dlg.Close()

		err = dlg.Respond(sip.StatusTrying, "Trying", nil)
		if err != nil {
			fmt.Println("Error OnInvite", err)
			return
		}
		err = dlg.Respond(sip.StatusRinging, "Ringing", nil)
		if err != nil {
			fmt.Println("Error OnInvite", err)
			return
		}
		err = dlg.Respond(sip.StatusOK, "OK", nil)
		if err != nil {
			fmt.Println("Error OnInvite", err)
			return
		}
		<-dlg.Context().Done()
	})

	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
		dialogSrv.ReadAck(req, tx)
	})

	srv.serveRequest(func(r *sip.Request) {
		t.Log("UAS server: ", r.StartLine())
	})

	startTestServer(ctx, srv, uasContact.Address.HostPort())

	// Client
	{
		ua, _ := NewUA()
		defer ua.Close()

		srv, _ := NewServer(ua)
		cli, _ := NewClient(ua)

		contactHDR := sip.ContactHeader{
			Address: sip.Uri{User: "test", Host: "127.0.0.201", Port: 5088},
		}
		dialogCli := NewDialogClientCache(cli, contactHDR)

		// Setup server side
		srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
			err := dialogCli.ReadBye(req, tx)
			require.NoError(t, err)
		})
		srv.serveRequest(func(r *sip.Request) {
			t.Log("UAC server: ", r.StartLine())
		})

		startTestServer(ctx, srv, contactHDR.Address.HostPort())

		t.Run("UAS BYE Error", func(t *testing.T) {
			srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
				tx.Respond(sip.NewResponseFromRequest(req, sip.StatusInternalServerError, "", nil))
			})
			// INVITE
			t.Log("UAC: INVITE ", uasContact.Address.String())
			sess, err := dialogCli.Invite(context.TODO(), uasContact.Address, nil)
			require.NoError(t, err)
			defer sess.Close()

			err = sess.WaitAnswer(ctx, AnswerOptions{})
			require.NoError(t, err)
			require.Equal(t, sip.StatusOK, sess.InviteResponse.StatusCode)

			// ACK
			t.Log("UAC: ACK")
			err = sess.Ack(context.TODO())
			require.NoError(t, err)
			// BYE
			t.Log("UAC: BYE")
			err = sess.Bye(context.TODO())
			require.Error(t, err)
			require.Empty(t, dialogCli.dialogsLen())
		})

		t.Run("UAS ACK Error", func(t *testing.T) {
			// INVITE
			t.Log("UAC: INVITE ", uasContact.Address.String())
			sess, err := dialogCli.Invite(context.TODO(), uasContact.Address, nil)
			require.NoError(t, err)
			defer sess.Close()

			err = sess.WaitAnswer(ctx, AnswerOptions{})
			require.NoError(t, err)
			require.Equal(t, sip.StatusOK, sess.InviteResponse.StatusCode)

			// ACK
			t.Log("UAC: ACK")
			sess.InviteResponse.Contact().Address.Host = "nodestination.dst"
			ctx, _ := context.WithTimeout(context.Background(), 1*time.Millisecond)
			err = sess.Ack(ctx)
			require.Error(t, err)

			sess.Close()
			require.Empty(t, dialogCli.dialogsLen())
		})

	}

}

func TestIntegrationDialogCancel(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	ua, _ := NewUA()
	defer ua.Close()
	srv, _ := NewServer(ua)
	cli, _ := NewClient(ua)
	// sip.SetTimers(10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	uasContact := sip.ContactHeader{
		Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
	}

	dialogSrv := NewDialogServerCache(cli, uasContact)
	wg := sync.WaitGroup{}
	wg.Add(1)
	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		defer wg.Done()
		dlg, err := dialogSrv.ReadInvite(req, tx)
		require.NoError(t, err)

		err = dlg.Respond(sip.StatusTrying, "Trying", nil)
		require.NoError(t, err)

		err = dlg.Respond(sip.StatusRinging, "Ringing", nil)
		require.NoError(t, err)

		<-dlg.Context().Done()
	})

	srv.OnCancel(func(req *sip.Request, tx sip.ServerTransaction) {
		fmt.Println("Cancel received")
	})

	srv.serveRequest(func(r *sip.Request) {
		fmt.Println("UAS server: ", r.StartLine())
	})

	startTestServer(ctx, srv, uasContact.Address.HostPort())

	// Client
	{
		ua, _ := NewUA()
		defer ua.Close()

		srv, _ := NewServer(ua)
		cli, _ := NewClient(ua)

		contactHDR := sip.ContactHeader{
			Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5088},
		}
		dialogCli := NewDialogClientCache(cli, contactHDR)

		srv.serveRequest(func(r *sip.Request) {
			t.Log("UAC server: ", r.StartLine())
		})

		startTestServer(ctx, srv, contactHDR.Address.HostPort())

		// INVITE
		t.Log("UAC: INVITE")
		sess, err := dialogCli.Invite(context.TODO(), uasContact.Address, nil)
		require.NoError(t, err)
		defer sess.Close()

		// Cancel a call
		ctx, cancel := context.WithCancel(sess.Context())
		err = sess.WaitAnswer(ctx, AnswerOptions{OnResponse: func(res *sip.Response) error {
			if res.StatusCode == sip.StatusRinging {
				cancel()
			}
			return nil
		}})
		require.ErrorIs(t, err, context.Canceled)
		assert.EqualValues(t, 487, sess.InviteResponse.StatusCode)
	}

	wg.Wait()
}

func startTestServer(ctx context.Context, srv *Server, hostPort string) {
	srvReady := make(chan struct{})
	go srv.ListenAndServe(
		context.WithValue(ctx, ListenReadyCtxKey, ListenReadyCtxValue(srvReady)),
		"udp",
		hostPort,
	)
	// Wait server to be ready
	<-srvReady
	time.Sleep(500 * time.Millisecond) // just to avoid race with listeners on UDP
}
