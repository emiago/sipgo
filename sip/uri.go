package sip

import (
	"io"
	"strconv"
	"strings"
)

// Uri represents a URI used in a SIP message. SIP and SIPS URIs are split
// into their structured fields. The scheme-specific part of every other URI
// is retained without interpretation and is available through [Uri.Raw].
type Uri struct {
	Scheme string

	// raw is the uninterpreted scheme-specific part of a non-SIP URI. Use
	// Raw to read it. It is kept private so callers cannot put a Uri into a
	// state containing both a raw value and parsed SIP fields.
	raw string

	// If value is star (*)
	Wildcard bool

	// if // is present
	HierarhicalSlashes bool

	// The user part of the URI: the 'joe' in sip:joe@bloggs.com
	User string

	// The password field of the URI. This is represented in the URI as joe:hunter2@bloggs.com.
	// Note that if a URI has a password field, it *must* have a user field as well.
	// Note that RFC 3261 strongly recommends against the use of password fields in SIP URIs,
	// as they are fundamentally insecure.
	Password string

	// The host part of the URI. This can be a domain, or a string representation of an IP address.
	Host string

	// The port part of the URI. This is optional, and can be empty.
	Port int

	// Any parameters associated with the URI.
	// These are used to provide information about requests that may be constructed from the URI.
	// (For more details, see RFC 3261 section 19.1.1).
	// These appear as a semicolon-separated list of key=value pairs following the host[:port] part.
	UriParams HeaderParams

	// Any headers to be included on requests constructed from this URI.
	// These appear as a '&'-separated list at the end of the URI, introduced by '?'.
	Headers HeaderParams
}

// String returns the string representation of the URI.
func (uri *Uri) String() string {
	var buffer strings.Builder
	uri.StringWrite(&buffer)

	return buffer.String()
}

// StringWrite writes uri string to buffer
func (uri *Uri) StringWrite(buffer io.StringWriter) {
	// Normally we expect sip or sips, but it can be tel, urn
	scheme := uri.Scheme
	// For backward compatibility. No scheme defaults to sip
	if uri.Scheme == "" {
		scheme = "sip"
	}

	buffer.WriteString(scheme)
	buffer.WriteString(":")
	if uri.raw != "" {
		buffer.WriteString(uri.raw)
		return
	}

	if uri.HierarhicalSlashes {
		buffer.WriteString("//")
	}

	// Optional userinfo part.
	if uri.User != "" {
		buffer.WriteString(uri.User)
		if uri.Password != "" {
			buffer.WriteString(":")
			buffer.WriteString(uri.Password)
		}
		buffer.WriteString("@")
	}

	// Compulsory hostname.
	buffer.WriteString(uriIP(uri.Host))

	// Optional port number.
	if uri.Port > 0 {
		buffer.WriteString(":")
		buffer.WriteString(strconv.Itoa(uri.Port))
	}

	if (uri.UriParams != nil) && uri.UriParams.Length() > 0 {
		buffer.WriteString(";")
		buffer.WriteString(uri.UriParams.ToString(';'))
	}

	if (uri.Headers != nil) && uri.Headers.Length() > 0 {
		buffer.WriteString("?")
		buffer.WriteString(uri.Headers.ToString('&'))
	}
}

// Raw returns the uninterpreted scheme-specific part of a non-SIP URI. It
// excludes the scheme and its separating colon. The value is empty for SIP
// and SIPS URIs.
func (uri *Uri) Raw() string {
	return uri.raw
}

// Clone
func (uri *Uri) Clone() *Uri {
	c := *uri
	if uri.UriParams != nil {
		c.UriParams = uri.UriParams.clone()
	}
	if uri.Headers != nil {
		c.Headers = uri.Headers.clone()
	}
	return &c
}

// IsEncrypted returns true if uri is SIPS uri
func (uri *Uri) IsEncrypted() bool {
	return uri.Scheme == "sips"
}

// Endpoint is uri user identifier. user@host[:port]
func (uri *Uri) Endpoint() string {
	addr := uri.User + "@" + uri.Host
	if uri.Port > 0 {
		addr += ":" + strconv.Itoa(uri.Port)
	}
	return addr
}

// Addr is uri part without headers and params. sip[s]:user@host[:port]
func (uri *Uri) Addr() string {
	scheme := uri.Scheme
	// For backward compatibility. No scheme defaults to sip
	if uri.Scheme == "" {
		scheme = "sip"
	}

	addr := uri.Host
	if uri.User != "" {
		addr = uri.User + "@" + addr
	}
	if uri.Port > 0 {
		addr += ":" + strconv.Itoa(uri.Port)
	}

	if uri.IsEncrypted() {
		return "sips:" + addr
	}
	return scheme + ":" + addr
}

// HostPort represents host:port part
func (uri *Uri) HostPort() string {
	p := strconv.Itoa(uri.Port)
	return uri.Host + ":" + p
}
