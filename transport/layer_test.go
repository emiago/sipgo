package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sipgo "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	"github.com/livekit/sipgo/fakes"
	"github.com/livekit/sipgo/sip"
)

// fakeDNS is a minimal UDP DNS server answering A, AAAA and SRV queries from a table.
// It exists so the resolver can be driven through a real *net.Resolver, which is
// the only type Layer accepts.
type fakeDNS struct {
	t    *testing.T
	conn *net.UDPConn

	a   map[string][]string  // hostname -> addresses, served as A or AAAA by family
	srv map[string][]fakeSRV // owner name -> records

	mu   sync.Mutex
	seen map[string]int // qname/qtype -> count

	done sync.WaitGroup
}

type fakeSRV struct {
	target string
	port   uint16
}

const (
	typeA    = 1
	typeAAAA = 28
	typeSRV  = 33
)

func newFakeDNS(t *testing.T, a map[string][]string, srv map[string][]fakeSRV) *fakeDNS {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := &fakeDNS{t: t, conn: conn, a: a, srv: srv, seen: map[string]int{}}
	d.done.Add(1)
	go d.serve()
	// Closing the socket makes the read in serve fail, which ends the goroutine.
	// Wait for it so it cannot outlive the test.
	t.Cleanup(func() {
		_ = conn.Close()
		d.done.Wait()
	})
	return d
}

// resolver returns a *net.Resolver that queries only this server.
func (d *fakeDNS) resolver() *net.Resolver {
	addr := d.conn.LocalAddr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "udp", addr)
		},
	}
}

func (d *fakeDNS) queries(name string, qtype uint16) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return d.seen[strings.ToLower(name)+"/"+string(rune(qtype))]
}

func (d *fakeDNS) serve() {
	defer d.done.Done()
	buf := make([]byte, 512)
	for {
		n, from, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		resp := d.respond(buf[:n])
		if resp != nil {
			_, _ = d.conn.WriteToUDP(resp, from)
		}
	}
}

// respond parses the question section and builds an answer. Only the subset of
// the wire format these tests need is implemented.
func (d *fakeDNS) respond(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	name, off, ok := readName(q, 12)
	if !ok || off+4 > len(q) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(q[off : off+2])
	d.mu.Lock()
	d.seen[strings.ToLower(name)+"/"+string(rune(qtype))]++
	d.mu.Unlock()

	var answers []byte
	var count int
	switch qtype {
	case typeA:
		for _, ip := range d.a[strings.TrimSuffix(name, ".")] {
			v4 := net.ParseIP(ip).To4()
			if v4 == nil {
				continue
			}
			answers = append(answers, encodeName(name)...)
			answers = append(answers, rrHeader(typeA, 4)...)
			answers = append(answers, v4...)
			count++
		}
	case typeAAAA:
		for _, ip := range d.a[strings.TrimSuffix(name, ".")] {
			parsed := net.ParseIP(ip)
			if parsed.To4() != nil {
				continue
			}
			answers = append(answers, encodeName(name)...)
			answers = append(answers, rrHeader(typeAAAA, 16)...)
			answers = append(answers, parsed.To16()...)
			count++
		}
	case typeSRV:
		for _, rec := range d.srv[strings.TrimSuffix(name, ".")] {
			target := encodeName(rec.target)
			rdata := make([]byte, 6, 6+len(target))
			binary.BigEndian.PutUint16(rdata[0:], 5)  // priority
			binary.BigEndian.PutUint16(rdata[2:], 50) // weight
			binary.BigEndian.PutUint16(rdata[4:], rec.port)
			rdata = append(rdata, target...)

			answers = append(answers, encodeName(name)...)
			answers = append(answers, rrHeader(typeSRV, uint16(len(rdata)))...)
			answers = append(answers, rdata...)
			count++
		}
	}

	resp := make([]byte, 12)
	copy(resp, q[:2])                                   // transaction ID
	binary.BigEndian.PutUint16(resp[2:], 0x8180)        // response, recursion available
	binary.BigEndian.PutUint16(resp[4:], 1)             // question count
	binary.BigEndian.PutUint16(resp[6:], uint16(count)) // answer count
	if count == 0 {
		binary.BigEndian.PutUint16(resp[2:], 0x8183) // NXDOMAIN
	}
	resp = append(resp, q[12:off+4]...) // echo the question
	return append(resp, answers...)
}

func rrHeader(qtype, rdlen uint16) []byte {
	b := make([]byte, 10)
	binary.BigEndian.PutUint16(b[0:], qtype)
	binary.BigEndian.PutUint16(b[2:], 1)  // class IN
	binary.BigEndian.PutUint32(b[4:], 60) // TTL
	binary.BigEndian.PutUint16(b[8:], rdlen)
	return b
}

func encodeName(name string) []byte {
	name = strings.TrimSuffix(name, ".")
	var b []byte
	for _, label := range strings.Split(name, ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	return append(b, 0)
}

func readName(msg []byte, off int) (string, int, bool) {
	var sb strings.Builder
	for off < len(msg) {
		n := int(msg[off])
		off++
		if n == 0 {
			return sb.String(), off, true
		}
		if n > 63 || off+n > len(msg) {
			return "", 0, false
		}
		sb.Write(msg[off : off+n])
		sb.WriteByte('.')
		off += n
	}
	return "", 0, false
}

// A carrier layout where two servers sit on different ports behind one hostname
// whose A record lists both of their addresses.
const (
	testSRVHost = "trunk.example.com"
	testSBC1IP  = "198.51.100.11"
	testSBC2IP  = "198.51.100.12"
	testSBC1    = "sbc1.example.net"
	testSBC2    = "sbc2.example.net"
)

func newTwoSBCDNS(t *testing.T, hostA []string) *fakeDNS {
	return newFakeDNS(t,
		map[string][]string{
			testSBC1:    {testSBC1IP},
			testSBC2:    {testSBC2IP},
			testSRVHost: hostA,
		},
		map[string][]fakeSRV{
			"_sip._udp." + testSRVHost: {
				{target: testSBC1, port: 5006},
				{target: testSBC2, port: 5008},
			},
		},
	)
}

func newTestLayer(t *testing.T, d *fakeDNS) *Layer {
	t.Helper()
	l := NewLayer(slog.Default(), d.resolver(), sipgo.NewParser(), nil, nil)
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func resolveWithin(t *testing.T, fn func(ctx context.Context) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return fn(ctx)
}

// Both SRV records share a priority and weight, so Go randomises which one wins;
// that is load balancing and is fine. What must never happen is a host from one
// record paired with the port from the other, which sends the request to a port
// that host does not serve.
func TestResolveAddrPreferSRVNeverCrossPairsHostAndPort(t *testing.T) {
	d := newTwoSBCDNS(t, []string{testSBC1IP, testSBC2IP})
	l := newTestLayer(t, d)

	valid := map[string]bool{
		testSBC1IP + ":5006": true,
		testSBC2IP + ":5008": true,
	}
	seen := map[string]bool{}

	// Records of equal priority and weight come back in random order, so repeat
	// until both have won. Either is a fine choice; only the pairing is fixed.
	const rounds = 30
	for i := 0; i < rounds; i++ {
		var got netip.AddrPort
		err := resolveWithin(t, func(ctx context.Context) error {
			var err error
			got, err = l.ResolveAddrPreferSRV(ctx, "udp", testSRVHost, 0, "sip")
			return err
		})
		if err != nil {
			t.Fatalf("ResolveAddrPreferSRV: %v", err)
		}
		if !valid[got.String()] {
			t.Fatalf("got %s, which pairs a host with another record's port", got)
		}
		seen[got.String()] = true
	}

	if len(seen) != 2 {
		t.Fatalf("only saw %v in %d resolutions, so one record went unchecked", seen, rounds)
	}

	// The hostname's own A record lists both servers. Reading it is what paired an
	// address with another record's port, so it must never be consulted here.
	if n := d.queries(testSRVHost, typeA); n != 0 {
		t.Fatalf("the hostname's A record was queried %d times", n)
	}
}

// An explicit port on the request URI means the port was chosen for us, so SRV
// must not be consulted at all. RFC 3263 4.2.
func TestResolveAddrPreferSRVExplicitPortSkipsSRV(t *testing.T) {
	d := newTwoSBCDNS(t, []string{testSBC1IP, testSBC2IP})
	l := newTestLayer(t, d)

	var got netip.AddrPort
	err := resolveWithin(t, func(ctx context.Context) error {
		var err error
		got, err = l.ResolveAddrPreferSRV(ctx, "udp", testSRVHost, 5006, "sip")
		return err
	})
	if err != nil {
		t.Fatalf("ResolveAddrPreferSRV: %v", err)
	}
	if n := d.queries("_sip._udp."+testSRVHost, typeSRV); n != 0 {
		t.Fatalf("SRV was queried %d times despite an explicit port", n)
	}
	if got.Port() != 5006 {
		t.Fatalf("port %d, want 5006", got.Port())
	}
}

// Without a port, SRV supplies both halves of the address.
func TestResolveAddrPreferSRVNoPortUsesSRV(t *testing.T) {
	d := newTwoSBCDNS(t, []string{testSBC1IP, testSBC2IP})
	l := newTestLayer(t, d)

	var got netip.AddrPort
	err := resolveWithin(t, func(ctx context.Context) error {
		var err error
		got, err = l.ResolveAddrPreferSRV(ctx, "udp", testSRVHost, 0, "sip")
		return err
	})
	if err != nil {
		t.Fatalf("ResolveAddrPreferSRV: %v", err)
	}
	if n := d.queries("_sip._udp."+testSRVHost, typeSRV); n == 0 {
		t.Fatal("SRV was not queried for a URI without a port")
	}
	if got.Port() == 5060 {
		t.Fatal("port stayed at the default, so the SRV port was not applied")
	}
}

// An IP literal needs no lookup at all.
func TestResolveAddrPreferSRVIPLiteral(t *testing.T) {
	d := newTwoSBCDNS(t, []string{testSBC1IP})
	l := newTestLayer(t, d)

	var got netip.AddrPort
	err := resolveWithin(t, func(ctx context.Context) error {
		var err error
		got, err = l.ResolveAddrPreferSRV(ctx, "udp", testSBC1IP, 5006, "sip")
		return err
	})
	if err != nil {
		t.Fatalf("ResolveAddrPreferSRV: %v", err)
	}
	if want := testSBC1IP + ":5006"; got.String() != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// With no port and no SRV records, the host lookup supplies the address and the
// transport default supplies the port.
func TestResolveAddrPreferSRVFallsBackToHostLookup(t *testing.T) {
	d := newFakeDNS(t, map[string][]string{"plain.example.com": {"192.0.2.10"}}, nil)
	l := newTestLayer(t, d)

	var got netip.AddrPort
	err := resolveWithin(t, func(ctx context.Context) error {
		var err error
		got, err = l.ResolveAddrPreferSRV(ctx, "udp", "plain.example.com", 0, "sip")
		return err
	})
	if err != nil {
		t.Fatalf("ResolveAddrPreferSRV: %v", err)
	}
	if want := "192.0.2.10:5060"; got.String() != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A failing SRV lookup must not leave its port behind for the fallback lookup to
// pair with an unrelated address.
func TestResolveAddrPreferSRVFailureLeavesPortAlone(t *testing.T) {
	// SRV points at a target that does not resolve; the host itself does.
	d := newFakeDNS(t,
		map[string][]string{"mixed.example.com": {"192.0.2.60"}},
		map[string][]fakeSRV{
			"_sip._udp.mixed.example.com": {{target: "missing.example.com", port: 5006}},
		})
	l := newTestLayer(t, d)

	var got netip.AddrPort
	err := resolveWithin(t, func(ctx context.Context) error {
		var err error
		got, err = l.ResolveAddrPreferSRV(ctx, "udp", "mixed.example.com", 0, "sip")
		return err
	})
	if err != nil {
		t.Fatalf("ResolveAddrPreferSRV: %v", err)
	}
	// Fell back to the host lookup, so the port must be the transport default.
	if want := "192.0.2.60:5060"; got.String() != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// With preferSRV false the host resolves first and SRV is only the fallback.
// Requests sent through the layer take this path. Every trunk but the SRV ones
// relies on it, and an SRV lookup they never need is a round trip per call.
func TestResolveAddrHostFirst(t *testing.T) {
	d := newTwoSBCDNS(t, []string{testSBC1IP})
	l := newTestLayer(t, d)

	var addr Addr
	addr.Port = 5060
	err := resolveWithin(t, func(ctx context.Context) error {
		return l.resolveAddr(ctx, "sip", "udp", testSRVHost, false, &addr)
	})
	if err != nil {
		t.Fatalf("resolveAddr: %v", err)
	}
	if want := testSBC1IP + ":5060"; addr.String() != want {
		t.Fatalf("got %s, want %s", addr.String(), want)
	}
	if n := d.queries("_sip._udp."+testSRVHost, typeSRV); n != 0 {
		t.Fatalf("SRV was queried %d times for a host that resolves", n)
	}
}

// The other direction: no A record, so SRV is what answers.
func TestResolveAddrHostFirstFallsBackToSRV(t *testing.T) {
	const host = "srvonly.example.com"
	d := newFakeDNS(t,
		map[string][]string{"edge.example.com": {"192.0.2.90"}},
		map[string][]fakeSRV{
			"_sip._udp." + host: {{target: "edge.example.com", port: 5062}},
		})
	l := newTestLayer(t, d)

	var addr Addr
	addr.Port = 5060
	err := resolveWithin(t, func(ctx context.Context) error {
		return l.resolveAddr(ctx, "sip", "udp", host, false, &addr)
	})
	if err != nil {
		t.Fatalf("resolveAddr: %v", err)
	}
	if want := "192.0.2.90:5062"; addr.String() != want {
		t.Fatalf("got %s, want %s", addr.String(), want)
	}
}

// The address picked for an SRV target follows the same family preference as a
// plain host lookup. Each preference is tested because the resolver's ordering
// of A and AAAA answers depends on the machine running the test.
func TestResolveAddrSRVTargetUsesIPPreference(t *testing.T) {
	const host = "dualstack.example.com"
	d := newFakeDNS(t,
		map[string][]string{"edge.example.com": {"2001:db8::91", "192.0.2.91"}},
		map[string][]fakeSRV{
			"_sip._udp." + host: {{target: "edge.example.com", port: 5062}},
		})

	for _, tc := range []struct {
		prefer dnsIPPreference
		want   string
	}{
		{dnsPreferIPv4, "192.0.2.91:5062"},
		{dnsPreferIPv6, "[2001:db8::91]:5062"},
	} {
		l := newTestLayer(t, d)
		l.dnsPreferIP = tc.prefer

		var addr Addr
		err := resolveWithin(t, func(ctx context.Context) error {
			return l.resolveAddrSRV(ctx, "sip", "udp", host, &addr)
		})
		if err != nil {
			t.Fatalf("resolveAddrSRV: %v", err)
		}
		if addr.String() != tc.want {
			t.Fatalf("prefer %d: got %s, want %s", tc.prefer, addr.String(), tc.want)
		}
	}
}

func newTestRequest(host string, port int) *sipgo.Request {
	req := sipgo.NewRequest(sipgo.INVITE, sipgo.Uri{Scheme: "sip", User: "alice", Host: host, Port: port})
	req.AppendHeader(&sipgo.ViaHeader{
		ProtocolName:    "SIP",
		ProtocolVersion: "2.0",
		Transport:       "UDP",
		Params:          sipgo.NewParams(),
	})
	return req
}

// A port in the request URI is kept. When the host has no address records, the
// request fails rather than going to whatever port an SRV record names.
func TestClientRequestConnectionExplicitPortSkipsSRV(t *testing.T) {
	const host = "srvonly.example.com"
	d := newFakeDNS(t,
		map[string][]string{"edge.example.com": {"192.0.2.92"}},
		map[string][]fakeSRV{
			"_sip._udp." + host: {{target: "edge.example.com", port: 5062}},
		})
	l := newTestLayer(t, d)

	req := newTestRequest(host, 5090)
	if _, err := l.ClientRequestConnection(req); err == nil {
		t.Fatalf("expected an error, request went to %s", req.Destination())
	}
	if n := d.queries("_sip._udp."+host, typeSRV); n != 0 {
		t.Fatalf("SRV was queried %d times for a URI with a port", n)
	}
}

// Without a port in the URI, an SRV-only host still resolves through SRV.
func TestClientRequestConnectionNoPortFallsBackToSRV(t *testing.T) {
	const host = "srvonly.example.com"
	d := newFakeDNS(t,
		map[string][]string{"edge.example.com": {"192.0.2.93"}},
		map[string][]fakeSRV{
			"_sip._udp." + host: {{target: "edge.example.com", port: 5062}},
		})
	l := newTestLayer(t, d)

	req := newTestRequest(host, 0)
	if _, err := l.ClientRequestConnection(req); err != nil {
		t.Fatalf("ClientRequestConnection: %v", err)
	}
	if want := "192.0.2.93:5062"; req.Destination() != want {
		t.Fatalf("got %s, want %s", req.Destination(), want)
	}
}

func TestDestinationHasPort(t *testing.T) {
	req := newTestRequest("example.com", 0)
	if destinationHasPort(req) {
		t.Fatal("URI without a port reported one")
	}

	req = newTestRequest("example.com", 5090)
	if !destinationHasPort(req) {
		t.Fatal("URI with a port reported none")
	}

	// A Route header overrides the request URI.
	req.AppendHeader(&sipgo.RouteHeader{Address: sipgo.Uri{Host: "proxy.example.com"}})
	if destinationHasPort(req) {
		t.Fatal("Route without a port reported one")
	}

	req.SetDestination("proxy.example.com:5070")
	if !destinationHasPort(req) {
		t.Fatal("explicit destination reported no port")
	}

	req.SetDestination("proxy.example.com:0")
	if destinationHasPort(req) {
		t.Fatal("explicit destination with port 0 reported a port")
	}
}

// An SRV target that does not resolve is an error, not a nil IP passed onward.
func TestResolveAddrSRVTargetMustResolve(t *testing.T) {
	d := newFakeDNS(t, nil, map[string][]fakeSRV{
		"_sip._udp.broken.example.com": {{target: "missing.example.com", port: 5006}},
	})
	l := newTestLayer(t, d)

	var addr Addr
	addr.Port = 5060
	err := resolveWithin(t, func(ctx context.Context) error {
		return l.resolveAddrSRV(ctx, "sip", "udp", "broken.example.com", &addr)
	})
	if err == nil {
		t.Fatalf("expected an error, got addr %s", addr.String())
	}
}

// A host with no SRV records must produce an error, not a target.
func TestResolveAddrSRVEmptyAnswerIsAnError(t *testing.T) {
	d := newFakeDNS(t, map[string][]string{"empty.example.com": {"192.0.2.50"}},
		map[string][]fakeSRV{"_sip._udp.empty.example.com": {}})
	l := newTestLayer(t, d)

	var addr Addr
	addr.Port = 5060
	err := resolveWithin(t, func(ctx context.Context) error {
		return l.resolveAddrSRV(ctx, "sip", "udp", "empty.example.com", &addr)
	})
	if err == nil {
		t.Fatalf("expected an error, got %s", addr.String())
	}
}

// A sip: URI carried over TLS looks up _sips._tcp, not _sip._tcp and not
// _sips._tls.
func TestResolveAddrPreferSRVTLSUsesSipsTCP(t *testing.T) {
	const host = "secure.example.com"
	d := newFakeDNS(t,
		map[string][]string{"edge.example.com": {"192.0.2.70"}},
		map[string][]fakeSRV{
			"_sips._tcp." + host: {{target: "edge.example.com", port: 5061}},
		})
	l := newTestLayer(t, d)

	// Scheme stays sip; the TLS transport is what selects the sips service.
	var got netip.AddrPort
	err := resolveWithin(t, func(ctx context.Context) error {
		var err error
		got, err = l.ResolveAddrPreferSRV(ctx, "tls", host, 0, "sip")
		return err
	})
	if err != nil {
		t.Fatalf("ResolveAddrPreferSRV: %v", err)
	}
	if want := "192.0.2.70:5061"; got.String() != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if n := d.queries("_sip._tcp."+host, typeSRV); n != 0 {
		t.Fatalf("_sip._tcp was queried %d times for a TLS transport", n)
	}
}

// A WebSocket transport resolves through _sip._ws rather than _sip._tcp.
func TestResolveAddrPreferSRVWSUsesSipWS(t *testing.T) {
	const host = "ws.example.com"
	d := newFakeDNS(t,
		map[string][]string{"edge.example.com": {"192.0.2.80"}},
		map[string][]fakeSRV{
			"_sip._ws." + host: {{target: "edge.example.com", port: 8080}},
		})
	l := newTestLayer(t, d)

	var got netip.AddrPort
	err := resolveWithin(t, func(ctx context.Context) error {
		var err error
		got, err = l.ResolveAddrPreferSRV(ctx, "ws", host, 0, "sip")
		return err
	})
	if err != nil {
		t.Fatalf("ResolveAddrPreferSRV: %v", err)
	}
	if want := "192.0.2.80:8080"; got.String() != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if n := d.queries("_sip._tcp."+host, typeSRV); n != 0 {
		t.Fatalf("_sip._tcp was queried %d times for a ws transport", n)
	}
}

// srvLabels is the single place transports become SRV names.
func TestSRVLabels(t *testing.T) {
	for _, tc := range []struct {
		network, uriScheme string
		want               string // the owner name prefix these labels build
	}{
		{"udp", "sip", "_sip._udp"},
		{"udp4", "sip", "_sip._udp"},
		{"tcp", "sip", "_sip._tcp"},
		{"tls", "sip", "_sips._tcp"}, // RFC 3263 4.1, not _sips._tls
		{"tls", "sips", "_sips._tcp"},
		{"ws", "sip", "_sip._ws"},    // RFC 7118 6
		{"wss", "sip", "_sips._wss"}, // secure transport wins over the scheme
		{"wss", "sips", "_sips._wss"},
	} {
		service, protocol := srvLabels(tc.network, tc.uriScheme)
		if got := "_" + service + "._" + protocol; got != tc.want {
			t.Errorf("srvLabels(%q, %q) = %s, want %s", tc.network, tc.uriScheme, got, tc.want)
		}
	}
}

// dialCountingTransport counts dials and holds each one until release closes, so
// a test can pile requests up behind a dial in progress.
type dialCountingTransport struct {
	Transport
	pool    *ConnectionPool
	dials   atomic.Int32
	release chan struct{}
	err     error
}

func (t *dialCountingTransport) Close() error { return nil }

func (t *dialCountingTransport) GetConnection(addr string) (Connection, error) {
	if c := t.pool.Get(addr); c != nil {
		return c, nil
	}
	return nil, nil
}

func (t *dialCountingTransport) CreateConnection(laddr Addr, host string, raddr Addr, handler sip.MessageHandler) (Connection, error) {
	t.dials.Add(1)
	<-t.release
	if t.err != nil {
		return nil, t.err
	}
	c := &TCPConnection{
		Conn: &fakes.TCPConn{
			LAddr: net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40000},
			RAddr: net.TCPAddr{IP: raddr.IP, Port: raddr.Port},
		},
		log:      slog.Default(),
		refcount: 2, // The pool's and the dialer's.
	}
	t.pool.Add(raddr.String(), c)
	return c, nil
}

// requestsDuringDial sends n requests to one address while the first dial is
// held, then lets it finish.
func requestsDuringDial(t *testing.T, tr *dialCountingTransport, n int) ([]Connection, []error) {
	t.Helper()
	l := NewLayer(slog.Default(), net.DefaultResolver, sipgo.NewParser(), nil, nil)
	t.Cleanup(func() { _ = l.Close() })
	l.transports["tcp"] = tr

	conns := make([]Connection, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := newTestRequest("192.0.2.10", 5060)
			req.SetTransport("TCP")
			conns[i], errs[i] = l.ClientRequestConnection(req)
		}()
	}

	require.Eventually(t, func() bool { return tr.dials.Load() == 1 }, time.Second, time.Millisecond)
	// Let the rest reach the dial in progress.
	time.Sleep(50 * time.Millisecond)
	close(tr.release)
	wg.Wait()
	return conns, errs
}

// Concurrent requests to an address with no connection share one dial, and each
// holds its own reference to the result.
func TestClientRequestConnectionSharesDial(t *testing.T) {
	const n = 10
	tr := &dialCountingTransport{pool: NewConnectionPool(), release: make(chan struct{})}
	conns, errs := requestsDuringDial(t, tr, n)

	require.EqualValues(t, 1, tr.dials.Load())
	for i := range n {
		require.NoError(t, errs[i])
		require.Same(t, conns[0], conns[i])
	}
	require.Equal(t, 1+n, conns[0].Ref(0))
}

// A failed dial fails every request waiting on it rather than each dialing in
// turn.
func TestClientRequestConnectionSharesDialError(t *testing.T) {
	const n = 10
	dialErr := errors.New("dial failed")
	tr := &dialCountingTransport{pool: NewConnectionPool(), release: make(chan struct{}), err: dialErr}
	_, errs := requestsDuringDial(t, tr, n)

	require.EqualValues(t, 1, tr.dials.Load())
	for i := range n {
		require.ErrorIs(t, errs[i], dialErr)
	}
}
