package sip

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRequest(t *testing.T) {
	newReq := func() *Request {
		return NewRequest(REGISTER, Uri{User: "alice", Host: "example.com"})
	}

	t.Run("clean request passes", func(t *testing.T) {
		req := testCreateRequest(t, "OPTIONS", "sip:example.com", "UDP", "127.0.0.1:5060")
		require.NoError(t, ValidateRequest(req))
	})

	t.Run("header value", func(t *testing.T) {
		req := newReq()
		req.AppendHeader(NewHeader("Subject", "injected\r\nContent-Length: 0"))
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("header value bare LF", func(t *testing.T) {
		req := newReq()
		req.AppendHeader(NewHeader("Subject", "injected\nContent-Length: 0"))
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("header value bare CR", func(t *testing.T) {
		req := newReq()
		req.AppendHeader(NewHeader("Subject", "injected\rContent-Length: 0"))
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("header name", func(t *testing.T) {
		req := newReq()
		req.AppendHeader(NewHeader("Subject\r\nInjected", "value"))
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("typed header value", func(t *testing.T) {
		req := newReq()
		req.AppendHeader(&FromHeader{
			DisplayName: "Alice\r\nInjected: yes",
			Address:     Uri{User: "alice", Host: "example.com"},
		})
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("request uri", func(t *testing.T) {
		req := NewRequest(REGISTER, Uri{User: "alice\r\nInjected: yes", Host: "example.com"})
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("method", func(t *testing.T) {
		req := newReq()
		req.Method = RequestMethod("REGISTER\r\nInjected: yes")
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("sip version", func(t *testing.T) {
		req := newReq()
		req.SipVersion = "SIP/2.0\r\nInjected: yes"
		require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	})

	t.Run("body with CRLF passes", func(t *testing.T) {
		// A body is not line structured. SDP is CRLF separated and must not be
		// mistaken for an injection.
		req := newReq()
		req.SetBody([]byte("v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\n"))
		require.NoError(t, ValidateRequest(req))
	})
}

func TestValidateResponse(t *testing.T) {
	req := testCreateRequest(t, "OPTIONS", "sip:example.com", "UDP", "127.0.0.1:5060")

	t.Run("clean response passes", func(t *testing.T) {
		res := NewResponseFromRequest(req, StatusOK, "OK", nil)
		require.NoError(t, ValidateResponse(res))
	})

	t.Run("reason phrase", func(t *testing.T) {
		res := NewResponseFromRequest(req, StatusOK, "OK\r\nInjected: yes", nil)
		require.ErrorIs(t, ValidateResponse(res), ErrInvalidCRLF)
	})

	t.Run("header value", func(t *testing.T) {
		res := NewResponseFromRequest(req, StatusOK, "OK", nil)
		res.AppendHeader(NewHeader("Server", "evil\r\nContent-Length: 0"))
		require.ErrorIs(t, ValidateResponse(res), ErrInvalidCRLF)
	})

	t.Run("sip version", func(t *testing.T) {
		res := NewResponseFromRequest(req, StatusOK, "OK", nil)
		res.SipVersion = "SIP/2.0\r\nInjected: yes"
		require.ErrorIs(t, ValidateResponse(res), ErrInvalidCRLF)
	})

	t.Run("body with CRLF passes", func(t *testing.T) {
		res := NewResponseFromRequest(req, StatusOK, "OK", []byte("v=0\r\ns=-\r\n"))
		require.NoError(t, ValidateResponse(res))
	})
}

// TestValidateRejectsBeforeSerializing pins down why validation matters: without
// it the injected value is serialized verbatim and the receiver reads a header
// the sender never intended.
func TestValidateRejectsBeforeSerializing(t *testing.T) {
	req := testCreateRequest(t, "OPTIONS", "sip:example.com", "UDP", "127.0.0.1:5060")
	req.AppendHeader(NewHeader("Subject", "hi\r\nInjected: yes"))

	require.ErrorIs(t, ValidateRequest(req), ErrInvalidCRLF)
	require.True(t, strings.Contains(req.String(), "\r\nInjected: yes\r\n"),
		"injection is only stopped by validating, serializing does not escape it")
}
