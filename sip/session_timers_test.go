package sip

import "testing"

// headersWith builds headers carrying a single generic header, the same way an
// inbound message exposes an unregistered header before any lazy accessor runs.
// When present is false the header is absent, modelling a peer that offers no
// session timers.
func headersWith(present bool, name, value string) *headers {
	hs := &headers{}
	if present {
		hs.AppendHeader(NewHeader(name, value))
	}
	return hs
}

func TestSessionExpiresParse(t *testing.T) {
	tests := []struct {
		name       string
		present    bool
		header     string
		raw        string
		wantNil    bool
		wantDelta  uint32
		wantParams map[string]string // nil => expect no params
	}{
		{
			name:       "delta and refresher uas",
			present:    true,
			raw:        "1800;refresher=uas",
			wantDelta:  1800,
			wantParams: map[string]string{"refresher": "uas"},
		},
		{
			name:       "delta and refresher uac kept as sent",
			present:    true,
			raw:        "1800;refresher=uac",
			wantDelta:  1800,
			wantParams: map[string]string{"refresher": "uac"},
		},
		{
			name:      "delta only",
			present:   true,
			raw:       "1800",
			wantDelta: 1800,
		},
		{
			// Compact form of Session-Expires is "x" per RFC 4028.
			name:       "compact form x",
			present:    true,
			header:     "x",
			raw:        "1800;refresher=uas",
			wantDelta:  1800,
			wantParams: map[string]string{"refresher": "uas"},
		},
		{
			// Zero parses as delta 0. Enforcing the Min-SE floor is policy for
			// the caller, not a parse error.
			name:      "zero delta parses",
			present:   true,
			raw:       "0",
			wantDelta: 0,
		},
		{
			// Unknown params are kept next to the refresher. Parsing does not
			// elect or filter.
			name:       "extra param kept",
			present:    true,
			raw:        "1800;refresher=uas;foo=bar",
			wantDelta:  1800,
			wantParams: map[string]string{"refresher": "uas", "foo": "bar"},
		},
		{
			name:      "upper bound uint32 max",
			present:   true,
			raw:       "4294967295",
			wantDelta: 4294967295,
		},
		{
			name:    "non numeric delta rejected",
			present: true,
			raw:     "uas",
			wantNil: true,
		},
		{
			name:    "empty value rejected",
			present: true,
			raw:     "",
			wantNil: true,
		},
		{
			name:    "overflow uint32 rejected",
			present: true,
			raw:     "4294967296",
			wantNil: true,
		},
		{
			name:    "header absent returns nil",
			present: false,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := tc.header
			if header == "" {
				header = "Session-Expires"
			}

			hs := headersWith(tc.present, header, tc.raw)
			got := hs.SessionExpires()

			if tc.wantNil {
				if got != nil {
					t.Fatalf("SessionExpires() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("SessionExpires() = nil, want Delta=%d", tc.wantDelta)
			}
			if got.Delta != tc.wantDelta {
				t.Fatalf("Delta = %d, want %d", got.Delta, tc.wantDelta)
			}
			assertParams(t, got.Params, tc.wantParams)
		})
	}
}

func TestMinSEParse(t *testing.T) {
	tests := []struct {
		name    string
		present bool
		raw     string
		wantNil bool
		want    uint32
	}{
		{name: "valid", present: true, raw: "90", want: 90},
		{name: "upper bound uint32 max", present: true, raw: "4294967295", want: 4294967295},
		{name: "overflow uint32 rejected", present: true, raw: "4294967296", wantNil: true},
		{name: "non numeric rejected", present: true, raw: "abc", wantNil: true},
		{name: "empty rejected", present: true, raw: "", wantNil: true},
		{name: "header absent returns nil", present: false, wantNil: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hs := headersWith(tc.present, "Min-SE", tc.raw)
			got := hs.MinSE()

			if tc.wantNil {
				if got != nil {
					t.Fatalf("MinSE() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("MinSE() = nil, want %d", tc.want)
			}
			if uint32(*got) != tc.want {
				t.Fatalf("MinSE() = %d, want %d", uint32(*got), tc.want)
			}
		})
	}
}

// params builds HeaderParams from ordered key/value pairs.
func params(kv ...string) HeaderParams {
	p := NewParams()
	for i := 0; i < len(kv); i += 2 {
		p.Add(kv[i], kv[i+1])
	}
	return p
}

func TestSessionExpiresStringWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    SessionExpiresHeader
		want string
	}{
		{
			name: "delta only",
			h:    SessionExpiresHeader{Delta: 1800},
			want: "Session-Expires: 1800",
		},
		{
			name: "with refresher",
			h:    SessionExpiresHeader{Delta: 1800, Params: params("refresher", "uas")},
			want: "Session-Expires: 1800;refresher=uas",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.h.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMinSEStringWrite(t *testing.T) {
	h := MinSEHeader(90)
	if got, want := h.String(), "Min-SE: 90"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestSessionExpiresClone checks the clone does not share the params backing
// array with the original.
func TestSessionExpiresClone(t *testing.T) {
	h := &SessionExpiresHeader{Delta: 1800, Params: params("refresher", "uas")}

	clone := h.Clone()
	clone.Params.Add("refresher", "uac")
	clone.Delta = 90

	if got, ok := h.Params.Get("refresher"); !ok || got != "uas" {
		t.Fatalf("original refresher = %q (ok=%v), want %q", got, ok, "uas")
	}
	if h.Delta != 1800 {
		t.Fatalf("original Delta = %d, want 1800", h.Delta)
	}
}

// assertParams checks the parsed params match want exactly. A nil want means no
// params at all.
func assertParams(t *testing.T, got HeaderParams, want map[string]string) {
	t.Helper()
	if want == nil {
		if got.Length() != 0 {
			t.Fatalf("params = %v, want none", got)
		}
		return
	}
	for k, v := range want {
		gv, ok := got.Get(k)
		if !ok || gv != v {
			t.Fatalf("param %q = %q (ok=%v), want %q", k, gv, ok, v)
		}
	}
	if got.Length() != len(want) {
		t.Fatalf("params length = %d, want %d (%v)", got.Length(), len(want), got)
	}
}
