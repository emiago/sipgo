package sip

import (
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"

	"github.com/emiago/sipgo/fakes"
)

type warnCounter struct{ n atomic.Int32 }

func (w *warnCounter) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }
func (w *warnCounter) Handle(context.Context, slog.Record) error    { w.n.Add(1); return nil }
func (w *warnCounter) WithAttrs([]slog.Attr) slog.Handler           { return w }
func (w *warnCounter) WithGroup(string) slog.Handler                { return w }

func TestConnectionTryCloseAfterHardCloseDoesNotWarn(t *testing.T) {
	prev := DefaultLogger()
	t.Cleanup(func() { SetDefaultLogger(prev) })

	laddr := net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5060}
	raddr := net.TCPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 5060}
	conns := map[string]func() Connection{
		"tcp": func() Connection { return &TCPConnection{Conn: &fakes.TCPConn{LAddr: laddr, RAddr: raddr}} },
		"ws":  func() Connection { return &WSConnection{Conn: &fakes.TCPConn{LAddr: laddr, RAddr: raddr}} },
		"udp": func() Connection {
			return &UDPConnection{PacketConn: &fakes.UDPConn{LAddr: net.UDPAddr(laddr), RAddr: net.UDPAddr(raddr)}}
		},
	}
	for name, newConn := range conns {
		t.Run(name, func(t *testing.T) {
			w := &warnCounter{}
			SetDefaultLogger(slog.New(w))

			// Close takes the connection from two holders, who then release.
			c := newConn()
			c.Ref(2)
			c.Close()
			c.TryClose()
			c.TryClose()
			if n := w.n.Load(); n != 0 {
				t.Errorf("releases after Close logged %d warnings, want 0", n)
			}

			// Without Close, a release too many still warns.
			c = newConn()
			c.Ref(1)
			c.TryClose()
			c.TryClose()
			if n := w.n.Load(); n != 1 {
				t.Errorf("a release too many logged %d warnings, want 1", n)
			}
		})
	}
}
