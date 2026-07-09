package sipgo

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/siptest"
	"github.com/icholy/digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRequestBuild(t *testing.T) {
	// ua, err := NewUA(WithUserAgentIP(net.ParseIP("10.0.0.0")))
	ua, err := NewUA(
		WithUserAgentHostname("mydomain.com"),
	)
	require.Nil(t, err)

	c, err := NewClient(ua,
		WithClientHostname("10.0.0.0"),
	)
	require.Nil(t, err)

	recipment := sip.Uri{
		User:      "bob",
		Host:      "10.2.2.2",
		Port:      5060,
		Headers:   sip.HeaderParams{{"transport", "udp"}},
		UriParams: sip.HeaderParams{{"foo", "bar"}},
	}

	req := sip.NewRequest(sip.OPTIONS, recipment)
	clientRequestBuildReq(c, req)

	via := req.Via()
	assert.Equal(t, "SIP/2.0/UDP 10.0.0.0;branch="+via.Params.GetOr("branch", ""), via.Value())

	from := req.From()
	// No ports should exists, headers, uriparams should exists, except tag
	assert.Equal(t, "\"sipgo\" <sip:sipgo@mydomain.com>;tag="+from.Params.GetOr("tag", ""), from.Value())

	to := req.To()
	// No ports should exists, headers, uriparams should exists
	assert.Equal(t, "<sip:bob@10.2.2.2>", to.Value())

	callid := req.CallID()
	assert.NotEmpty(t, callid.Value())

	cseq := req.CSeq()
	assert.True(t, cseq.SeqNo > 1)
	assert.Equal(t, fmt.Sprintf("%d %s", cseq.SeqNo, "OPTIONS"), cseq.Value())

	maxfwd := req.MaxForwards()
	assert.Equal(t, "70", maxfwd.Value())

	clen := req.ContentLength()
	assert.Equal(t, "0", clen.Value())
}

func TestClientRequestBuildWithNAT(t *testing.T) {
	// ua, err := NewUA(WithUserAgentIP(net.ParseIP("10.0.0.0")))
	ua, err := NewUA()
	require.Nil(t, err)

	c, err := NewClient(ua,
		WithClientHostname("10.0.0.0"),
		WithClientNAT(),
	)
	require.Nil(t, err)

	recipment := sip.Uri{
		User: "bob",
		Host: "10.2.2.2",
		Port: 5060,
	}

	req := sip.NewRequest(sip.OPTIONS, recipment)
	clientRequestBuildReq(c, req)

	via := req.Via()
	val := via.Value()
	params := strings.Split(val, ";")
	sort.Slice(params, func(i, j int) bool { return params[i] < params[j] })
	assert.Equal(t, "SIP/2.0/UDP 10.0.0.0;branch="+via.Params.GetOr("branch", "")+";rport", strings.Join(params, ";"))
}

func TestClientRequestBuildWithHostAndPort(t *testing.T) {
	// ua, err := NewUA(WithUserAgentIP(net.ParseIP("10.0.0.0")))
	ua, err := NewUA(
		WithUserAgentHostname("sip.myserver.com"),
	)
	require.Nil(t, err)

	c, err := NewClient(ua,
		WithClientHostname("sip.myserver.com"),
		WithClientPort(5066),
	)
	require.Nil(t, err)

	recipment := sip.Uri{
		User: "bob",
		Host: "10.2.2.2",
		Port: 5060,
	}

	req := sip.NewRequest(sip.OPTIONS, recipment)
	clientRequestBuildReq(c, req)

	via := req.Via()
	assert.Equal(t, "SIP/2.0/UDP sip.myserver.com:5066;branch="+via.Params.GetOr("branch", ""), via.Value())

	from := req.From()
	// No ports should exists
	assert.Equal(t, "\"sipgo\" <sip:sipgo@sip.myserver.com>;tag="+from.Params.GetOr("tag", ""), from.Value())

	to := req.To()
	// No port should exists or special values
	assert.Equal(t, "<sip:bob@10.2.2.2>", to.Value())
}

func TestClientRequestOptions(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	c, err := NewClient(ua,
		WithClientHostname("10.0.0.0"),
	)
	require.Nil(t, err)

	sender := sip.Uri{
		User: "alice",
		Host: "10.1.1.1",
		Port: 5060,
	}

	recipment := sip.Uri{
		User: "bob",
		Host: "10.2.2.2",
		Port: 5060,
	}

	// Proxy receives this request
	req := createSimpleRequest(sip.INVITE, sender, recipment, "UDP")
	oldvia := req.Via()
	assert.Equal(t, "Via: SIP/2.0/UDP 10.1.1.1:5060;branch="+oldvia.Params.GetOr("branch", ""), oldvia.String())

	// Proxy will add via header with client host
	err = ClientRequestAddVia(c, req)
	require.Nil(t, err)
	via := req.Via()
	tmpvia := *via // Save this for later usage
	assert.Equal(t, "Via: SIP/2.0/UDP 10.0.0.0;branch="+via.Params.GetOr("branch", ""), via.String())
	assert.NotEqual(t, via.Params.GetOr("branch", ""), oldvia.Params.GetOr("branch", ""))

	// Add Record Route
	err = ClientRequestAddRecordRoute(c, req)
	require.Nil(t, err)
	rr := req.RecordRoute()

	if strings.Contains(";lr;transport=udp", rr.String()) {
		assert.Equal(t, "Record-Route: <sip:10.0.0.0;lr;transport=udp>", rr.String())
	}
	if strings.Contains(";transport=udp;lr", rr.String()) {
		assert.Equal(t, "Record-Route: <sip:10.0.0.0;transport=udp;lr>", rr.String())
	}

	// When proxy gets response, he will remove via
	res := sip.NewResponseFromRequest(req, 400, "", nil)
	res.RemoveHeader("Via")
	viaprev := res.Via()
	assert.Equal(t, oldvia, viaprev)

	// Lets make via multivalue
	req = createSimpleRequest(sip.INVITE, sender, recipment, "UDP")
	via = req.Via()
	req.AppendHeader(&tmpvia)
	res = sip.NewResponseFromRequest(req, 400, "", nil)
	res.RemoveHeader("Via")
	viaprev = res.Via()
	assert.Equal(t, tmpvia.Host, viaprev.Host)

	assert.Len(t, res.GetHeaders("Via"), 1)
}

func TestClientRequestAddRoute(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	c, err := NewClient(ua, WithClientHostname("10.0.0.0"))
	require.Nil(t, err)

	sender := sip.Uri{User: "alice", Host: "10.1.1.1", Port: 5060}
	recipient := sip.Uri{User: "bob", Host: "10.2.2.2", Port: 5060}

	t.Run("AddsLooseRouteHeader", func(t *testing.T) {
		req := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		proxy := sip.Uri{Host: "proxy.example.com", Port: 5080}

		err := ClientRequestAddRoute(proxy)(c, req)
		require.Nil(t, err)

		got := req.Route()
		require.NotNil(t, got)
		assert.Equal(t, "proxy.example.com", got.Address.Host)
		assert.Equal(t, 5080, got.Address.Port)
		assert.True(t, got.Address.UriParams.Has("lr"))

		// Per RFC 3261, the request must be dispatched to the top Route URI,
		// not to the Request-URI host.
		assert.Equal(t, "proxy.example.com:5080", req.Destination())
		assert.Equal(t, recipient.Host, req.Recipient.Host, "Request-URI must be unchanged")
	})

	t.Run("PreservesExistingLrParam", func(t *testing.T) {
		req := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		params := sip.NewParams()
		params.Add("lr", "")
		params.Add("transport", "tcp")
		proxy := sip.Uri{Host: "proxy.example.com", Port: 5080, UriParams: params}

		err := ClientRequestAddRoute(proxy)(c, req)
		require.Nil(t, err)

		got := req.Route()
		require.NotNil(t, got)
		// Both params should be present and lr should not be duplicated.
		assert.True(t, got.Address.UriParams.Has("lr"))
		assert.Equal(t, "tcp", got.Address.UriParams.GetOr("transport", ""))
		lrCount := 0
		for _, p := range got.Address.UriParams {
			if p.K == "lr" {
				lrCount++
			}
		}
		assert.Equal(t, 1, lrCount, "lr parameter must not be duplicated")
	})

	t.Run("ClonesCallerURI", func(t *testing.T) {
		req := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		proxy := sip.Uri{Host: "proxy.example.com", Port: 5080}

		err := ClientRequestAddRoute(proxy)(c, req)
		require.Nil(t, err)

		// Mutating the URI passed to the option must not affect the header.
		proxy.Host = "tampered.example.com"
		assert.Equal(t, "proxy.example.com", req.Route().Address.Host)
	})
}

func TestClientOutboundProxy(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	sender := sip.Uri{User: "alice", Host: "10.1.1.1", Port: 5060}
	recipient := sip.Uri{User: "bob", Host: "10.2.2.2", Port: 5060}

	t.Run("OptionInjectsRouteOnBuild", func(t *testing.T) {
		c, err := NewClient(ua,
			WithClientHostname("10.0.0.0"),
			WithClientOutboundProxy("proxy.example.com:5080", ""),
		)
		require.Nil(t, err)
		hp, tp := c.OutboundProxy()
		assert.Equal(t, "proxy.example.com:5080", hp)
		assert.Equal(t, "", tp)

		req := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		require.Nil(t, clientRequestBuildReq(c, req))

		got := req.Route()
		require.NotNil(t, got)
		assert.Equal(t, "proxy.example.com", got.Address.Host)
		assert.Equal(t, 5080, got.Address.Port)
		assert.True(t, got.Address.UriParams.Has("lr"))

		// Destination must follow the proxy, not the Request-URI.
		assert.Equal(t, "proxy.example.com:5080", req.Destination())
		assert.Equal(t, recipient.Host, req.Recipient.Host)
	})

	t.Run("TransportStampedOnRouteAndVia", func(t *testing.T) {
		c, err := NewClient(ua,
			WithClientHostname("10.0.0.0"),
			WithClientOutboundProxy("proxy.example.com:5080", "TCP"),
		)
		require.Nil(t, err)
		hp, tp := c.OutboundProxy()
		assert.Equal(t, "proxy.example.com:5080", hp)
		assert.Equal(t, "tcp", tp, "transport must be normalized to lowercase")

		// Build a request without an explicit transport: the proxy's hint
		// must drive both Via and Request.Transport().
		req := sip.NewRequest(sip.INVITE, recipient)
		require.Nil(t, clientRequestBuildReq(c, req))

		assert.Equal(t, "tcp", req.Route().Address.UriParams.GetOr("transport", ""))
		assert.Equal(t, "TCP", req.Transport(), "Route ;transport= must drive Request.Transport()")
		assert.Equal(t, "TCP", req.Via().Transport, "Via must reflect the proxy's transport")
	})

	t.Run("RuntimeSetterAndClear", func(t *testing.T) {
		c, err := NewClient(ua, WithClientHostname("10.0.0.0"))
		require.Nil(t, err)
		hp, _ := c.OutboundProxy()
		assert.Equal(t, "", hp)

		require.Nil(t, c.SetOutboundProxy("proxy.example.com:5080", "tls"))
		hp, tp := c.OutboundProxy()
		assert.Equal(t, "proxy.example.com:5080", hp)
		assert.Equal(t, "tls", tp)

		req := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		require.Nil(t, clientRequestBuildReq(c, req))
		require.NotNil(t, req.Route())

		require.Nil(t, c.SetOutboundProxy("", ""))
		hp, tp = c.OutboundProxy()
		assert.Equal(t, "", hp)
		assert.Equal(t, "", tp)

		req2 := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		require.Nil(t, clientRequestBuildReq(c, req2))
		assert.Nil(t, req2.Route(), "no Route should be added once proxy is cleared")
	})

	t.Run("InvalidHostPortReturnsError", func(t *testing.T) {
		c, err := NewClient(ua, WithClientHostname("10.0.0.0"))
		require.Nil(t, err)
		require.Error(t, c.SetOutboundProxy("not a host port", ""))
		hp, _ := c.OutboundProxy()
		assert.Equal(t, "", hp)
	})

	t.Run("EmptyTransportDoesNotStampParam", func(t *testing.T) {
		c, err := NewClient(ua,
			WithClientHostname("10.0.0.0"),
			WithClientOutboundProxy("proxy.example.com:5080", ""),
		)
		require.Nil(t, err)

		req := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		require.Nil(t, clientRequestBuildReq(c, req))

		got := req.Route()
		require.NotNil(t, got)
		require.NotNil(t, got.Address.UriParams)
		assert.True(t, got.Address.UriParams.Has("lr"))
		_, ok := got.Address.UriParams.Get("transport")
		assert.False(t, ok, "no ;transport= must be stamped when caller passes empty transport")
	})

	t.Run("RecipientTransportRespectedWhenProxyTransportEmpty", func(t *testing.T) {
		c, err := NewClient(ua,
			WithClientHostname("10.0.0.0"),
			WithClientOutboundProxy("proxy.example.com:5080", ""),
		)
		require.Nil(t, err)

		// Recipient carries the transport hint; outbound proxy does not.
		recipientTCP := sip.Uri{
			User:      "bob",
			Host:      "10.2.2.2",
			Port:      5060,
			UriParams: sip.HeaderParams{{K: "transport", V: "tcp"}},
		}
		req := sip.NewRequest(sip.INVITE, recipientTCP)
		require.Nil(t, clientRequestBuildReq(c, req))

		// Destination still follows the proxy.
		assert.Equal(t, "proxy.example.com:5080", req.Destination())
		// But the Recipient's transport hint must drive Via and Transport,
		// since the proxy did not specify its own transport.
		assert.Equal(t, "TCP", req.Via().Transport, "Via must inherit Recipient transport when proxy transport is empty")
		assert.Equal(t, "TCP", req.Transport(), "Request.Transport() must reflect the inherited transport")
	})

	t.Run("RuntimeSwapReflectedOnNextBuild", func(t *testing.T) {
		c, err := NewClient(ua, WithClientHostname("10.0.0.0"))
		require.Nil(t, err)

		require.Nil(t, c.SetOutboundProxy("proxy.example.com:5080", "tcp"))
		req1 := sip.NewRequest(sip.INVITE, recipient)
		require.Nil(t, clientRequestBuildReq(c, req1))
		assert.Equal(t, "TCP", req1.Transport())
		assert.Equal(t, "tcp", req1.Route().Address.UriParams.GetOr("transport", ""))

		require.Nil(t, c.SetOutboundProxy("proxy.example.com:5080", "tls"))
		req2 := sip.NewRequest(sip.INVITE, recipient)
		require.Nil(t, clientRequestBuildReq(c, req2))
		assert.Equal(t, "TLS", req2.Transport(), "next build must observe the swapped transport")
		assert.Equal(t, "tls", req2.Route().Address.UriParams.GetOr("transport", ""))
	})

	t.Run("DoesNotOverrideExistingRoute", func(t *testing.T) {
		c, err := NewClient(ua,
			WithClientHostname("10.0.0.0"),
			WithClientOutboundProxy("proxy.example.com:5080", "tcp"),
		)
		require.Nil(t, err)

		req := createSimpleRequest(sip.INVITE, sender, recipient, "UDP")
		// Simulate an in-dialog request that already carries a Route from
		// Record-Route processing.
		params := sip.NewParams()
		params.Add("lr", "")
		req.PrependHeader(&sip.RouteHeader{
			Address: sip.Uri{Host: "indialog.example.com", Port: 5060, UriParams: params},
		})

		require.Nil(t, clientRequestBuildReq(c, req))

		routes := req.GetHeaders("Route")
		assert.Len(t, routes, 1, "outbound proxy must not stack on top of an existing Route")
		assert.Equal(t, "indialog.example.com", req.Route().Address.Host)
	})
}

/* func TestClientVia(t *testing.T) {
	ua, err := NewUA()
	require.Nil(t, err)

	c, err := NewClient(ua,
		WithClientHostname("10.0.0.0"),
	)
	require.Nil(t, err)

	tp := ua.tp

	req := sip.NewRequest(sip.OPTIONS, &sip.Uri{User: "test", Host: "example.com", Port: 5060})
	err = clientRequestBuildReq(c, req)
	require.NoError(t, err)

	conn, err := tp.ClientRequestConnection(context.TODO(), req)
}
*/

func TestClientViaRouting(t *testing.T) {
	ua, _ := NewUA()
	client, err := NewClient(ua,
		WithClientHostname("myhost.xy"),
		WithClientPort(5060),
	)
	require.NoError(t, err)

	client.TxRequester = &siptest.ClientTxRequesterResponder{
		OnRequest: func(req *sip.Request, w *siptest.ClientTxResponder) {
			res := sip.NewResponseFromRequest(req, 200, "OK", nil)
			w.Receive(res)
		},
	}

	options := sip.NewRequest(sip.OPTIONS, sip.Uri{User: "test", Host: "localhost"})
	_, err = client.Do(context.TODO(), options)
	require.NoError(t, err)

	via := options.Via()
	assert.Equal(t, "myhost.xy", via.Host)
	assert.Equal(t, 5060, via.Port)
}

func TestClientWriteRequestRejectsCRLFAfterBuild(t *testing.T) {
	ua, err := NewUA()
	require.NoError(t, err)

	client, err := NewClient(ua, WithClientHostname("127.0.0.1"))
	require.NoError(t, err)

	called := false
	client.TxRequester = clientTxRequesterFunc(func(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
		called = true
		return nil, nil
	})

	req := sip.NewRequest(sip.REGISTER, sip.Uri{User: "foo", Host: "example.com"})
	req.AppendHeader(sip.NewHeader("Subject", "injected\r\nContent-Length: 0"))

	err = client.WriteRequest(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid CRLF")
	require.False(t, called)
	require.NotNil(t, req.Via())
}

func TestClientTransactionRequestRejectsCRLFAfterBuild(t *testing.T) {
	ua, err := NewUA()
	require.NoError(t, err)

	client, err := NewClient(ua, WithClientHostname("127.0.0.1"))
	require.NoError(t, err)

	called := false
	client.TxRequester = clientTxRequesterFunc(func(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
		called = true
		return nil, nil
	})

	req := sip.NewRequest(sip.REGISTER, sip.Uri{User: "foo", Host: "example.com"})
	req.AppendHeader(sip.NewHeader("Subject", "injected\r\nContent-Length: 0"))

	_, err = client.TransactionRequest(context.Background(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid CRLF")
	require.False(t, called)
	require.NotNil(t, req.Via())
}

type clientTxRequesterFunc func(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error)

func (f clientTxRequesterFunc) Request(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	return f(ctx, req)
}

func TestIntegrationClientViaBindHost(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	{
		ua, _ := NewUA()
		defer ua.Close()
		srv, err := NewServer(ua)
		require.NoError(t, err)

		startTestServer(ctx, srv, "127.0.0.1:15099")
		srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
			res := sip.NewResponseFromRequest(req, 200, "OK", nil)
			tx.Respond(res)
		})
	}

	ua, _ := NewUA()
	defer ua.Close()
	client, err := NewClient(ua,
		WithClientHostname("127.0.0.1"),
		WithClientPort(15090),
		WithClientConnectionAddr("127.0.0.1:16099"),
	)
	require.NoError(t, err)

	options := sip.NewRequest(sip.OPTIONS, sip.Uri{User: "test", Host: "localhost"})
	tx, err := client.TransactionRequest(context.TODO(), options)
	require.NoError(t, err)

	clientTx := tx.(*sip.ClientTx)
	conn := clientTx.Connection()

	laddr := conn.LocalAddr()
	assert.Equal(t, "127.0.0.1:16099", laddr.String())

	via := options.Via()
	assert.Equal(t, "127.0.0.1", via.Host)
	assert.Equal(t, 15090, via.Port)
}

func TestDigestAuthLowerCase(t *testing.T) {
	challenge := `Digest username="user", realm="asterisk", nonce="662d65a084b88c6d2a745a9de086fa91", uri="sip:+user@example.com", algorithm=sha-256, response="3681b63e5d9c3bb80e5350e2783d7b88"`
	chal, err := digest.ParseChallenge(challenge)
	require.NoError(t, err)
	chal.Algorithm = sip.ASCIIToUpper(chal.Algorithm)

	_, err = digest.Digest(chal, digest.Options{
		Method:   "INVITE",
		Username: "user",
		URI:      "sip:+user@example.com",
	})
	require.NoError(t, err)
}

func TestIntegrationClientParalelDialing(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	ua, err := NewUA()
	require.NoError(t, err)
	defer ua.Close()

	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	defer l.Close()
	go func() {
		io.ReadAll(l)
	}()
	_, dstPort, err := sip.ParseAddr(l.LocalAddr().String())
	require.NoError(t, err)

	c, err := NewClient(ua,
		WithClientHostname("10.0.0.0"),
		WithClientConnectionAddr("127.0.0.1:15066"),
	)
	require.NoError(t, err)
	wg := sync.WaitGroup{}
	defer t.Log("Exiting")
	for i := 0; i < 2*runtime.NumCPU(); i++ {
		wg.Add(1)
		t.Log("Running", i)
		go func() {
			defer wg.Done()
			req := sip.NewRequest(sip.INVITE, sip.Uri{Host: "127.0.0.1", Port: dstPort})
			err := c.WriteRequest(req)
			require.NoError(t, err)
		}()
	}

	wg.Wait()

	// Check that connection reference count
	conn, err := ua.TransportLayer().GetConnection("udp", "127.0.0.1:15066")
	require.NoError(t, err)
	assert.Equal(t, 3, conn.Ref(0))
}

func BenchmarkClientTransactionRequestBuild(t *testing.B) {
	ua, err := NewUA()
	require.Nil(t, err)

	c, err := NewClient(ua,
		WithClientHostname("10.0.0.0"),
	)
	for i := 0; i < t.N; i++ {
		req := sip.NewRequest(sip.INVITE, sip.Uri{User: "test", Host: "localhost"})
		clientRequestBuildReq(c, req)
	}
}
