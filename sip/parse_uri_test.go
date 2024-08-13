package sip

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUri(t *testing.T) {
	// This are all good accepted URIs test.

	/*
		https://datatracker.ietf.org/doc/html/rfc3261#section-19.1.3
		sip:alice@atlanta.com
		sip:alice:secretword@atlanta.com;transport=tcp
		sips:alice@atlanta.com?subject=project%20x&priority=urgent
		sip:+1-212-555-1212:1234@gateway.com;user=phone
		sips:1212@gateway.com
		sip:alice@192.0.2.4
		sip:atlanta.com;method=REGISTER?to=alice%40atlanta.com
		sip:alice;day=tuesday@atlanta.com
	*/

	var uri Uri
	var err error
	var str string

	t.Run("basic", func(t *testing.T) {
		uri = Uri{}
		str = "sip:alice@localhost:5060"
		err = ParseUri(str, &uri)
		require.NoError(t, err)
		assert.Equal(t, "sip", uri.Scheme)
		assert.Equal(t, "alice", uri.User)
		assert.Equal(t, "localhost", uri.Host)
		assert.Equal(t, 5060, uri.Port)
		assert.Equal(t, "localhost:5060", uri.HostPort())
		assert.Equal(t, "alice@localhost:5060", uri.Endpoint())
	})

	t.Run("sip case insensitive", func(t *testing.T) {
		testCases := []string{
			"sip:alice@atlanta.com",
			"SIP:alice@atlanta.com",
			"sIp:alice@atlanta.com",
		}
		for _, testCase := range testCases {
			err = ParseUri(testCase, &uri)
			require.NoError(t, err)
			assert.Equal(t, "alice", uri.User)
			assert.Equal(t, "atlanta.com", uri.Host)
			assert.False(t, uri.Encrypted)
		}

		testCases = []string{
			"sips:alice@atlanta.com",
			"SIPS:alice@atlanta.com",
			"sIpS:alice@atlanta.com",
		}
		for _, testCase := range testCases {
			err = ParseUri(testCase, &uri)
			require.NoError(t, err)
			assert.Equal(t, "alice", uri.User)
			assert.Equal(t, "atlanta.com", uri.Host)
			assert.True(t, uri.Encrypted)
		}

	})

	t.Run("hierarchical slashes are preserved", func(t *testing.T) {
		testCases := []string{
			"http://atlanta.com",
			"file://atlanta.com/foo/bar.txt",
			"sip://alice@localhost:5060",
		}
		for _, testCase := range testCases {
			uri = Uri{}
			err = ParseUri(testCase, &uri)
			require.NoError(t, err)
			assert.Equal(t, testCase, uri.String())
		}
	})

	t.Run("sip scheme", func(t *testing.T) {
		// No scheme is not allowed
		// URI relative references are not supported yet
		testCases := []string{
			"example.com;foo=bar",
			"alice@localhost:5060",
			"bad_scheme://example.com",
		}
		for _, testCase := range testCases {
			uri = Uri{}
			err = ParseUri(testCase, &uri)
			require.Error(t, err)
		}
	})

	t.Run("uri params parsed", func(t *testing.T) {
		uri = Uri{}
		str = "sips:alice@atlanta.com?subject=project%20x&priority=urgent"
		err = ParseUri(str, &uri)
		require.NoError(t, err)

		assert.Equal(t, "alice", uri.User)
		assert.Equal(t, "atlanta.com", uri.Host)
		subject, _ := uri.Headers.Get("subject")
		priority, _ := uri.Headers.Get("priority")
		assert.Equal(t, "project%20x", subject)
		assert.Equal(t, "urgent", priority)
	})

	t.Run("header params parsed", func(t *testing.T) {
		uri = Uri{}
		str = "sip:bob:secret@atlanta.com:9999;rport;transport=tcp;method=REGISTER?to=sip:bob%40biloxi.com"
		err = ParseUri(str, &uri)
		require.NoError(t, err)

		assert.Equal(t, "bob", uri.User)
		assert.Equal(t, "secret", uri.Password)
		assert.Equal(t, "atlanta.com", uri.Host)
		assert.Equal(t, 9999, uri.Port)

		assert.Equal(t, 3, uri.UriParams.Length())
		transport, _ := uri.UriParams.Get("transport")
		method, _ := uri.UriParams.Get("method")
		assert.Equal(t, "tcp", transport)
		assert.Equal(t, "REGISTER", method)

		assert.Equal(t, 1, uri.Headers.Length())
		to, _ := uri.Headers.Get("to")
		assert.Equal(t, "sip:bob%40biloxi.com", to)

	})

	t.Run("params no value", func(t *testing.T) {
		uri = Uri{}
		str = "127.0.0.2:5060;rport;branch=z9hG4bKPj6c65c5d9-b6d0-4a30-9383-1f9b42f97de9"
		err = ParseUri(str, &uri)
		require.NoError(t, err)

		rport, _ := uri.UriParams.Get("rport")
		branch, _ := uri.UriParams.Get("branch")
		assert.Equal(t, "", rport)
		assert.Equal(t, "z9hG4bKPj6c65c5d9-b6d0-4a30-9383-1f9b42f97de9", branch)

	})

	t.Run("tel scheme", func(t *testing.T) {
		testCases := [][]string{
			[]string{"tel:+1-(201) 555 0123", "+1-(201) 555 0123", ""},
			[]string{"tel:7042;phone-context=example.com", "7042", "phone-context", "example.com"},
		}
		for _, testCase := range testCases {
			err = ParseUri(testCase[0], &uri)
			require.NoError(t, err)
			assert.Equal(t, "tel", uri.Scheme)
			assert.Equal(t, testCase[1], uri.User)
			if testCase[2] != "" {
				assert.Less(t, 0, uri.UriParams.Length())
				val, found := uri.UriParams.Get(testCase[2])
				assert.Equal(t, true, found)
				assert.Equal(t, testCase[3], val)
				assert.Equal(t, testCase[0], uri.String())
			}
			assert.False(t, uri.Encrypted)
		}
	})
}

func TestParseUriBad(t *testing.T) {
	t.Run("double ports", func(t *testing.T) {
		str := "sip:127.0.0.1:5060:5060;lr;transport=udp"
		uri := Uri{}
		err := ParseUri(str, &uri)
		require.Error(t, err)
	})
}
