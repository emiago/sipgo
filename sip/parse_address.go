package sip

import (
	"errors"
	"fmt"
	"strings"
)

type nameAddress struct {
	displayName  string
	uri          *Uri
	headerParams HeaderParams
}

type addressFSM func(dispName *nameAddress, s string) (addressFSM, string, error)

// ParseAddressValue parses an address - such as from a From, To, or
// Contact header. It returns:
// See RFC 3261 section 20.10 for details on parsing an address.
func ParseAddressValue(addressText string, uri *Uri, headerParams HeaderParams) (displayName string, err error) {
	if len(addressText) == 0 {
		return "", errors.New("Empty Address")
	}

	// adds alloc but easier to maintain
	a := nameAddress{
		uri:          uri,
		headerParams: headerParams,
	}

	err = parseNameAddress(addressText, &a)
	displayName = a.displayName
	return
}

// parseNameAddress
// name-addr      =  [ display-name ] LAQUOT addr-spec RAQUOT
// addr-spec      =  SIP-URI / SIPS-URI / absoluteURI
// TODO Consider exporting this
func parseNameAddress(addressText string, a *nameAddress) (err error) {
	state := addressStateDisplayName
	str := addressText
	for state != nil {
		state, str, err = state(a, str)
		if err != nil {
			return
		}
	}
	return nil
}

func addressStateDisplayName(a *nameAddress, s string) (addressFSM, string, error) {
	var startQuote, endQuote int = -1, -1
	for i, c := range s {
		if c == '"' {
			if startQuote < 0 {
				startQuote = i
			} else {
				endQuote = i
			}
			continue
		}

		// https://datatracker.ietf.org/doc/html/rfc3261#section-20.10
		// When the header field value contains a display name, the URI
		// including all URI parameters is enclosed in "<" and ">".  If no "<"
		// and ">" are present, all parameters after the URI are header
		// parameters, not URI parameters.
		if c == '<' {
			if endQuote > 0 {
				a.displayName = s[startQuote+1 : endQuote]
			} else {
				a.displayName = strings.TrimSpace(s[:i])
			}
			return addressStateUriBracket, s[i+1:], nil
		}

		if c == ';' {
			if startQuote > 0 {
				continue
			}
			// detect early
			// uri can be without <> in that case there all after ; are header params
			return addressStateUri, s, nil
		}
	}

	// No DisplayName found
	return addressStateUri, s, nil
}

func addressStateUriBracket(a *nameAddress, s string) (addressFSM, string, error) {
	if len(s) == 0 {
		return nil, s, errors.New("No URI present")
	}

	for i, c := range s {
		if c == '>' {
			err := ParseUri(s[:i], a.uri)
			return addressStateHeaderParams, s[i+1:], err
		}
	}
	return nil, s, fmt.Errorf("invalid uri, missing end bracket")
}

func addressStateUri(a *nameAddress, s string) (addressFSM, string, error) {
	if len(s) == 0 {
		return nil, s, errors.New("No URI present")
	}

	for i, c := range s {
		if c == ';' {
			err := ParseUri(s[:i], a.uri)
			return addressStateHeaderParams, s[i+1:], err
		}
	}

	// No header params detected
	err := ParseUri(s, a.uri)
	return nil, s, err
}

func addressStateHeaderParams(a *nameAddress, s string) (addressFSM, string, error) {

	addParam := func(equal int, s string) {

		if equal > 0 {
			name := s[:equal]
			val := s[equal+1:]
			a.headerParams.Add(name, val)
			return
		}

		if len(s) == 0 {
			// could be just ;
			return
		}

		// Case when we have key name but not value. ex ;+siptag;
		name := s[:]
		a.headerParams.Add(name, "")
	}

	equal := -1
	for i, c := range s {
		if c == '=' {
			equal = i
			continue
		}

		if c == ';' {
			addParam(equal, s[:i])
			return addressStateHeaderParams, s[i+1:], nil
		}
	}

	addParam(equal, s)
	return nil, s, nil
}

// headerParserTo generates ToHeader
func headerParserTo(headerName string, headerText string) (header Header, err error) {
	h := &ToHeader{}
	return h, parseToHeader(headerText, h)
}

func parseToHeader(headerText string, h *ToHeader) error {
	var err error

	h.Params = NewParams()
	h.DisplayName, err = ParseAddressValue(headerText, &h.Address, h.Params)
	if err != nil {
		return err
	}

	if h.Address.Wildcard {
		// The Wildcard '*' URI is only permitted in Contact headers.
		err = fmt.Errorf(
			"wildcard uri not permitted in to: header: %s",
			headerText,
		)
		return err
	}
	return nil
}

// headerParserFrom generates FromHeader
func headerParserFrom(headerName string, headerText string) (header Header, err error) {
	h := &FromHeader{}
	return h, parseFromHeader(headerText, h)
}

func parseFromHeader(headerText string, h *FromHeader) error {
	var err error

	h.Params = NewParams()
	h.DisplayName, err = ParseAddressValue(headerText, &h.Address, h.Params)
	// h.DisplayName, h.Address, h.Params, err = ParseAddressValue(headerText)
	if err != nil {
		return err
	}

	if h.Address.Wildcard {
		// The Wildcard '*' URI is only permitted in Contact headers.
		err = fmt.Errorf(
			"wildcard uri not permitted in to: header: %s",
			headerText,
		)
		return err
	}
	return nil
}

func headerParserContact(headerName string, headerText string) (header Header, err error) {
	h := ContactHeader{}
	return &h, parseContactHeader(headerText, &h)
}

// parseContactHeader generates ContactHeader
func parseContactHeader(headerText string, h *ContactHeader) error {
	inBrackets := false
	inQuotes := false

	endInd := len(headerText)
	end := endInd - 1

	var err error
	for idx, char := range headerText {
		if char == '<' && !inQuotes {
			inBrackets = true
		} else if char == '>' && !inQuotes {
			inBrackets = false
		} else if char == '"' {
			inQuotes = !inQuotes
		} else if !inQuotes && !inBrackets {
			switch {
			case char == ',':
				err = errComaDetected(idx)
			case idx == end:
				endInd = idx + 1
			default:
				continue
			}

			break
		}
	}

	var e error
	h.Params = NewParams()
	h.DisplayName, e = ParseAddressValue(headerText[:endInd], &h.Address, h.Params)
	if e != nil {
		return e
	}

	return err
}

func headerParserRoute(headerName string, headerText string) (header Header, err error) {
	// Append a comma to simplify the parsing code; we split address sections
	// on commas, so use a comma to signify the end of the final address section.
	h := RouteHeader{}
	return &h, parseRouteHeader(headerText, &h)
}

// parseRouteHeader parser RouteHeader
func parseRouteHeader(headerText string, h *RouteHeader) error {
	return parseRouteAddress(headerText, &h.Address)
}

// parseRouteHeader generates RecordRouteHeader
func headerParserRecordRoute(headerName string, headerText string) (header Header, err error) {
	// Append a comma to simplify the parsing code; we split address sections
	// on commas, so use a comma to signify the end of the final address section.
	h := RecordRouteHeader{}
	return &h, parseRecordRouteHeader(headerText, &h)
}

func parseRecordRouteHeader(headerText string, h *RecordRouteHeader) error {
	return parseRouteAddress(headerText, &h.Address)
}

func headerParserReferTo(headerName string, headerText string) (header Header, err error) {
	h := ReferToHeader{}
	return &h, parseReferToHeader(headerText, &h)
}

func parseReferToHeader(headerText string, h *ReferToHeader) error {
	return parseRouteAddress(headerText, &h.Uri)
}

func parseRouteAddress(headerText string, address *Uri) (err error) {
	inBrackets := false
	inQuotes := false
	end := len(headerText) - 1
	for idx, char := range headerText {
		if char == '<' && !inQuotes {
			inBrackets = true
			continue
		}
		if char == '>' && !inQuotes {
			inBrackets = false
		} else if char == '"' {
			inQuotes = !inQuotes
		}

		if !inQuotes && !inBrackets {
			switch {
			case char == ',':
				err = errComaDetected(idx)
			case idx == end:
				idx = idx + 1
			default:
				continue
			}

			_, e := ParseAddressValue(headerText[:idx], address, nil)
			if e != nil {
				return e
			}
			break
		}
	}
	return
}
