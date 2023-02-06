package parser

import (
	"strconv"
	"strings"

	"github.com/emiago/sipgo/sip"
)

// Note that although Via headers may contain a comma-separated list, RFC 3261 makes it clear that
// these should not be treated as separate logical Via headers, but as multiple values on a single
// Via header.
func parseViaHeader(headerName string, headerText string) (
	header sip.Header, err error) {
	// sections := strings.Split(headerText, ",")
	h := sip.ViaHeader{
		Params: sip.HeaderParams{},
	}
	state := viaStateProtocol
	str := headerText
	hop := &h
	for state != nil {
		state, str, err = state(hop, str)
		if err != nil {
			return
		}
		// If we alocated next hop this means we hit coma
		if hop.Next != nil {
			hop = h.Next
		}
	}
	return &h, nil
}

type viaFSM func(h *sip.ViaHeader, s string) (viaFSM, string, error)

func viaStateProtocol(h *sip.ViaHeader, s string) (viaFSM, string, error) {
	ind := strings.IndexRune(s, '/')
	h.ProtocolName = s[:ind]
	return viaStateProtocolVersion, s[ind+1:], nil
}

func viaStateProtocolVersion(h *sip.ViaHeader, s string) (viaFSM, string, error) {
	ind := strings.IndexRune(s, '/')
	h.ProtocolVersion = s[:ind]
	return viaStateProtocolTransport, s[ind+1:], nil
}

func viaStateProtocolTransport(h *sip.ViaHeader, s string) (viaFSM, string, error) {
	ind := strings.IndexAny(s, " \t")
	h.Transport = s[:ind]
	return viaStateHost, s[ind+1:], nil
}

func viaStateHost(h *sip.ViaHeader, s string) (viaFSM, string, error) {
	var colonInd int
	var endIndex int = len(s)
	var err error
loop:
	for i, c := range s {
		switch c {
		case ';':
			endIndex = i
			break loop
		case ':':
			colonInd = i
			// Uri has port
		}
	}

	if colonInd > 0 {
		h.Port, err = strconv.Atoi(s[colonInd+1 : endIndex])
		if err != nil {
			return nil, "", nil
		}
		h.Host = s[:colonInd]
	} else {
		h.Host = s[:endIndex]
	}

	if endIndex == len(s) {
		return nil, "", nil
	}

	// return nil, "", nil
	return viaStateParams, s[endIndex+1:], nil
}

func viaStateParams(h *sip.ViaHeader, s string) (viaFSM, string, error) {
	var err error
	coma := strings.IndexRune(s, ',')
	if coma > 0 {
		// h.Params, _, err = ParseParams(s[:coma], ';', ';', 0, true, true)
		// h.Params, _, err = ParseParams(s[:coma], ';', ';')
		_, err = UnmarshalParams(s[:coma], ';', ',', h.Params)
		h.Next = &sip.ViaHeader{
			Params: sip.HeaderParams{},
		}
		return viaStateProtocol, s[coma+1:], err
	}

	// h.Params, _, err = ParseParams(s, ';', ';', 0, true, true)
	// h.Params, _, err = ParseParams(s, ';', ';')
	_, err = UnmarshalParams(s, ';', '\r', h.Params)
	return nil, "", err
}
