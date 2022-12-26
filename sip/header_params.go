package sip

import (
	"io"
	"strings"
)

// Whitespace recognised by SIP protocol.
const abnfWs = " \t"

type Params interface {
	Get(key string) (string, bool)
	Add(key string, val string) Params
	Remove(key string) Params
	Clone() Params
	Equals(params interface{}) bool
	ToString(sep uint8) string
	ToStringWrite(sep uint8, buffer io.StringWriter)
	String() string
	Length() int
	Items() map[string]string
	Keys() []string
	Has(key string) bool
}

type HeaderKV struct {
	K string
	V string
}

// HeaderParams are key value params. They do not provide order by default due to performance reasons
type HeaderParams map[string]string

// Create an empty set of parameters.
func NewParams() HeaderParams {
	return HeaderParams{}
}

// Items returns the entire parameter map.
func (hp HeaderParams) Items() map[string]string {
	m := make(map[string]string, len(hp))
	for k, v := range hp {
		m[k] = v
	}
	return m
}

// Keys return a slice of keys, in order.
func (hp HeaderParams) Keys() []string {
	s := make([]string, len(hp))
	i := 0
	for k := range hp {
		s[i] = k
	}
	return s
}

// Get returns existing key
func (hp HeaderParams) Get(key string) (string, bool) {
	v, ok := hp[key]
	return v, ok
}

// Add will add new key:val. If key exists it will be overwriten
func (hp HeaderParams) Add(key string, val string) Params {
	hp[key] = val
	return hp
}

// Remove removes param with exact key
func (hp HeaderParams) Remove(key string) Params {
	delete(hp, key)
	return hp
}

// Has checks does key exists
func (hp HeaderParams) Has(key string) bool {
	_, exists := hp[key]
	return exists
}

// Clone returns underneath map copied
func (hp HeaderParams) Clone() Params {
	return hp.clone()
}

func (hp HeaderParams) clone() HeaderParams {
	dup := make(HeaderParams, len(hp))

	for k, v := range hp {
		dup.Add(k, v)
	}

	return dup
}

// ToString renders params to a string.
// Note that this does not escape special characters, this should already have been done before calling this method.
func (hp HeaderParams) ToString(sep uint8) string {
	if hp == nil || len(hp) == 0 {
		return ""
	}

	// sepstr := fmt.Sprintf("%c", sep)
	sepstr := string(sep)
	var buffer strings.Builder

	for k, v := range hp {
		buffer.WriteString(sepstr)
		buffer.WriteString(k)
		// This could be removed
		if strings.ContainsAny(v, abnfWs) {
			buffer.WriteString("=\"")
			buffer.WriteString(v)
			buffer.WriteString("\"")
		} else if v != "" {
			// Params can be without value like ;lr;
			buffer.WriteString("=")
			buffer.WriteString(v)
		}
	}

	return buffer.String()[1:]
}

// ToStringWrite is same as ToString but it stores to defined buffer instead returning string
func (hp HeaderParams) ToStringWrite(sep uint8, buffer io.StringWriter) {
	if hp == nil || len(hp) == 0 {
		return
	}

	// sepstr := fmt.Sprintf("%c", sep)
	sepstr := string(sep)
	i := 0
	for k, v := range hp {
		if i > 0 {
			buffer.WriteString(sepstr)
		}
		i++

		buffer.WriteString(k)
		if v == "" {
			continue
		}
		// This could be removed
		if strings.ContainsAny(v, abnfWs) {
			buffer.WriteString("=\"")
			buffer.WriteString(v)
			buffer.WriteString("\"")
		} else {
			buffer.WriteString("=")
			buffer.WriteString(v)
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
