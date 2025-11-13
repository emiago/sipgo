package sip

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAddressValue(t *testing.T) {
	t.Run("all", func(t *testing.T) {
		address := "\"Bob\" <sips:bob:password@127.0.0.1:5060;user=phone>;tag=1234"

		uri := Uri{}
		params := NewParams()

		displayName, err := ParseAddressValue(address, &uri, params)

		assert.Nil(t, err)
		assert.Equal(t, "sips:bob:password@127.0.0.1:5060;user=phone", uri.String())
		assert.Equal(t, "tag=1234", params.String())

		assert.Equal(t, "Bob", displayName)
		assert.Equal(t, "bob", uri.User)
		assert.Equal(t, "password", uri.Password)
		assert.Equal(t, "127.0.0.1", uri.Host)
		assert.Equal(t, 5060, uri.Port)
		assert.Equal(t, true, uri.IsEncrypted())
		assert.Equal(t, false, uri.Wildcard)

		user, ok := uri.UriParams.Get("user")
		assert.True(t, ok)
		assert.Equal(t, 1, uri.UriParams.Length())
		assert.Equal(t, "phone", user)

	})

	t.Run("no display name", func(t *testing.T) {
		address := "sip:1215174826@222.222.222.222;tag=9300025590389559597"
		uri := Uri{}
		params := NewParams()
		displayName, err := ParseAddressValue(address, &uri, params)
		require.NoError(t, err)

		assert.Equal(t, "", displayName)
		assert.Equal(t, "1215174826", uri.User)
		assert.Equal(t, "222.222.222.222", uri.Host)
		assert.Equal(t, false, uri.IsEncrypted())
	})

	t.Run("nil uri params", func(t *testing.T) {
		address := "sip:1215174826@222.222.222.222:5066"
		uri := Uri{}
		params := NewParams()
		displayName, err := ParseAddressValue(address, &uri, params)
		require.NoError(t, err)

		assert.Equal(t, "", displayName)
		assert.Equal(t, "1215174826", uri.User)
		assert.Equal(t, "222.222.222.222", uri.Host)
		assert.Equal(t, HeaderParams{}, uri.UriParams)
		assert.Equal(t, false, uri.IsEncrypted())
	})

	t.Run("wildcard", func(t *testing.T) {
		address := "*"
		uri := Uri{}
		params := NewParams()
		displayName, err := ParseAddressValue(address, &uri, params)
		require.NoError(t, err)

		assert.Equal(t, "", displayName)
		assert.Equal(t, "*", uri.Host)
		assert.Equal(t, true, uri.Wildcard)
	})

	t.Run("quoted-pairs", func(t *testing.T) {
		address := "\"!\\\"#$%&/'()*+-.,0123456789:;<=>? @ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\\\]^_'abcdefghijklmnopqrstuvwxyz{|}\" <sip:bob@127.0.0.1:5060;user=phone>;tag=1234"
		uri := Uri{}
		params := NewParams()
		displayName, err := ParseAddressValue(address, &uri, params)
		require.NoError(t, err)

		assert.Equal(t, "sip:bob@127.0.0.1:5060;user=phone", uri.String())
		assert.Equal(t, "tag=1234", params.String())

		assert.Equal(t, "!\\\"#$%&/'()*+-.,0123456789:;<=>? @ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\\\]^_'abcdefghijklmnopqrstuvwxyz{|}", displayName)
		assert.Equal(t, "bob", uri.User)
		assert.Equal(t, "", uri.Password)
		assert.Equal(t, "127.0.0.1", uri.Host)
		assert.Equal(t, 5060, uri.Port)
		assert.Equal(t, false, uri.IsEncrypted())
		assert.Equal(t, false, uri.Wildcard)

		user, ok := uri.UriParams.Get("user")
		assert.True(t, ok)
		assert.Equal(t, 1, uri.UriParams.Length())
		assert.Equal(t, "phone", user)

	})

}

func TestParseAddressBad(t *testing.T) {

	t.Run("double ports in uri", func(t *testing.T) {
		uri := Uri{}
		params := NewParams()
		address := "<sip:127.0.0.1:5060:5060;lr;transport=udp>"
		_, err := ParseAddressValue(address, &uri, params)
		require.Error(t, err)
	})
}

func BenchmarkParseAddress(b *testing.B) {
	address := "\"Bob\" <sips:bob:password@127.0.0.1:5060;user=phone>;tag=1234"
	uri := Uri{}
	params := NewParams()

	for i := 0; i < b.N; i++ {
		displayName, err := ParseAddressValue(address, &uri, params)
		assert.Nil(b, err)
		assert.Equal(b, "Bob", displayName)
	}
}
