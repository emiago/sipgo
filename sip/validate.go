package sip

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidCRLF is returned by ValidateRequest and ValidateResponse when a
// message field carries a CR or LF character. A serialized SIP message uses
// CRLF to terminate the start line and every header line, so such a character
// lets user input end its line early and inject arbitrary headers or a body.
var ErrInvalidCRLF = errors.New("invalid CRLF")

// hasCRLF reports whether s contains a line terminator. Bare CR and bare LF are
// both rejected: parsers in the wild disagree on which one ends a line, and a
// message that is unambiguous to us may still split for the next hop.
func hasCRLF(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}

// ValidateRequest reports whether req is safe to serialize, returning an error
// wrapping ErrInvalidCRLF if any field that becomes part of the start line or a
// header line contains CR or LF.
//
// Call it after building a request from user input and before passing the
// request to a transaction or transport. The body is not checked: it is not
// line structured, and payloads such as SDP legitimately contain CRLF.
func ValidateRequest(req *Request) error {
	if hasCRLF(string(req.Method)) {
		return fmt.Errorf("%w: request method", ErrInvalidCRLF)
	}

	if hasCRLF(req.SipVersion) {
		return fmt.Errorf("%w: sip version", ErrInvalidCRLF)
	}

	// Recipient is rendered into the start line whole, so any of its parts
	// (user, host, params, embedded headers) can carry the injection.
	if hasCRLF(req.Recipient.String()) {
		return fmt.Errorf("%w: request uri", ErrInvalidCRLF)
	}

	return validateHeaders(&req.headers)
}

// ValidateResponse reports whether res is safe to serialize, returning an error
// wrapping ErrInvalidCRLF if the reason phrase or any header contains CR or LF.
//
// Call it after building a response from user input and before passing the
// response to a transaction or transport. StatusCode needs no check, it is an
// int. The body is not checked, see ValidateRequest.
func ValidateResponse(res *Response) error {
	if hasCRLF(res.SipVersion) {
		return fmt.Errorf("%w: sip version", ErrInvalidCRLF)
	}

	if hasCRLF(res.Reason) {
		return fmt.Errorf("%w: reason phrase", ErrInvalidCRLF)
	}

	return validateHeaders(&res.headers)
}

// validateHeaders walks headerOrder, which is what StringWrite serializes, so
// every header of every type is covered by the Header interface alone. Name and
// Value are exactly the two strings written around the ": " separator.
func validateHeaders(hs *headers) error {
	for _, h := range hs.headerOrder {
		name := h.Name()
		if hasCRLF(name) {
			return fmt.Errorf("%w: header name %q", ErrInvalidCRLF, name)
		}

		if hasCRLF(h.Value()) {
			return fmt.Errorf("%w: header %q value", ErrInvalidCRLF, name)
		}
	}

	return nil
}
