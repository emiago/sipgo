package fakes

import (
	"net"
	"testing"
)

type TestConnection interface {
	TestReadConn(t testing.TB) []byte
	TestWriteConn(t testing.TB, data []byte)
	TestRequest(t testing.TB, data []byte) []byte
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
}
