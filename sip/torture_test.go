package sip

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTorture(t *testing.T) {
	parser := NewParser()

	validTests := []string{
		"dblreq",
		//"esc01",
		"esc02",
		"escnull",
		"intmeth",
		"longreq",
		"lwsdisp",
		"mpart01",
		"noreason",
		//"semiuri",
		"transports",
		"unreason",
		//"wsinv",
	}
	invalidTests := []string{
		"badaspec",
		//"badbranch",
		//"baddate",
		"baddn",
		"badinv01",
		//"badvers",
		//"bcast",
		//"bext01",
		"bigcode",
		//"clerr",
		//"cparam01",
		//"cparam02",
		//"escruri",
		//"insuf",
		//"inv2543",
		//"invut",
		"ltgtruri",
		"lwsruri",
		"lwsstart",
		//"mcl01",
		//"mismatch01",
		//"mismatch02",
		//"multi01",
		"ncl",
		"novelsc",
		//"quotbal",
		//"regaut01",
		//"regbadct",
		//"regescrt",
		"scalar02",
		"scalarlg",
		//"sdp01",
		"test",
		"trws",
		//"unkscm",
		//"unksm2",
		//"zeromf",
	}
	for _, test := range validTests {
		t.Run(test, func(t *testing.T) {
			data, err := os.ReadFile("testdata/torture/valid/" + test + ".dat")
			assert.NoError(t, err)

			_, err = parser.ParseSIP(data)
			if err != nil {
				require.NoErrorf(t, err, fmt.Sprintf("error parsing %s", test))
			}
		})
	}

	for _, test := range invalidTests {
		t.Run(test, func(t *testing.T) {
			data, err := os.ReadFile("testdata/torture/invalid/" + test + ".dat")
			assert.NoError(t, err)

			_, err = parser.ParseSIP(data)
			if err == nil {
				require.Fail(t, "test %f should be failing", test)
			}
		})
	}
}
