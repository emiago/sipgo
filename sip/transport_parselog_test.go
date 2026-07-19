package sip

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// logCapture is a slog.Handler retaining every record so a test can assert on
// the level and the attributes a code path emits. Enabled reports true for all
// levels so a record is captured whatever the default level is.
type logCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *logCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *logCapture) WithGroup(string) slog.Handler { return h }

func (h *logCapture) find(t *testing.T, msg string) slog.Record {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			return r
		}
	}
	t.Fatalf("no %q record captured", msg)
	return slog.Record{}
}

// flatten renders the message and every attribute of a record, so an assertion
// can cover all fields at once rather than the ones it thought to name.
func flatten(r slog.Record) string {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	return b.String()
}

// digestResponse stands in for the credential a peer puts on the wire before
// anything has authenticated it, and before the parse fails.
const digestResponse = "6629fae49393a05397450978507c4ef1"

// malformedRegister carries a complete digest Authorization header and then
// fails to parse on its CSeq, the shape of a REGISTER retry after a 401.
var malformedRegister = []byte("REGISTER sip:example.com SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bKnashds7\r\n" +
	"From: <sip:alice@example.com>;tag=1\r\n" +
	"To: <sip:alice@example.com>\r\n" +
	"Call-ID: 1@10.0.0.1\r\n" +
	"Authorization: Digest username=\"alice\", realm=\"example.com\", " +
	"nonce=\"dcd98b7102dd2f0e\", uri=\"sip:example.com\", response=\"" + digestResponse + "\"\r\n" +
	"CSeq: NOTANUMBER REGISTER\r\n" +
	"Content-Length: 0\r\n" +
	"\r\n")

// TestTransportParseFailureLog pins how every transport reports a message that
// failed to parse. The trigger is remote and pre-auth, so the record may not be
// at Error and may not carry the payload. All three transports must agree.
func TestTransportParseFailureLog(t *testing.T) {
	const src = "10.0.0.1:5060"

	handler := func(msg Message) {
		t.Errorf("handler ran for a message that failed to parse")
	}

	tests := []struct {
		name string
		run  func(t *testing.T, log *slog.Logger)
	}{
		{"UDP", func(t *testing.T, log *slog.Logger) {
			tr := &TransportUDP{log: log}
			tr.init(NewParser())
			tr.parseAndHandle(malformedRegister, src, handler)
		}},
		{"TCP", func(t *testing.T, log *slog.Logger) {
			tr := &TransportTCP{log: log}
			par := NewParser()
			tr.init(par)
			tr.parseStream(par.NewSIPStream(), malformedRegister, src, handler)
		}},
		{"WS", func(t *testing.T, log *slog.Logger) {
			tr := &TransportWS{log: log}
			par := NewParser()
			tr.init(par)
			tr.parseStream(par.NewSIPStream(), malformedRegister, src, handler)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lc := &logCapture{}
			tc.run(t, slog.New(lc))

			rec := lc.find(t, "failed to parse")
			text := flatten(rec)

			// A remote peer picks when this fires. Above Debug it can forge the
			// error rate of the process from off the network.
			assert.Equal(t, slog.LevelDebug, rec.Level, "level, record: %s", text)

			// The bytes are attacker controlled and reach here pre-auth.
			assert.NotContains(t, text, digestResponse, "credential in log: %s", text)
			assert.NotContains(t, text, "REGISTER sip:example.com", "payload in log: %s", text)

			// What is left has to be enough to chase a real malformed peer.
			assert.Contains(t, text, "src="+src, "record: %s", text)
			assert.Contains(t, text, fmt.Sprintf("bytes=%d", len(malformedRegister)), "record: %s", text)
		})
	}
}
