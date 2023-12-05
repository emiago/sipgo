package main

import (
	"flag"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	NRequest = flag.Int("NReq", 1000, "Change default num request")
)

func testCreateMessage(t testing.TB, rawMsg []string) sip.Message {
	msg, err := sip.ParseMessage([]byte(strings.Join(rawMsg, "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func inviteScenario(t testing.TB, client1, client2 fakes.TestConnection, p *sip.Parser) {
	// client2 := testCreateUDPListener(t, "udp", client2Addr)
	// defer client2.Close()

	// time.Sleep(12 * time.Second)
	transport := "UDP"
	switch client1.(type) {
	case *fakes.TCPConn:
		transport = "TCP"
	}

	branch := sip.GenerateBranch()
	callid := "gotest-" + time.Now().Format(time.RFC3339Nano)
	inviteReq := testCreateMessage(t, []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + client1.LocalAddr().String() + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + client1.LocalAddr().String() + ">",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"",
	})

	ackReq := testCreateMessage(t, []string{
		"ACK sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + client1.LocalAddr().String() + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + client1.LocalAddr().String() + ">;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 ACK",
		"Content-Length: 0",
		"",
		"",
	})

	byeReq := testCreateMessage(t, []string{
		"BYE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + client1.LocalAddr().String() + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + client1.LocalAddr().String() + ">;tag=1928301774",
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 2 BYE",
		"Content-Length: 0",
		"",
		"",
	})

	// Run Client2
	go func() {
		//RECEIVE INVITE
		{
			res := client2.TestReadConn(t)
			inviteReqRec, err := p.ParseSIP(res)
			require.Nil(t, err)
			assert.Equal(t, inviteReqRec.(*sip.Request).StartLine(), inviteReq.(*sip.Request).StartLine())

			// trying := sip.NewResponseFromRequest("", inviteReqRec.(sip.Request), 180, "Ringing", "")

			// time.Sleep(1 * time.Second)

			t.Log("CLIENT2 INVITE: Send 200 OK")
			// Let transaction layer sends Trying
			time.Sleep(300 * time.Millisecond)
			ok200 := sip.NewResponseFromRequest(inviteReqRec.(*sip.Request), 200, "OK", nil)
			// serverC.ExpectAddr(client2Addr)
			resp := ok200.String()
			client2.TestWriteConn(t, []byte(resp))
		}

		// RECEIVE ACK or BYE
		{
			// We can receive resend INVITE
			for {
				res := client2.TestReadConn(t)

				resReceived, err := p.ParseSIP(res)
				if req, ok := resReceived.(*sip.Request); ok && req.IsInvite() {
					continue
				}

				if req, ok := resReceived.(*sip.Request); ok && req.Method == sip.ACK {
					require.Nil(t, err)
					t.Log("CLIENT2: Received ACK. Call established")
					assert.Equal(t, ackReq.(*sip.Request).StartLine(), req.StartLine())
					continue
				}

				// RECEIVE BYE
				req, ok := resReceived.(*sip.Request)
				require.True(t, ok, req.Short())
				assert.Equal(t, byeReq.(*sip.Request).StartLine(), req.StartLine())

				t.Log("CLIENT2 BYE: Send 200 OK")
				ok200 := sip.NewResponseFromRequest(req, 200, "OK", nil)
				// serverC.ExpectAddr(client2Addr)
				client2.TestWriteConn(t, []byte(ok200.String()))
				break
			}

		}
	}()

	// SEND INVITE
	{
		// serverC.ExpectAddr(client1Addr)
		t.Log("CLIENT1: Send INVITE")
		res := client1.TestRequest(t, []byte(inviteReq.String()))
		t.Log("CLIENT1 INVITE: Got response")
		trying, err := p.ParseSIP(res)
		require.Nil(t, err)
		assert.Equal(t, "SIP/2.0 100 Trying", trying.(*sip.Response).StartLine())

		res = client1.TestReadConn(t)
		inviteOK, err := p.ParseSIP(res)
		require.Nil(t, err)
		assert.Equal(t, "SIP/2.0 200 OK", inviteOK.(*sip.Response).StartLine())
	}

	// SEND ACK
	{
		t.Log("CLIENT1: Send ACK")
		client1.TestWriteConn(t, []byte(ackReq.String()))
	}

	// SEND BYE
	{
		// serverC.ExpectAddr(client1Addr)
		t.Log("CLIENT1: Send BYE")
		res := client1.TestRequest(t, []byte(byeReq.String()))
		t.Log("CLIENT1 BYE: Got response")
		byeOK, err := p.ParseSIP(res)
		require.Nil(t, err)
		assert.Equal(t, "SIP/2.0 200 OK", byeOK.(*sip.Response).StartLine())
	}

}

func TestMain(m *testing.M) {
	debug := flag.Bool("debug", false, "")
	flag.Parse()

	logruser := logrus.New()
	formater := &logrus.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05.000",
	}

	formater.DisableColors = false
	logruser.Formatter = formater

	logruser.SetOutput(os.Stderr)
	// logruser.SetLevel(logrus.DebugLevel)

	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05.000",
	}).With().Timestamp().Logger().Level(zerolog.WarnLevel)

	if *debug {
		logruser.SetLevel(logrus.TraceLevel)
		log.Logger = log.Logger.With().Logger().Level(zerolog.DebugLevel)
		sip.SIPDebug = true
	}

	m.Run()
}

func TestInviteCallUDP(t *testing.T) {
	p := sip.NewParser()
	serverReader, serverWriter := io.Pipe()
	client1Reader, client1Writer := io.Pipe()
	client2Reader, client2Writer := io.Pipe()
	//Client1 writes to server and reads response

	serverAddr := net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	client1Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	client2Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.3"), Port: 5060}
	client1 := &fakes.UDPConn{
		LAddr:  client1Addr,
		RAddr:  serverAddr,
		Reader: client1Reader,
		Writers: map[string]io.Writer{
			serverAddr.String(): serverWriter,
		},
	}

	//Client2 writes to server and reads response
	client2 := &fakes.UDPConn{
		LAddr:  client2Addr,
		RAddr:  serverAddr,
		Reader: client2Reader,
		Writers: map[string]io.Writer{
			serverAddr.String(): serverWriter,
		},
	}

	//Server writes to clients and reads response
	serverC := &fakes.UDPConn{
		LAddr:  serverAddr,
		RAddr:  client1Addr,
		Reader: serverReader,
		Writers: map[string]io.Writer{
			client1Addr.String(): client1Writer,
			client2Addr.String(): client2Writer,
		},
	}

	t.Log("Running proxy", serverAddr, client1Addr, client2Addr)
	srv := setupSipProxy(client2Addr.String(), serverAddr.String())
	go srv.ServeUDP(serverC)
	inviteScenario(t, client1, client2, p)

}

func TestInviteCallTCP(t *testing.T) {
	sip.SIPDebug = true
	p := sip.NewParser()
	serverReader, serverWriter := io.Pipe()
	serverReader2, serverWriter2 := io.Pipe()
	client1Reader, client1Writer := io.Pipe()
	client2Reader, client2Writer := io.Pipe()
	//Client1 writes to server and reads response

	serverAddr := net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	client1Addr := net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	client2Addr := net.TCPAddr{IP: net.ParseIP("127.0.0.3"), Port: 5060}

	client1 := &fakes.TCPConn{
		LAddr:  client1Addr,
		RAddr:  serverAddr,
		Reader: client1Reader,
		Writer: serverWriter,
	}

	//Client2 writes to server and reads response
	client2 := &fakes.TCPConn{
		LAddr:  client2Addr,
		RAddr:  serverAddr,
		Reader: client2Reader,
		Writer: serverWriter2,
	}

	//Server writes to clients and reads response
	serverC1 := &fakes.TCPConn{
		LAddr:  serverAddr,
		RAddr:  client1Addr,
		Reader: serverReader,
		Writer: client1Writer,
	}

	//Add client2 as new connection although normall this should go by Dial
	serverC2 := &fakes.TCPConn{
		LAddr:  serverAddr,
		RAddr:  client2Addr,
		Reader: serverReader2,
		Writer: client2Writer,
	}

	listener := &fakes.TCPListener{
		LAddr: serverAddr,
		Conns: make(chan *fakes.TCPConn, 2),
	}
	listener.Conns <- serverC1
	listener.Conns <- serverC2

	srv := setupSipProxy(client2Addr.String(), serverAddr.String())

	go srv.ServeTCP(listener)
	inviteScenario(t, client1, client2, p)
}

func TestRegisterTCP(t *testing.T) {
	p := sip.NewParser()
	serverReader, serverWriter := io.Pipe()
	// serverReader2, serverWriter2 := io.Pipe()
	client1Reader, client1Writer := io.Pipe()
	// client2Reader, client2Writer := io.Pipe()
	//Client1 writes to server and reads response

	serverAddr := net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	client1Addr := net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	client2Addr := net.TCPAddr{IP: net.ParseIP("127.0.0.3"), Port: 5060}

	client1 := &fakes.TCPConn{
		LAddr:  client1Addr,
		RAddr:  serverAddr,
		Reader: client1Reader,
		Writer: serverWriter,
	}

	//Server writes to clients and reads response
	serverC1 := &fakes.TCPConn{
		LAddr:  serverAddr,
		RAddr:  client1Addr,
		Reader: serverReader,
		Writer: client1Writer,
	}

	listener := &fakes.TCPListener{
		LAddr: serverAddr,
		Conns: make(chan *fakes.TCPConn, 2),
	}
	listener.Conns <- serverC1

	srv := setupSipProxy(client2Addr.String(), serverAddr.String())
	go srv.ServeTCP(listener)

	reg := testCreateMessage(t, []string{
		"REGISTER sip:10.5.0.10:5060;transport=tcp SIP/2.0",
		"v: SIP/2.0/TCP 10.5.0.1:47453;rport;branch=z9hG4bKPj90632a72-086e-485c-bff4-dbe6711fdcec;alias",
		"Route: <sip:10.5.0.10:5060;transport=tcp;lr>",
		"Route: <sip:10.5.0.10:5060;transport=tcp;lr>",
		"Max-Forwards: 70",
		"f: <sip:KC82LHNFR5@10.5.0.10>;tag=fe37d7ec-2449-4fed-a759-77f62b37133b",
		"t: <sip:KC82LHNFR5@10.5.0.10>",
		"i: 5187d714-12ed-47b9-8934-47bfa447960d",
		"CSeq: 51826 REGISTER",
		"User-Agent: PJSUA v2.10 Linux-5.14.4.18/x86_64/glibc-2.31",
		"k: outbound, path",
		"m: <sip:KC82LHNFR5@10.5.0.1:47453;transport=TCP;ob>;reg-id=1;+sip.instance=\"<urn:uuid:00000000-0000-0000-0000-0000eb83488d>\"",
		"Expires: 90",
		"Allow: PRACK, INVITE, ACK, BYE, CANCEL, UPDATE, INFO, SUBSCRIBE, NOTIFY, REFER, MESSAGE, OPTIONS",
		"l:  0",
		"",
		"",
	})

	res := client1.TestRequest(t, []byte(reg.String()))
	res200, err := p.ParseSIP(res)
	require.Nil(t, err)
	t.Log(res200.String())
	assert.Equal(t, "SIP/2.0 200 OK", res200.(*sip.Response).StartLine())
}

func BenchmarkInviteCall(t *testing.B) {
	serverAddr := net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	client1Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	client2Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.3"), Port: 5060}
	t.Log("Running proxy", serverAddr, client1Addr, client2Addr)
	serverReader, serverWriter := io.Pipe()
	client1Reader, client1Writer := io.Pipe()
	client2Reader, client2Writer := io.Pipe()
	//Client1 writes to server and reads response
	client1 := &fakes.UDPConn{
		LAddr:  client1Addr,
		RAddr:  serverAddr,
		Reader: client1Reader,
		Writers: map[string]io.Writer{
			serverAddr.String(): serverWriter,
		},
	}

	//Client2 writes to server and reads response
	client2 := &fakes.UDPConn{
		LAddr:  client2Addr,
		RAddr:  serverAddr,
		Reader: client2Reader,
		Writers: map[string]io.Writer{
			serverAddr.String(): serverWriter,
		},
	}

	//Server writes to clients and reads response
	serverC := &fakes.UDPConn{
		LAddr:  serverAddr,
		RAddr:  client1Addr,
		Reader: serverReader,
		Writers: map[string]io.Writer{
			client1Addr.String(): client1Writer,
			client2Addr.String(): client2Writer,
		},
	}

	srv := setupSipProxy(client2Addr.String(), serverAddr.String())
	go srv.ServeUDP(serverC)
	// defer srv.Shutdown()

	// client2 := testCreateUDPListener(t, "udp", client2Addr)
	// defer client2.Close()

	// time.Sleep(12 * time.Second)
	N := t.N
	t.Log("Running iterations:", N)
	inviteRequests := make([]string, N)
	inviteResponses := make([]string, N)
	for i := 0; i < N; i++ {
		branch := sip.GenerateBranch()
		callid := "gotest-" + time.Now().Format(time.RFC3339Nano)
		inviteReq := testCreateMessage(t, []string{
			"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
			"Via: SIP/2.0/UDP " + client1Addr.String() + ";branch=" + branch,
			"From: \"Alice\" <sip:alice@" + client1Addr.String() + ">",
			"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
			"Call-ID: " + callid,
			"CSeq: 1 INVITE",
			"Content-Length: 0",
			"",
			"",
		})
		inviteRequests[i] = inviteReq.String()

		ok200 := sip.NewResponseFromRequest(inviteReq.(*sip.Request), 200, "OK", nil)
		// serverC.ExpectAddr(client2Addr)
		inviteResponses[i] = ok200.String()
	}
	t.ResetTimer()
	go func() {
		defer t.Log("CLIENT2 exit")
		for i := 0; i < N; i++ {
			client2.TestReadConn(t)
			client2.TestWriteConn(t, []byte(inviteResponses[i]))
		}
	}()

	for i := 0; i < N; i++ {
		client1.TestWriteConn(t, []byte(inviteRequests[i]))
		client1.TestReadConn(t)
	}
}
