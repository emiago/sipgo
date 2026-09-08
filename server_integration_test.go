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
	"sync/atomic"
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
			assert.Equal(t, 200, res.StatusCode)

			tx.Terminate()
		})
	}
}

func TestIntegrationServerResponse(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}
	// Per rfc 18.2.2 server should
	// use Headers for unreliable requests while connection addr for reliable
	ua, _ := NewUA()
	defer ua.Close()
	srv, _ := NewServer(ua)

	serverResDestination := atomic.Pointer[string]{}
	srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, 200, "", nil)
		dst := res.Destination()
		serverResDestination.Store(&dst)
		tx.Respond(res)
	})

	ludp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 15151})
	require.NoError(t, err)
	defer ludp.Close()
	go srv.ServeUDP(ludp)

	ltcp, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 15151})
	require.NoError(t, err)
	defer ltcp.Close()
	go srv.ServeTCP(ltcp)

	t.Run("UDP", func(t *testing.T) {
		ua, _ := NewUA()
		defer ua.Close()
		cli, _ := NewClient(ua, WithClientAddr("127.0.0.1:15152"))
		// Make cli to listen on this port
		l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 15152})
		require.NoError(t, err)
		defer l.Close()
		go ua.TransportLayer().ServeUDP(l)

		req := sip.NewRequest(sip.OPTIONS, sip.Uri{Scheme: "sip", User: "test", Host: "127.0.0.1", Port: 15151})
		res, err := cli.Do(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, 200, res.StatusCode)
		// Now this is important that server choosed this addr instead client default
		// This confirms server is actually sending request over VIA
		assert.Equal(t, "127.0.0.1:15152", *serverResDestination.Load())
	})

	t.Run("TCP", func(t *testing.T) {
		ua, _ := NewUA()
		defer ua.Close()
		cli, _ := NewClient(ua, WithClientAddr("127.0.0.1:9999"), WithClientConnectionAddr("127.0.0.1:15000"))

		// TCP should always receive response over same connection and ignoring Via
		req := sip.NewRequest(sip.OPTIONS, sip.Uri{Scheme: "sip", User: "test", Host: "127.0.0.1", Port: 15151})
		req.SetTransport("TCP")
		res, err := cli.Do(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, 200, res.StatusCode)
		assert.Equal(t, "127.0.0.1:15000", *serverResDestination.Load())
	})
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
			shost, sport, _ := sip.ParseAddr(tc.serverAddr)
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
					req := sip.NewRequest(sip.INVITE, sip.Uri{User: "bob", Host: shost, Port: sport, Scheme: proto})
					req.SetTransport(tc.transport)
					tx, err := client.TransactionRequest(ctx, req)
					require.NoError(t, err)

					res := <-tx.Responses()
					assert.Equal(t, 200, res.StatusCode)

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
