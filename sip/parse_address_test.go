package sip

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAddressValue(t *testing.T) {
	address := "\"Bob\" <sips:bob:password@127.0.0.1:5060;user=phone>;tag=1234"

	uri := Uri{}
	params := NewParams()

	displayName, err := ParseAddressValue(address, &uri, params)

	assert.Nil(t, err)
	assert.Equal(t, "sips:bob:password@127.0.0.1:5060;user=phone", uri.String())
	assert.Equal(t, "tag=1234", params.String())

	assert.Equal(t, displayName, "Bob")
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
}
