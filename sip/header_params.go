package sip

import (
	"io"
	"slices"
	"strings"
)

// HeaderKV is a key-value pair for URI or header params.
type HeaderKV struct {
	K string
	V string
}

// HeaderParams are key value params.
type HeaderParams []HeaderKV

// NewParams creates an empty set of parameters.
func NewParams() HeaderParams {
	// Typical number of params:
	// URI: 1-2
	// Via: 1-2
	// Route: 4
	return make(HeaderParams, 0, 4)
}

// Items returns the entire parameter map.
func (hp HeaderParams) Items() map[string]string {
	m := make(map[string]string, len(hp))
	for _, kv := range hp {
		m[kv.K] = kv.V
	}
	return m
}

// Keys return a slice of keys, in order of appearance.
func (hp HeaderParams) Keys() []string {
	s := make([]string, 0, len(hp))
	for _, kv := range hp {
		if slices.Contains(s, kv.K) {
			continue
		}
		s = append(s, kv.K)
	}
	return s
}

func (hp HeaderParams) index(key string) int {
	for i, kv := range hp {
		if kv.K == key {
			return i
		}
	}
	return -1
}

// Get returns a value for a given key, if it exists.
func (hp HeaderParams) Get(key string) (string, bool) {
	for _, kv := range hp {
		if kv.K == key {
			return kv.V, true
		}
	}
	return "", false
}

// GetOr returns a value for a given key, oe a default, if it doesn't exist.
func (hp HeaderParams) GetOr(key, def string) string {
	for _, kv := range hp {
		if kv.K == key {
			return kv.V
		}
	}
	return def
}

// Add will add new key-value. If key exists it will be overwritten.
func (hp *HeaderParams) Add(key string, val string) HeaderParams {
	if i := hp.index(key); i >= 0 {
		(*hp)[i].V = val
	} else {
		*hp = append(*hp, HeaderKV{K: key, V: val})
	}
	return *hp
}

// Remove removes all values with a given key.
func (hp *HeaderParams) Remove(key string) HeaderParams {
	for {
		i := hp.index(key)
		if i < 0 {
			return *hp
		}
		*hp = slices.Delete(*hp, i, i+1)
	}
}

// Has checks does key exists
func (hp HeaderParams) Has(key string) bool {
	return hp.index(key) >= 0
}

// Clone returns underneath params copied
func (hp HeaderParams) Clone() HeaderParams {
	return hp.clone()
}

func (hp HeaderParams) clone() HeaderParams {
	return slices.Clone(hp)
}

// ToString renders params to a string.
// Note that this does not escape special characters, this should already have been done before calling this method.
func (hp HeaderParams) ToString(sep byte) string {
	if len(hp) == 0 {
		return ""
	}

	var buffer strings.Builder
	for _, kv := range hp {
		buffer.WriteByte(sep)
		buffer.WriteString(kv.K)
		// This could be removed
		if strings.ContainsAny(kv.V, abnf) {
			buffer.WriteString("=\"")
			buffer.WriteString(kv.V)
			buffer.WriteByte('"')
		} else if kv.V != "" {
			// Params can be without value like ;lr;
			buffer.WriteByte('=')
			buffer.WriteString(kv.V)
		}
	}

	return buffer.String()[1:]
}

// ToStringWrite is same as ToString but it stores to defined buffer instead returning string
func (hp HeaderParams) ToStringWrite(sep byte, buffer io.StringWriter) {
	if hp == nil || len(hp) == 0 {
		return
	}

	sepstr := string(sep)
	i := 0
	for _, kv := range hp {
		if i > 0 {
			buffer.WriteString(sepstr)
		}
		i++

		buffer.WriteString(kv.K)
		if kv.V == "" {
			continue
		}
		// This could be removed
		if strings.ContainsAny(kv.V, abnf) {
			buffer.WriteString("=\"")
			buffer.WriteString(kv.V)
			buffer.WriteString("\"")
		} else {
			buffer.WriteString("=")
			buffer.WriteString(kv.V)
		}
	}
}

// String returns params joined with '&' char.
func (hp HeaderParams) String() string {
	return hp.ToString('&')
}

// Length returns number of params.
func (hp HeaderParams) Length() int {
	return len(hp)
}

// Equals check if two maps of parameters are equal in the sense of having the same keys with the same values.
// This does not rely on any ordering of the keys of the map in memory.
func (hp HeaderParams) Equals(other interface{}) bool {
	q, ok := other.(HeaderParams)
	if !ok {
		return false
	}

	hplen := hp.Length()
	qlen := q.Length()
	if hplen != qlen {
		return false
	}

	if hplen == 0 && qlen == 0 {
		return true
	}

	for key, pVal := range hp.Items() {
		qVal, ok := q.Get(key)
		if !ok {
			return false
		}
		if pVal != qVal {
			return false
		}
	}

	return true
}
