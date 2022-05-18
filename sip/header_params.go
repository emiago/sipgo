package sip

import (
	"io"
	"strings"
)

// SIP Headers structs
// Originally forked from https://github.com/ghettovoice/gosip

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
type HeaderParamsOrdered []HeaderKV

// Create an empty set of parameters.
func NewOrderedParams() HeaderParamsOrdered {
	return HeaderParamsOrdered{}
}

// Returns the entire parameter map.
func (hp HeaderParamsOrdered) Items() map[string]string {
	m := make(map[string]string, len(hp))
	for _, v := range hp {
		m[v.K] = v.V
	}
	return m
}

// Returns a slice of keys, in order.
func (hp HeaderParamsOrdered) Keys() []string {
	s := make([]string, len(hp))
	for i, v := range hp {
		s[i] = v.V
	}
	return s
}

// Returns the requested parameter value.
func (hp HeaderParamsOrdered) Get(key string) (string, bool) {
	for _, v := range hp {
		if v.K == key {
			return v.V, true
		}
	}
	return "", false
}

// Put a new parameter.
func (hp HeaderParamsOrdered) Add(key string, val string) Params {
	hp = append(hp, HeaderKV{key, val})
	return hp
}

func (hp HeaderParamsOrdered) Remove(key string) Params {
	for i, v := range hp {
		if v.K == key {
			hp = append(hp[:i], hp[i+1:]...)
		}
	}
	return hp
}

func (hp HeaderParamsOrdered) Has(key string) bool {
	for _, v := range hp {
		if v.K == key {
			return true
		}
	}

	return false
}

// Copy a list of params.
func (hp HeaderParamsOrdered) Clone() Params {
	dup := make(HeaderParamsOrdered, len(hp))

	for _, v := range hp {
		dup.Add(v.K, v.V)
	}

	return dup
}

// Render params to a string.
// Note that this does not escape special characters, this should already have been done before calling this method.
func (hp HeaderParamsOrdered) ToString(sep uint8) string {
	if hp == nil || len(hp) == 0 {
		return ""
	}

	sepstr := string(sep)
	var buffer strings.Builder

	for _, v := range hp {
		buffer.WriteString(sepstr)
		buffer.WriteString(v.K)
		val := v.V
		// This could be removed
		if strings.ContainsAny(val, abnfWs) {
			buffer.WriteString("=\"")
			buffer.WriteString(val)
			buffer.WriteString("\"")
		} else {
			buffer.WriteString("=")
			buffer.WriteString(val)
		}
	}

	return buffer.String()[1:]
}

func (hp HeaderParamsOrdered) ToStringWrite(sep uint8, buffer io.StringWriter) {

}

// String returns params joined with '&' char.
func (hp HeaderParamsOrdered) String() string {
	return hp.ToString('&')
}

// Returns number of params.
func (hp HeaderParamsOrdered) Length() int {
	return len(hp)
}

// Check if two maps of parameters are equal in the sense of having the same keys with the same values.
// This does not rely on any ordering of the keys of the map in memory.
func (hp HeaderParamsOrdered) Equals(other interface{}) bool {
	q, ok := other.(HeaderParamsOrdered)
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

type HeaderParams map[string]string

// Create an empty set of parameters.
func NewParams() HeaderParams {
	return HeaderParams{}
}

// Returns the entire parameter map.
func (hp HeaderParams) Items() map[string]string {
	m := make(map[string]string, len(hp))
	for k, v := range hp {
		m[k] = v
	}
	return m
}

// Returns a slice of keys, in order.
func (hp HeaderParams) Keys() []string {
	s := make([]string, len(hp))
	i := 0
	for k, _ := range hp {
		s[i] = k
	}
	return s
}

// Returns the requested parameter value.
func (hp HeaderParams) Get(key string) (string, bool) {
	v, ok := hp[key]
	return v, ok
}

// Put a new parameter.
func (hp HeaderParams) Add(key string, val string) Params {
	hp[key] = val
	return hp
}

func (hp HeaderParams) Remove(key string) Params {
	delete(hp, key)
	return hp
}

func (hp HeaderParams) Has(key string) bool {
	_, exists := hp[key]
	return exists
}

// Copy a list of params.
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

// Render params to a string.
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

// Returns number of params.
func (hp HeaderParams) Length() int {
	return len(hp)
}

// Check if two maps of parameters are equal in the sense of having the same keys with the same values.
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
