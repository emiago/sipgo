package transport

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	sipgo "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	"github.com/livekit/sipgo/sip"
)

// Concurrent writers on one WS connection must not interleave frames, from
// either end.
func TestWSConcurrentWrites(t *testing.T) {
	const writers, perWriter = 8, 50
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	received := map[string]chan sip.Message{
		"accepted": make(chan sip.Message, writers*perWriter),
		"dialed":   make(chan sip.Message, writers*perWriter),
	}
	acpt := NewWSTransport(log, sipgo.NewParser())
	dial := NewWSTransport(log, sipgo.NewParser())
	defer acpt.Close()
	defer dial.Close()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	go acpt.Serve(l, func(m sip.Message) { received["accepted"] <- m })

	raddr := l.Addr().(*net.TCPAddr)
	dialed, err := dial.CreateConnection(Addr{}, "", Addr{IP: raddr.IP, Port: raddr.Port}, func(m sip.Message) { received["dialed"] <- m })
	require.NoError(t, err)
	defer dialed.TryClose()

	var accepted Connection
	require.Eventually(t, func() bool {
		accepted, _ = acpt.GetConnection(dialed.LocalAddr().String())
		return accepted != nil
	}, 2*time.Second, 5*time.Millisecond)
	defer accepted.TryClose()

	for _, dir := range []struct {
		from Connection
		to   string
	}{{dialed, "accepted"}, {accepted, "dialed"}} {
		var wg sync.WaitGroup
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range perWriter {
					require.NoError(t, dir.from.WriteMsg(wsTestRequest(fmt.Sprintf("%d-%d", w, i))))
				}
			}()
		}
		wg.Wait()

		seen := map[string]bool{}
		for range writers * perWriter {
			select {
			case m := <-received[dir.to]:
				seen[m.CallID().Value()] = true
			case <-time.After(2 * time.Second):
				t.Fatalf("%s side got %d of %d messages", dir.to, len(seen), writers*perWriter)
			}
		}
		require.Len(t, seen, writers*perWriter)
	}
}

func wsTestRequest(callID string) sip.Message {
	msg, err := sipgo.NewParser().ParseSIP([]byte("OPTIONS sip:127.0.0.1 SIP/2.0\r\n" +
		"Via: SIP/2.0/WS 127.0.0.1;branch=z9hG4bK" + callID + "\r\n" +
		"From: <sip:a@127.0.0.1>;tag=1\r\n" +
		"To: <sip:b@127.0.0.1>\r\n" +
		"Call-ID: " + callID + "\r\n" +
		"CSeq: 1 OPTIONS\r\n" +
		"Content-Length: 0\r\n\r\n"))
	if err != nil {
		panic(err)
	}
	return msg
}
