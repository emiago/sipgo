package sipgo

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/fakes"
	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCreateMessage(t testing.TB, rawMsg []string) sip.Message {
	msg, err := parser.ParseMessage([]byte(strings.Join(rawMsg, "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func createSimpleRequest(method sip.RequestMethod, sender sip.Uri, recipment sip.Uri, transport string) *sip.Request {
	req := sip.NewRequest(method, &recipment, "SIP/2.0")
	params := sip.NewParams()
	params["branch"] = sip.GenerateBranch()
	req.AppendHeader(&sip.ViaHeader{
		ProtocolName:    "SIP",
		ProtocolVersion: "2.0",
		Transport:       transport,
		Host:            sender.Host,
		Port:            sender.Port,
		Params:          params,
	})
	req.AppendHeader(&sip.FromHeader{
		DisplayName: strings.ToUpper(sender.User),
		Address: sip.Uri{
			User: sender.User,
			Host: sender.Host,
			Port: sender.Port,
		},
	})
	req.AppendHeader(&sip.ToHeader{
		DisplayName: strings.ToUpper(recipment.User),
		Address: sip.Uri{
			User: recipment.User,
			Host: recipment.Host,
			Port: recipment.Port,
		},
	})
	callid := sip.CallIDHeader("gotest-" + time.Now().Format(time.RFC3339Nano))
	req.AppendHeader(&callid)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: method})
	req.SetBody(nil)
	return req
}

func createTestInvite(t *testing.T, transport, addr string) (*sip.Request, string, string) {
	branch := sip.GenerateBranch()
	callid := "gotest-" + time.Now().Format(time.RFC3339Nano)
	ftag := fmt.Sprintf("%d", time.Now().UnixNano())
	return testCreateMessage(t, []string{
		"INVITE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + addr + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + addr + ">;tag=" + ftag,
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>",
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"",
	}).(*sip.Request), callid, ftag
}

func createTestBye(t *testing.T, transport, addr string, callid string, ftag string, totag string) *sip.Request {
	branch := sip.GenerateBranch()
	return testCreateMessage(t, []string{
		"BYE sip:bob@127.0.0.1:5060 SIP/2.0",
		"Via: SIP/2.0/" + transport + " " + addr + ";branch=" + branch,
		"From: \"Alice\" <sip:alice@" + addr + ">;tag=" + ftag,
		"To: \"Bob\" <sip:bob@127.0.0.1:5060>;tag=" + totag,
		"Call-ID: " + callid,
		"CSeq: 1 INVITE",
		"Content-Length: 0",
		"",
		"",
	}).(*sip.Request)
}

func TestMain(m *testing.M) {
	debug := flag.Bool("debug", false, "")
	flag.Parse()

	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05.000",
	}).With().Timestamp().Logger().Level(zerolog.WarnLevel)

	if *debug {
		log.Logger = log.Logger.With().Logger().Level(zerolog.DebugLevel)
	}

	m.Run()
}

func TestUDPUAS(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	srv, err := NewServer(ua)
	require.Nil(t, err)

	p := parser.NewParser()

	serverReader, serverWriter := io.Pipe()
	client1Reader, client1Writer := io.Pipe()

	serverAddr := net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	client1Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	// client2Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.3"), Port: 5060}
	client1 := &fakes.UDPConn{
		LAddr:  client1Addr,
		RAddr:  serverAddr,
		Reader: client1Reader,
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
			// client2Addr.String(): client2Writer,
		},
	}

	allmethods := []sip.RequestMethod{
		sip.INVITE, sip.ACK, sip.BYE, sip.REFER, sip.REGISTER, sip.INFO, sip.MESSAGE, sip.NOTIFY, sip.OPTIONS, sip.PRACK, sip.PUBLISH,
		// sip.CANCEL, // CANCEL can only be on active transaction by INVITE
	}

	// Register all handlers
	for _, method := range allmethods {
		srv.OnRequest(method, func(req *sip.Request, tx sip.ServerTransaction) {
			t.Log("New " + req.Method.String())
			// Make all responses
			res := sip.NewResponseFromRequest(req, 200, "OK", nil)
			tx.Respond(res)
		})
	}

	// Fire up server
	go srv.TransportLayer().ServeUDP(serverC)

	sender := sip.Uri{
		User: "alice",
		Host: client1Addr.IP.String(),
		Port: client1Addr.Port,
	}

	recipment := sip.Uri{
		User: "bob",
		Host: serverAddr.IP.String(),
		Port: serverC.LAddr.Port,
	}

	for _, method := range allmethods {
		req := createSimpleRequest(method, sender, recipment, "UDP")
		rstr := req.String()

		data := client1.TestRequest(t, []byte(rstr))
		res, err := p.Parse(data)
		assert.Nil(t, err)
		assert.Equal(t, "SIP/2.0 200 OK", res.StartLine())
	}
}

func TestTCPUAS(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	srv, err := NewServer(ua)
	require.Nil(t, err)

	p := parser.NewParser()

	serverReader, serverWriter := io.Pipe()
	client1Reader, client1Writer := io.Pipe()

	serverAddr := net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060}
	client1Addr := net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 5060}
	// client2Addr := net.UDPAddr{IP: net.ParseIP("127.0.0.3"), Port: 5060}
	client1 := &fakes.TCPConn{
		LAddr:  client1Addr,
		RAddr:  serverAddr,
		Reader: client1Reader,
		Writer: serverWriter,
	}

	//Server writes to clients and reads response
	serverC := &fakes.TCPConn{
		LAddr:  serverAddr,
		RAddr:  client1Addr,
		Reader: serverReader,
		Writer: client1Writer,
	}

	listener := &fakes.TCPListener{
		LAddr: serverAddr,
		Conns: make(chan *fakes.TCPConn, 2),
	}
	listener.Conns <- serverC

	allmethods := []sip.RequestMethod{
		sip.INVITE, sip.ACK, sip.BYE, sip.REFER, sip.REGISTER, sip.INFO, sip.MESSAGE, sip.NOTIFY, sip.OPTIONS, sip.PRACK, sip.PUBLISH,
		// sip.CANCEL, // CANCEL can only be on active transaction by INVITE
	}

	// Register all handlers
	for _, method := range allmethods {
		srv.OnRequest(method, func(req *sip.Request, tx sip.ServerTransaction) {
			t.Log("New " + req.Method.String())
			// Make all responses
			res := sip.NewResponseFromRequest(req, 200, "OK", nil)
			tx.Respond(res)
		})
	}

	// Fire up server
	go srv.TransportLayer().ServeTCP(listener)

	sender := sip.Uri{
		User: "alice",
		Host: client1Addr.IP.String(),
		Port: client1Addr.Port,
	}

	recipment := sip.Uri{
		User: "bob",
		Host: serverAddr.IP.String(),
		Port: serverC.LAddr.Port,
	}

	for _, method := range allmethods {
		req := createSimpleRequest(method, sender, recipment, "TCP")
		rstr := req.String()

		data := client1.TestRequest(t, []byte(rstr))
		res, err := p.Parse(data)
		assert.Nil(t, err)
		assert.Equal(t, "SIP/2.0 200 OK", res.StartLine())
	}
}

func BenchmarkSwitchVsMap(b *testing.B) {

	b.Run("map", func(b *testing.B) {
		m := map[string]RequestHandler{
			"INVITE":    nil,
			"ACK":       nil,
			"CANCEL":    nil,
			"BYE":       nil,
			"REGISTER":  nil,
			"OPTIONS":   nil,
			"SUBSCRIBE": nil,
			"NOTIFY":    nil,
			"REFER":     nil,
			"INFO":      nil,
			"MESSAGE":   nil,
			"PRACK":     nil,
			"UPDATE":    nil,
			"PUBLISH":   nil,
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := m["REGISTER"]; !ok {
				b.FailNow()
			}

			if _, ok := m["PUBLISH"]; !ok {
				b.FailNow()
			}
		}
	})

	b.Run("switch", func(b *testing.B) {
		m := func(s string) (RequestHandler, bool) {
			switch s {
			case "INVITE":
				return nil, true
			case "ACK":
				return nil, true
			case "CANCEL":
				return nil, true
			case "BYE":
				return nil, true
			case "REGISTER":
				return nil, true
			case "OPTIONS":
				return nil, true
			case "SUBSCRIBE":
				return nil, true
			case "NOTIFY":
				return nil, true
			case "REFER":
				return nil, true
			case "INFO":
				return nil, true
			case "MESSAGE":
				return nil, true
			case "PRACK":
				return nil, true
			case "UPDATE":
				return nil, true
			case "PUBLISH":
				return nil, true

			}
			return nil, false
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := m("REGISTER"); !ok {
				b.FailNow()
			}

			if _, ok := m("NOTIFY"); !ok {
				b.FailNow()
			}
		}
	})
}
