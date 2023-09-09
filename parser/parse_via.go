package parser

import (
	"errors"
	"strconv"
	"strings"

	"github.com/emiago/sipgo/sip"
)

// Via header is important header

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
	var ind, nextInd int

	for state != nil {
		state, nextInd, err = state(&h, str[ind:])
		if err != nil {

			// Fix the offset
			if _, ok := err.(errComaDetected); ok {
				err = errComaDetected(ind + nextInd)
			}
			return &h, err
		}
		// If we alocated next hop this means we hit coma
		// if hop.Next != nil {
		// 	hop = h.Next
		// }
		ind += nextInd
	}
	return &h, nil
}

type viaFSM func(h *sip.ViaHeader, s string) (viaFSM, int, error)

func viaStateProtocol(h *sip.ViaHeader, s string) (viaFSM, int, error) {
	ind := strings.IndexRune(s, '/')
	if ind < 0 {
		return nil, 0, errors.New("Malformed protocol name in Via header")
	}
	h.ProtocolName = s[:ind]
	return viaStateProtocolVersion, ind + 1, nil
}

func viaStateProtocolVersion(h *sip.ViaHeader, s string) (viaFSM, int, error) {
	ind := strings.IndexRune(s, '/')
	if ind < 0 {
		return nil, 0, errors.New("Malformed protocol version in Via header")
	}
	h.ProtocolVersion = s[:ind]
	return viaStateProtocolTransport, ind + 1, nil
}

func viaStateProtocolTransport(h *sip.ViaHeader, s string) (viaFSM, int, error) {
	ind := strings.IndexAny(s, " \t")
	if ind < 0 {
		return nil, 0, errors.New("Malformed transport in Via header")
	}
	h.Transport = s[:ind]
	return viaStateHost, ind + 1, nil
}

func viaStateHost(h *sip.ViaHeader, s string) (viaFSM, int, error) {
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
			return nil, 0, nil
		}
		h.Host = s[:colonInd]
	} else {
		h.Host = s[:endIndex]
	}

	if endIndex == len(s) {
		return nil, 0, nil
	}

	// return nil, "", nil
	return viaStateParams, endIndex + 1, nil
}

func viaStateParams(h *sip.ViaHeader, s string) (viaFSM, int, error) {
	var err error
	coma := strings.IndexRune(s, ',')
	if coma > 0 {
		// h.Params, _, err = ParseParams(s[:coma], ';', ';', 0, true, true)
		// h.Params, _, err = ParseParams(s[:coma], ';', ';')
		_, err = UnmarshalParams(s[:coma], ';', ',', h.Params)
		if err != nil {
			return nil, 0, err
		}
		// h.Next = &sip.ViaHeader{
		// 	Params: sip.HeaderParams{},
		// }
		return viaStateProtocol, coma, errComaDetected(coma)
	}

	// h.Params, _, err = ParseParams(s, ';', ';', 0, true, true)
	// h.Params, _, err = ParseParams(s, ';', ';')
	_, err = UnmarshalParams(s, ';', '\r', h.Params)
	return nil, 0, err
}
