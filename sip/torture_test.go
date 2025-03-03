package sip

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
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

	// TODO these should fail with specific error we should validate against
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
			require.NoErrorf(t, err, "Error reading torture file %s", test)

			_, err = parser.ParseSIP(data)
			require.NoErrorf(t, err, "error parsing %s", test)
		})
	}

	for _, test := range invalidTests {
		t.Run(test, func(t *testing.T) {
			data, err := os.ReadFile("testdata/torture/invalid/" + test + ".dat")
			require.NoErrorf(t, err, "Error reading torture file %s", test)

			_, err = parser.ParseSIP(data)
			require.Errorf(t, err, "test %s should be failing", test)
		})
	}
}
