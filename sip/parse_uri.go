package sip

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type uriFSM func(uri *Uri, s string) (uriFSM, string, error)

// ParseUri converts a string representation of a URI into a Uri object.
// Following https://datatracker.ietf.org/doc/html/rfc3261#section-19.1.1
// sip:user:password@host:port;uri-parameters?headers
func ParseUri(uriStr string, uri *Uri) (err error) {
	if len(uriStr) == 0 {
		return errors.New("empty URI")
	}
	state := uriStateScheme
	str := uriStr
	for state != nil {
		state, str, err = state(uri, str)
		if err != nil {
			return
		}
	}
	return
}

func uriStateScheme(uri *Uri, s string) (uriFSM, string, error) {
	// Do fast checks. Minimum uri
	if len(s) < 3 {
		if s == "*" {
			// Normally this goes under url path, but we set on host
			uri.Host = "*"
			uri.Wildcard = true
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("not valid sip uri")
	}

	for i, c := range s {
		if c == ':' {
			uri.Scheme = ASCIIToLower(s[:i])
			return uriStateSlashes, s[i+1:], nil
		}
		// Check is c still ASCII
		if !isASCII(c) {
			return nil, "", fmt.Errorf("invalid uri scheme")
		}
	}

	return nil, "", fmt.Errorf("missing protocol scheme")
}

func uriStateSlashes(uri *Uri, s string) (uriFSM, string, error) {
	// Check does uri contain slashes
	// They are valid in uri but normally we cut them
	s, uri.HierarhicalSlashes = strings.CutPrefix(s, "//")
	return uriStateUser, s, nil
}

func uriStateUser(uri *Uri, s string) (uriFSM, string, error) {
	var userend int = 0
	for i, c := range s {
		if c == '[' {
			// IPV6
			return uriStateHost, s[i:], nil
		}

		if c == ':' {
			userend = i
		}

		if c == '@' {
			if userend > 0 {
				uri.User = s[:userend]
				uri.Password = s[userend+1 : i]
			} else {
				uri.User = s[:i]
			}
			return uriStateHost, s[i+1:], nil
		}
	}

	return uriStateHost, s, nil
}

func uriStateHost(uri *Uri, s string) (uriFSM, string, error) {
	for i, c := range s {
		if c == '[' {
			return uriStateHostIPV6, s[i:], nil
		}

		if c == ':' {
			uri.Host = s[:i]
			return uriStatePort, s[i+1:], nil
		}

		if c == ';' {
			uri.Host = s[:i]
			return uriStateUriParams, s[i+1:], nil
		}

		if c == '?' {
			uri.Host = s[:i]
			return uriStateHeaders, s[i+1:], nil
		}
	}
	// If no special chars found, it means we are at end
	uri.Host = s
	// Check is this wildcard
	uri.Wildcard = s == "*"
	return uriStateUriParams, "", nil
}

func uriStateHostIPV6(uri *Uri, s string) (uriFSM, string, error) {
	// ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff max 39 + 2 brackets
	// Do not waste time looking end
	maxs := min(len(s), 42)

	ind := strings.Index(s[:maxs], "]")
	if ind <= 0 {
		return nil, s, fmt.Errorf("IPV6 no closing bracket")
	}
	uri.Host = s[:ind+1]

	if ind+1 == len(s) {
		// finished
		return uriStateUriParams, "", nil
	}

	s = s[ind+1:]

	// Check now termination
	c := s[0]
	if c == ':' {
		return uriStatePort, s[1:], nil
	}

	if c == ';' {
		return uriStateUriParams, s[1:], nil
	}

	if c == '?' {
		return uriStateHeaders, s[1:], nil
	}

	return uriStateUriParams, "", nil
}

func uriStatePort(uri *Uri, s string) (uriFSM, string, error) {
	var err error
	for i, c := range s {
		if c == ';' {
			uri.Port, err = strconv.Atoi(s[:i])
			return uriStateUriParams, s[i+1:], err
		}

		if c == '?' {
			uri.Port, err = strconv.Atoi(s[:i])
			return uriStateHeaders, s[i+1:], err
		}
	}

	uri.Port, err = strconv.Atoi(s)
	return uriStateUriParams, "", err
}

func uriStateUriParams(uri *Uri, s string) (uriFSM, string, error) {
	var n int
	var err error
	if len(s) == 0 {
		uri.UriParams = NewParams()
		uri.Headers = NewParams()
		return nil, s, nil
	}
	uri.UriParams = NewParams()
	// uri.UriParams, n, err = ParseParams(s, 0, ';', '?', true, true)
	n, err = UnmarshalHeaderParams(s, ';', '?', uri.UriParams)
	if err != nil {
		return nil, s, err
	}

	if n == len(s) {
		n = n - 1
	}

	if s[n] != '?' {
		return nil, s, nil
	}

	return uriStateHeaders, s[n+1:], nil
}

func uriStateHeaders(uri *Uri, s string) (uriFSM, string, error) {
	var err error
	// uri.Headers, _, err = ParseParams(s, 0, '&', 0, true, false)
	uri.Headers = NewParams()
	_, err = UnmarshalHeaderParams(s, '&', 0, uri.Headers)
	return nil, s, err
}
