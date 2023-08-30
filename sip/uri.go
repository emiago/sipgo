package sip

import (
	"io"
	"strconv"
	"strings"
)

type Uri struct {
	// True if and only if the URI is a SIPS URI.
	Encrypted bool
	Wildcard  bool

	// The user part of the URI: the 'joe' in sip:joe@bloggs.com
	// This is a pointer, so that URIs without a user part can have 'nil'.
	User string

	// The password field of the URI. This is represented in the URI as joe:hunter2@bloggs.com.
	// Note that if a URI has a password field, it *must* have a user field as well.
	// This is a pointer, so that URIs without a password field can have 'nil'.
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
	// Although the values of the map are sip.strings, they will never be NoString in practice as the parser
	// guarantees to not return blank values for header elements in SIP URIs.
	// You should not set the values of headers to NoString.
	Headers HeaderParams
}

// Generates the string representation of a SipUri struct.
func (uri *Uri) String() string {
	var buffer strings.Builder
	uri.StringWrite(&buffer)

	return buffer.String()
}

// StringWrite writes uri string to buffer
func (uri *Uri) StringWrite(buffer io.StringWriter) {
	// Compulsory protocol identifier.
	if uri.IsEncrypted() {
		buffer.WriteString("sips")
		buffer.WriteString(":")
	} else {
		buffer.WriteString("sip")
		buffer.WriteString(":")
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
	buffer.WriteString(uri.Host)

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

// Clone
func (uri *Uri) Clone() *Uri {
	c := *uri
	return &c
}

// IsEncrypted returns true if uri is SIPS uri
func (uri *Uri) IsEncrypted() bool {
	return uri.Encrypted
}

// Addr is uri address form. sip:user@host:port
func (uri *Uri) Addr() string {
	addr := uri.User + "@" + uri.HostPort()

	if uri.Encrypted {
		return "sips:" + addr
	}
	return "sip:" + addr
}

// HostPort represents host:port part
func (uri *Uri) HostPort() string {
	p := strconv.Itoa(uri.Port)
	return uri.Host + ":" + p
}
