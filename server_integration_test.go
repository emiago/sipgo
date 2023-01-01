package sipgo

import (
	"context"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebsocket(t *testing.T) {
	ua, _ := NewUA()
	// transport.SIPDebug = true
	log.Logger = log.Level(zerolog.DebugLevel)

	// Build UAS
	srv, err := NewServer(ua)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup dialog server")
	}
	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		t.Log("Invite received")
		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		if err := tx.Respond(res); err != nil {
			t.Fatal(err)
		}
		<-tx.Done()
	})

	go func() {
		if err := srv.ListenAndServe(context.TODO(), "ws", "127.0.0.1:5060"); err != nil {
			log.Error().Err(err).Msg("Fail to serve")
		}
	}()

	// Build UAC
	ua, _ = NewUA()
	client, err := NewClient(ua)
	require.Nil(t, err)

	csrv, err := NewServer(ua) // Create server handle
	require.Nil(t, err)
	go func() {
		if err := csrv.ListenAndServe(context.TODO(), "ws", "127.0.0.2:5060"); err != nil {
			log.Error().Err(err).Msg("Fail to serve")
		}
	}()

	time.Sleep(2 * time.Second)

	req, _, _ := createTestInvite(t, "WS", client.ip.String())
	// err = client.WriteRequest(req)
	// require.Nil(t, err)
	// time.Sleep(2 * time.Second)
	tx, err := client.TransactionRequest(req)
	require.Nil(t, err)
	res := <-tx.Responses()
	assert.Equal(t, sip.StatusCode(200), res.StatusCode())
}
