package sip

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type uriFSM func(uri *Uri, s string) (uriFSM, string, error)

// ParseUri converts a string representation of a URI into a Uri object.
// Following https://datatracker.ietf.org/doc/html/rfc3261#section-19.1.1
// sip:user:password@host:port;uri-parameters?headers
func ParseUri(uriStr string, uri *Uri) (err error) {
	if len(uriStr) == 0 {
		return errors.New("Empty URI")
	}

	state := uriStateStart
	str := uriStr
	for state != nil {
		state, str, err = state(uri, str)
		if err != nil {
			return
		}
	}
	return
}

func uriStateStart(uri *Uri, s string) (uriFSM, string, error) {
	if s == "*" {
		// Normally this goes under url path, but we set on host
		uri.Host = "*"
		uri.Wildcard = true
		return nil, "", nil
	}

	return uriStateScheme(uri, s)
}

func uriStateScheme(uri *Uri, s string) (uriFSM, string, error) {
	colInd := strings.Index(s, ":")
	if colInd == -1 {
		return nil, "", fmt.Errorf("missing protocol scheme")
	}

	uri.Scheme = strings.ToLower(s[:colInd])
	s = s[colInd+1:]

	if err := validateScheme(uri.Scheme); err != nil {
		return nil, "", err
	}

	if uri.Scheme == "tel" {
		return uriTelNumber, s, nil
	}

	// If hierarchical slashes are present after scheme, strip them out and take note
	// so that they can be inserted back when serializing URI
	if len(s) >= 2 && s[:2] == "//" {
		uri.HierarhicalSlashes = true
		s = s[2:]
	}

	if uri.Scheme == "sips" || uri.Scheme == "https" {
		uri.Encrypted = true
	}

	return uriStateUser, s, nil
}

func uriStateUser(uri *Uri, s string) (uriFSM, string, error) {
	var userend int = 0
	for i, c := range s {
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

func uriStatePassword(uri *Uri, s string) (uriFSM, string, error) {
	for i, c := range s {
		if c == '@' {
			uri.Password = s[:i]
			return uriStateHost, s[i+1:], nil
		}
	}

	return nil, "", fmt.Errorf("missing @")
}

func uriStateHost(uri *Uri, s string) (uriFSM, string, error) {
	for i, c := range s {
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
	return nil, s, err
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
	n, err = UnmarshalParams(s, ';', '?', uri.UriParams)
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
	_, err = UnmarshalParams(s, '&', 0, uri.Headers)
	return nil, s, err
}

// validateScheme performs basic scheme validation to prevent cases where port delimited 
// (or other naturally occuring colon) is mistakenly used as a scheme delimiter. 
// This is NOT a fool-proof validation and URIs may still be incorrectly parsed 
// unless more parsing validation effort is made.
//
// scheme        = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
func validateScheme(scheme string) error {
	if len(scheme) == 0 {
		return errors.New("no scheme found")
	}
	for _, c := range scheme {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '+' && c != '-' && c != '.' {
			return fmt.Errorf("invalid scheme: %q is not allowed", c)
		}
	}
	return nil
}

func uriTelNumber(uri *Uri, s string) (uriFSM, string, error) {
	for i, c := range s {
		if c == ';' {
			uri.User = s[:i]
			return uriStateUriParams, s[i+1:], nil
		}
	}

	uri.User = s
	return nil, "", nil
}
