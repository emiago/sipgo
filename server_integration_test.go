package sipgo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
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

func testServerTlsConfig(t testing.TB) *tls.Config {
	require.NotEmpty(t, serverCRT)
	require.NotEmpty(t, serverKEY)

	cert, err := tls.X509KeyPair(serverCRT, serverKEY)
	require.NoError(t, err)
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	return cfg
}

func testClientTlsConfig(t testing.TB) *tls.Config {
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

func TestIntegrationClientServer(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	serverTLS := testServerTlsConfig(t)
	clientTLS := testClientTlsConfig(t)

	testCases := []struct {
		transport  string
		serverAddr string
		encrypted  bool
	}{
		{transport: "udp", serverAddr: "127.1.1.100:16060"},
		{transport: "tcp", serverAddr: "127.1.1.100:16060"},
		{transport: "ws", serverAddr: "127.1.1.100:16061"},
		{transport: "tls", serverAddr: "127.1.1.100:16062", encrypted: true},
		{transport: "wss", serverAddr: "127.1.1.100:16063", encrypted: true},
	}

	ctx, shutdown := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	t.Cleanup(func() {
		shutdown()
		wg.Wait()
	})

	for _, tc := range testCases {
		ua, _ := NewUA()
		t.Cleanup(func() {
			ua.Close()
		})
		srv, err := NewServer(ua)
		require.NoError(t, err)

		srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
			t.Log("Invite received")
			res := sip.NewResponseFromRequest(req, 200, "OK", nil)
			if err := tx.Respond(res); err != nil {
				t.Fatal(err)
			}
			<-tx.Done()
		})

		// Trick to make sure we are listening
		serverReady := make(chan struct{})
		ctx = context.WithValue(ctx, ListenReadyCtxKey, ListenReadyCtxValue(serverReady))

		wg.Add(1)
		go func(srv *Server, transport string, serverAddr string, encrypted bool) {
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
		}(srv, tc.transport, tc.serverAddr, tc.encrypted)
		<-serverReady
		t.Log("Server ready")
	}

	for _, tc := range testCases {
		t.Run(tc.transport, func(t *testing.T) {
			// Doing gracefull shutdown

			// Build UAC
			ua, _ := NewUA(WithUserAgenTLSConfig(clientTLS))
			client, err := NewClient(ua)
			require.NoError(t, err)

			proto := "sip"
			if tc.encrypted {
				proto = "sips"
			}

			req, _, _ := createTestInvite(t, proto+":bob@"+tc.serverAddr, tc.transport, client.host)
			tx, err := client.TransactionRequest(ctx, req)
			require.NoError(t, err)

			res := <-tx.Responses()
			assert.Equal(t, sip.StatusCode(200), res.StatusCode)

			tx.Terminate()
		})
	}
}

func BenchmarkIntegrationClientServer(t *testing.B) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	serverTLS := testServerTlsConfig(t)
	clientTLS := testClientTlsConfig(t)

	testCases := []struct {
		transport  string
		serverAddr string
		encrypted  bool
	}{
		{transport: "udp", serverAddr: "127.1.1.100:15060"},
		{transport: "tcp", serverAddr: "127.1.1.100:15060"},
		{transport: "ws", serverAddr: "127.1.1.100:15061"},
		{transport: "tls", serverAddr: "127.1.1.100:15062", encrypted: true},
		{transport: "wss", serverAddr: "127.1.1.100:15063", encrypted: true},
	}

	ctx, shutdown := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	t.Cleanup(func() {
		shutdown()
		wg.Wait()
	})

	for _, tc := range testCases {
		ua, _ := NewUA()
		srv, err := NewServer(ua)
		require.NoError(t, err)

		srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
			res := sip.NewResponseFromRequest(req, 200, "OK", nil)
			if err := tx.Respond(res); err != nil {
				t.Fatal(err)
			}
			<-tx.Done()
		})

		// Trick to make sure we are listening
		serverReady := make(chan struct{})
		ctx = context.WithValue(ctx, ListenReadyCtxKey, ListenReadyCtxValue(serverReady))

		wg.Add(1)
		go func(srv *Server, transport string, serverAddr string, encrypted bool) {
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
		}(srv, tc.transport, tc.serverAddr, tc.encrypted)
		<-serverReady
		t.Log("Server ready")
	}

	var maxInvitesPerSec chan struct{}
	if v := os.Getenv("MAX_REQUESTS"); v != "" {
		t.Logf("Limiting number of requests: %s req/s", v)
		maxInvites, _ := strconv.Atoi(v)
		maxInvitesPerSec = make(chan struct{}, maxInvites)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
					for i := 0; i < maxInvites; i++ {
						<-maxInvitesPerSec
					}
				}
			}
		}()
	}

	for _, tc := range testCases {
		t.Run(tc.transport, func(t *testing.B) {
			proto := "sip"
			if tc.encrypted {
				proto = "sips"
			}
			t.ResetTimer()
			t.ReportAllocs()

			t.RunParallel(func(p *testing.PB) {
				// Build UAC
				ua, _ := NewUA(WithUserAgenTLSConfig(clientTLS))
				client, err := NewClient(ua)
				require.NoError(t, err)

				for p.Next() {
					// If we are running in limit mode
					if maxInvitesPerSec != nil {
						maxInvitesPerSec <- struct{}{}
					}
					req, _, _ := createTestInvite(t, proto+":bob@"+tc.serverAddr, tc.transport, client.host)
					tx, err := client.TransactionRequest(ctx, req)
					require.NoError(t, err)

					res := <-tx.Responses()
					assert.Equal(t, sip.StatusCode(200), res.StatusCode)

					tx.Terminate()
				}

			})

			t.ReportMetric(float64(t.N)/t.Elapsed().Seconds(), "req/s")

			// ua, _ := NewUA(WithUserAgenTLSConfig(clientTLS))
			// 		client, err := NewClient(ua)
			// 		require.NoError(t, err)

			// for i := 0; i < t.N; i++ {
			// 	req, _, _ := createTestInvite(t, proto+":bob@"+tc.serverAddr, tc.transport, client.ip.String())
			// 	tx, err := client.TransactionRequest(ctx, req)
			// 	require.NoError(t, err)

			// 	res := <-tx.Responses()
			// 	assert.Equal(t, sip.StatusCode(200), res.StatusCode)

			// 	tx.Terminate()
			// }
			// t.ReportMetric(float64(t.N)/max(t.Elapsed().Seconds(), 1), "req/s")
		})
	}
}
