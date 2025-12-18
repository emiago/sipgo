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

func asciiToLower(s []byte) []byte {
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

	b := make([]byte, len(s))
	copy(b, s[:nonLowInd])
	for i := nonLowInd; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return b
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

var (
	hdrVia           = []byte("via")
	hdrFrom          = []byte("from")
	hdrTo            = []byte("to")
	hdrCallID        = []byte("call-id")
	hdrContact       = []byte("contact")
	hdrCSeq          = []byte("cseq")
	hdrContentType   = []byte("content-type")
	hdrContentLength = []byte("content-length")
	hdrRoute         = []byte("route")
	hdrRecordRoute   = []byte("record-route")
	hdrMaxForwards   = []byte("max-forwards")
	hdrTimestamp     = []byte("timestamp")
)

func headerToLower(s []byte) []byte {
	if len(s) == 1 {
		c := s[0]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		switch c {
		case 't':
			return hdrTo
		case 'f':
			return hdrFrom
		case 'v':
			return hdrVia
		case 'i':
			return hdrCallID
		case 'l':
			return hdrContentLength
		case 'c':
			return hdrContentType
		case 'm':
			return hdrContact
		}
	}
	// Avoid allocations
	switch string(s) {
	case "Via", "via":
		return hdrVia
	case "From", "from":
		return hdrFrom
	case "To", "to":
		return hdrTo
	case "Call-ID", "call-id":
		return hdrCallID
	case "Contact", "contact":
		return hdrContact
	case "CSeq", "CSEQ", "cseq":
		return hdrCSeq
	case "Content-Type", "content-type":
		return hdrContentType
	case "Content-Length", "content-length":
		return hdrContentLength
	case "Route", "route":
		return hdrRoute
	case "Record-Route", "record-route":
		return hdrRecordRoute
	case "Max-Forwards":
		return hdrMaxForwards
	case "Timestamp", "timestamp":
		return hdrTimestamp
	}

	// This creates one allocation if we really need to lower
	return asciiToLower(s)
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
	case "Content-Length", "content-length":
		return "content-length"
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
func splitByWhitespace(text string) []string {
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

		ip, err := ResolveInterfaceIp(iface, network, targetIP)
		if errors.Is(err, io.EOF) {
			continue
		}
		return ip, iface, err
	}

	return nil, net.Interface{}, errors.New("no interface found on system")
}

func ResolveInterfaceIp(iface net.Interface, network string, targetIP *net.IPNet) (net.IP, error) {
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

		// IP is v6 only if this returns nil
		isIP4 := ip.To4() != nil
		switch network {
		case "ip4":
			if isIP4 {
				return ip, nil
			}

		case "ip6":
			if !isIP4 {
				return ip, nil
			}
		}
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

// messageShortString dumps short version of msg. Used only for logging
func messageShortString(msg Message) string {
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

func isIPV6(host string) bool {
	// Quick reject (has dot)
	for c := range host {
		if c == '.' {
			return false
		}
		if c == ':' {
			break
		}
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}

func uriIP(ip string) string {
	if isIPV6(ip) {
		return "[" + ip + "]"
	}
	return ip
}

// uriNetIP returns parsable IP by net
func uriNetIP(ip string) string {
	return strings.Trim(ip, "[]")
}

func printStack(args ...any) {
	buf := make([]byte, 8192)
	n := runtime.Stack(buf, false)
	fmt.Println(args...)
	fmt.Println(string(buf[:n]))
}
