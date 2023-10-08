//go:build integration

package sipgo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This will generate TLS certificates needed for test below
// openssl is required
//go:generate bash -c "cd testdata && ./generate_certs_rsa.sh"

var (
	//go:embed testdata/certs/server.crt
	rootCA []byte

	//go:embed testdata/certs/server.crt
	serverCRT []byte

	//go:embed testdata/certs/server.key
	serverKEY []byte

	//go:embed testdata/certs/client.crt
	clientCRT []byte

	//go:embed testdata/certs/client.key
	clientKEY []byte
)

func testServerTlsConfig(t *testing.T) *tls.Config {
	require.NotEmpty(t, serverCRT)
	require.NotEmpty(t, serverKEY)

	cert, err := tls.X509KeyPair(serverCRT, serverKEY)
	require.NoError(t, err)
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	return cfg
}

func testClientTlsConfig(t *testing.T) *tls.Config {
	require.NotEmpty(t, clientCRT)
	require.NotEmpty(t, clientKEY)
	require.NotEmpty(t, rootCA)

	cert, err := tls.X509KeyPair(clientCRT, clientKEY)
	require.NoError(t, err)

	roots := x509.NewCertPool()

	ok := roots.AppendCertsFromPEM(rootCA)
	if !ok {
		panic("failed to parse root certificate")
	}

	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		// InsecureSkipVerify: false,
		// InsecureSkipVerify: true,
		// MinVersion:         tls.VersionTLS12,
	}

	return tlsConf
}

func TestSimpleCall(t *testing.T) {
	ua, _ := NewUA()

	serverTLS := testServerTlsConfig(t)
	clientTLS := testClientTlsConfig(t)

	testCases := []struct {
		transport  string
		serverAddr string
		encrypted  bool
	}{
		{transport: "udp", serverAddr: "127.1.1.100:5060"},
		{transport: "tcp", serverAddr: "127.1.1.100:5060"},
		{transport: "ws", serverAddr: "127.1.1.100:5061"},
		{transport: "tls", serverAddr: "127.1.1.100:5062", encrypted: true},
		{transport: "wss", serverAddr: "127.1.1.100:5063", encrypted: true},
	}

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

	ctx, shutdown := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}

	t.Cleanup(func() {
		shutdown()
		wg.Wait()
	})

	for _, tc := range testCases {
		wg.Add(1)

		// Trick to make sure we are listening
		serverReady := make(chan any)
		ctx = context.WithValue(ctx, ctxTestListenAndServeReady, serverReady)

		go func(transport string, serverAddr string, encrypted bool) {
			defer wg.Done()

			if encrypted {
				err := srv.ListenAndServeTLS(ctx, transport, serverAddr, serverTLS)
				if err != nil && !errors.Is(err, net.ErrClosed) {
					t.Error("ListenAndServe error: ", err)
				}
				return
			}

			err := srv.ListenAndServe(ctx, transport, serverAddr)
			if err != nil && !errors.Is(err, net.ErrClosed) {
				t.Error("ListenAndServe error: ", err)
			}
		}(tc.transport, tc.serverAddr, tc.encrypted)
		<-serverReady
	}

	t.Log("Server ready")

	for _, tc := range testCases {
		t.Run(tc.transport, func(t *testing.T) {
			// Doing gracefull shutdown

			// Build UAC
			ua, _ = NewUA(WithUserAgenTLSConfig(clientTLS))
			client, err := NewClient(ua)
			require.NoError(t, err)

			// csrv, err := NewServer(ua) // Create server handle
			// require.Nil(t, err)

			// wg.Add(1)
			// go func() {
			// 	defer wg.Done()
			// 	err := csrv.ListenAndServe(ctx, tc.transport, "127.1.1.200:5066")
			// 	if err != nil && !errors.Is(err, net.ErrClosed) {
			// 		t.Error("ListenAndServe error: ", err)
			// 	}
			// }()
			proto := "sip"
			if tc.encrypted {
				proto = "sips"
			}

			req, _, _ := createTestInvite(t, proto+":bob@"+tc.serverAddr, tc.transport, client.ip.String())
			tx, err := client.TransactionRequest(ctx, req)
			require.NoError(t, err)

			res := <-tx.Responses()
			assert.Equal(t, sip.StatusCode(200), res.StatusCode)

			tx.Terminate()
		})
	}
}
