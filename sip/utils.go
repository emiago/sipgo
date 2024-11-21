package sip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"reflect"
	"runtime"
	"strings"
)

const (
	letterBytes   = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

// https://github.com/kpbird/golang_random_string
func RandString(n int) string {
	output := make([]byte, n)
	// We will take n bytes, one byte for each character of output.
	randomness := make([]byte, n)
	// read all random
	_, err := rand.Read(randomness)
	if err != nil {
		panic(err)
	}
	l := len(letterBytes)
	// fill output
	for pos := range output {
		// get random item
		random := randomness[pos]
		// random % 64
		randomPos := random % uint8(l)
		// put into output
		output[pos] = letterBytes[randomPos]
	}

	return string(output)
}

// https://stackoverflow.com/questions/22892120/how-to-generate-a-random-string-of-a-fixed-length-in-go
func RandStringBytesMask(sb *strings.Builder, n int) string {
	sb.Grow(n)
	// A rand.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := n-1, rand.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = rand.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			sb.WriteByte(letterBytes[idx])
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return sb.String()
}

func isASCII(c rune) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// ASCIIToLower is faster than go version. It avoids one more loop
func ASCIIToLower(s string) string {
	// first check is ascii already low to avoid alloc
	nonLowInd := -1
	for i, c := range s {
		if 'a' <= c && c <= 'z' {
			continue
		}
		nonLowInd = i
		break
	}
	if nonLowInd < 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:nonLowInd])
	for i := nonLowInd; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

func ASCIIToLowerInPlace(s []byte) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		s[i] = c
	}
}

func ASCIIToUpper(s string) string {
	// first check is ascii already up to avoid alloc
	nonLowInd := -1
	for i, c := range s {
		if 'A' <= c && c <= 'Z' {
			continue
		}
		nonLowInd = i
		break
	}
	if nonLowInd < 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:nonLowInd])
	for i := nonLowInd; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// HeaderToLower is fast ASCII lower string
func HeaderToLower(s string) string {
	// Avoid allocations
	switch s {
	case "Via", "via":
		return "via"
	case "From", "from":
		return "from"
	case "To", "to":
		return "to"
	case "Call-ID", "call-id":
		return "call-id"
	case "Contact", "contact":
		return "contact"
	case "CSeq", "CSEQ", "cseq":
		return "cseq"
	case "Content-Type", "content-type":
		return "content-type"
	case "Route", "route":
		return "route"
	case "Record-Route", "record-route":
		return "record-route"
	case "Max-Forwards":
		return "max-forwards"
	case "Timestamp", "timestamp":
		return "timestamp"
	}

	// This creates one allocation if we really need to lower
	return ASCIIToLower(s)
}

// Check uri is SIP fast
func UriIsSIP(s string) bool {
	switch s {
	case "sip", "SIP":
		return true
	}
	return false
}

func UriIsSIPS(s string) bool {
	switch s {
	case "sips", "SIPS":
		return true
	}
	return false
}

// Splits the given string into sections, separated by one or more characters
// from c_ABNF_WS.
func SplitByWhitespace(text string) []string {
	var buffer bytes.Buffer
	var inString = true
	result := make([]string, 0)

	for _, char := range text {
		s := string(char)
		if strings.Contains(abnf, s) {
			if inString {
				// First whitespace char following text; flush buffer to the results array.
				result = append(result, buffer.String())
				buffer.Reset()
			}
			inString = false
		} else {
			buffer.WriteString(s)
			inString = true
		}
	}

	if buffer.Len() > 0 {
		result = append(result, buffer.String())
	}

	return result
}

// A delimiter is any pair of characters used for quoting text (i.e. bulk escaping literals).
type delimiter struct {
	start uint8
	end   uint8
}

// Define common quote characters needed in parsing.
var quotesDelim = delimiter{'"', '"'}

var anglesDelim = delimiter{'<', '>'}

// Find the first instance of the target in the given text which is not enclosed in any delimiters
// from the list provided.
func findUnescaped(text string, target uint8, delims ...delimiter) int {
	return findAnyUnescaped(text, string(target), delims...)
}

// Find the first instance of any of the targets in the given text that are not enclosed in any delimiters
// from the list provided.
func findAnyUnescaped(text string, targets string, delims ...delimiter) int {
	escaped := false
	var endEscape uint8 = 0

	endChars := make(map[uint8]uint8)
	for _, delim := range delims {
		endChars[delim.start] = delim.end
	}

	for idx := 0; idx < len(text); idx++ {
		if !escaped && strings.Contains(targets, string(text[idx])) {
			return idx
		}

		if escaped {
			escaped = text[idx] != endEscape
			continue
		} else {
			endEscape, escaped = endChars[text[idx]]
		}
	}

	return -1
}

// ResolveInterfaceIP will check current interfaces and resolve to IP
// Using targetIP it will try to match interface with same subnet
// network can be "ip" "ip4" "ip6"
// by default it avoids loopack IP unless targetIP is loopback
func ResolveInterfacesIP(network string, targetIP *net.IPNet) (net.IP, net.Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, net.Interface{}, err
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			if targetIP != nil && !targetIP.IP.IsLoopback() {
				continue // loopback interface
			}
		}

		ip, err := resolveInterfaceIp(iface, network, targetIP)
		if errors.Is(err, io.EOF) {
			continue
		}
		return ip, iface, err
	}

	return nil, net.Interface{}, errors.New("no interface found on system")
}

func resolveInterfaceIp(iface net.Interface, network string, targetIP *net.IPNet) (net.IP, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		var ip net.IP
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			// IPAddr is returned on multicast not on unicast
			continue
		}
		ip = ipNet.IP
		if targetIP != nil {
			if !targetIP.Contains(ip) {
				continue
			}
		} else {
			if ip.IsLoopback() {
				continue
			}
		}

		if ip == nil {
			continue
		}

		switch network {
		case "ip4":
			if ip.To4() == nil {
				continue
			}

		case "ip6":
			// IP is v6 only if this returns nil
			if ip.To4() != nil {
				continue
			}
		}

		return ip, nil
	}
	return nil, io.EOF
}

func NonceWrite(buf []byte) {
	const letterBytes = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	length := len(letterBytes)
	for i := range buf {
		buf[i] = letterBytes[rand.Intn(length)]
	}
}

// MessageShortString dumps short version of msg. Used only for logging
func MessageShortString(msg Message) string {
	switch m := msg.(type) {
	case *Request:
		return m.Short()
	case *Response:
		return m.Short()
	}
	return "Unknown message type"
}

func compareFunctions(fsm1 any, fsm2 any) error {
	funcName1 := runtime.FuncForPC(reflect.ValueOf(fsm1).Pointer()).Name()
	funcName2 := runtime.FuncForPC(reflect.ValueOf(fsm2).Pointer()).Name()
	if funcName1 != funcName2 {
		return fmt.Errorf("Functions are not equal f1=%q, f2=%q", funcName1, funcName2)
	}
	return nil
}
