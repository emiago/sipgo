package sip

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAddressValue(t *testing.T) {
	t.Run("All", func(t *testing.T) {
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
		assert.Equal(t, true, uri.Encrypted)
		assert.Equal(t, false, uri.Wildcard)

		user, ok := uri.UriParams.Get("user")
		assert.True(t, ok)
		assert.Equal(t, 1, uri.UriParams.Length())
		assert.Equal(t, "phone", user)

	})

	t.Run("NoDisplayName", func(t *testing.T) {
		address := "sip:1215174826@222.222.222.222;tag=9300025590389559597"
		uri := Uri{}
		params := NewParams()
		displayName, err := ParseAddressValue(address, &uri, params)
		require.NoError(t, err)

		assert.Equal(t, "", displayName)
		assert.Equal(t, "1215174826", uri.User)
		assert.Equal(t, "222.222.222.222", uri.Host)
		assert.Equal(t, false, uri.Encrypted)
	})

	t.Run("Wildcard", func(t *testing.T) {
		address := "*"
		uri := Uri{}
		params := NewParams()
		displayName, err := ParseAddressValue(address, &uri, params)
		require.NoError(t, err)

		assert.Equal(t, "", displayName)
		assert.Equal(t, "*", uri.Host)
		assert.Equal(t, true, uri.Wildcard)
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

// TODO
// func TestParseAddressMultiline(t *testing.T) {
// contact:
// 	+`Contact: "Mr. Watson" <sip:watson@worcester.bell-telephone.com>
// 	;q=0.7; expires=3600,
// 	"Mr. Watson" <mailto:watson@bell-telephone.com> ;q=0.1`
// }

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
