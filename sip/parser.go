package sip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The whitespace characters recognised by the Augmented Backus-Naur Form syntax
// that SIP uses (RFC 3261 S.25).
const abnf = " \t"

// The maximum permissible CSeq number in a SIP message (2**31 - 1).
// C.f. RFC 3261 S. 8.1.1.5.
const maxCseq = 2147483647

var (
	ErrParseLineNoCRLF = errors.New("line has no CRLF")
	ErrParseEOF        = errors.New("EOF on reading line")

	// Stream parse errors
	ErrParseSipPartial         = errors.New("SIP partial data")
	ErrParseReadBodyIncomplete = errors.New("reading body incomplete")
	ErrParseMoreMessages       = errors.New("Stream has more message")

	ParseMaxMessageLength = 65535
)

func ParseMessage(msgData []byte) (Message, error) {
	parser := NewParser()
	return parser.ParseSIP(msgData)
}

// Parser is implementation of SIPParser
// It is optimized with faster header parsing
type Parser struct {
	// HeadersParsers uses default list of headers to be parsed. Smaller list parser will be faster
	headersParsers mapHeadersParser
}

// ParserOption are addition option for NewParser. Check WithParser...
type ParserOption func(p *Parser)

// Create a new Parser.
func NewParser(options ...ParserOption) *Parser {
	p := &Parser{
		headersParsers: headersParsers,
	}

	for _, o := range options {
		o(p)
	}

	return p
}

// WithHeadersParsers allows customizing parser headers parsers
// Consider performance when adding custom parser.
// Add only if it will appear in almost every message
//
// Check DefaultHeadersParser as starting point
func WithHeadersParsers(m map[string]HeaderParser) ParserOption {
	return func(p *Parser) {
		p.headersParsers = m
	}
}

// ParseSIP converts data to sip message. Buffer must contain full sip message
func (p *Parser) ParseSIP(data []byte) (msg Message, err error) {
	reader := bytes.NewBuffer(data)

	startLine, err := nextLine(reader)
	if err != nil {
		return nil, err
	}

	msg, err = parseLine(startLine)
	if err != nil {
		return nil, err
	}

	for {
		line, err := nextLine(reader)

		if err != nil {
			if err == io.EOF {
				return nil, ErrParseEOF
			}
			return nil, err
		}
		size := len(line)
		if size == 0 {
			// We've hit the end of the header section.
			break
		}

		err = p.headersParsers.parseMsgHeader(msg, line)
		if err != nil {
			err := fmt.Errorf("parsing header failed line=%q: %w", line, err)
			return nil, err
		}
	}

	// TODO Use Content Length header
	contentLength := getBodyLength(data)
	if contentLength <= 0 {
		return msg, nil
	}

	// p.log.Debugf("%s reads body with length = %d bytes", p, contentLength)
	body := make([]byte, contentLength)
	total, err := reader.Read(body)
	if err != nil {
		return nil, fmt.Errorf("read message body failed: %w", err)
	}
	// RFC 3261 - 18.3.
	if total != contentLength {
		return nil, fmt.Errorf(
			"incomplete message body: read %d bytes, expected %d bytes",
			len(body),
			contentLength,
		)
	}

	// Should we trim this?
	// if len(bytes.TrimSpace(body)) > 0 {
	if len(body) > 0 {
		msg.SetBody(body)
	}
	return msg, nil
}

// NewSIPStream implements SIP parsing contructor for IO that stream SIP message
// It should be created per each stream
func (p *Parser) NewSIPStream() *ParserStream {
	return &ParserStream{
		headersParsers: p.headersParsers, // safe as it read only
	}
}

func parseLine(startLine string) (msg Message, err error) {
	if isRequest(startLine) {
		recipient := Uri{}
		method, sipVersion, err := parseRequestLine(startLine, &recipient)
		if err != nil {
			return nil, err
		}

		m := NewRequest(method, recipient)
		m.SipVersion = sipVersion
		return m, nil
	}

	if isResponse(startLine) {
		sipVersion, statusCode, reason, err := parseStatusLine(startLine)
		if err != nil {
			return nil, err
		}

		m := NewResponse(statusCode, reason)
		m.SipVersion = sipVersion
		return m, nil
	}
	return nil, fmt.Errorf("transmission beginning '%s' is not a SIP message", startLine)
}

// nextLine should read until it hits CRLF
// ErrParseLineNoCRLF -> could not find CRLF in line
//
// https://datatracker.ietf.org/doc/html/rfc3261#section-7
// empty line MUST be
// terminated by a carriage-return line-feed sequence (CRLF).  Note that
// the empty line MUST be present even if the message-body is not.
func nextLine(reader *bytes.Buffer) (line string, err error) {
	// https://www.rfc-editor.org/rfc/rfc3261.html#section-7
	// The start-line, each message-header line, and the empty line MUST be
	// terminated by a carriage-return line-feed sequence (CRLF).  Note that
	// the empty line MUST be present even if the message-body is not.

	// Lines could be multiline as well so this is also acceptable
	// TO :
	// sip:vivekg@chair-dnrc.example.com ;   tag    = 1918181833n
	line, err = reader.ReadString('\r')
	if err != nil {
		// We may get io.EOF and line till it was read
		return line, err
	}
	br, err := reader.ReadByte()
	if err != nil {
		return line, err
	}

	if br != '\n' {
		return line, ErrParseLineNoCRLF
	}
	lenline := len(line)
	if lenline < 1 {
		return line, ErrParseLineNoCRLF
	}

	line = line[:lenline-1]
	return line, nil
}

// Calculate the size of a SIP message's body, given the entire contents of the message as a byte array.
func getBodyLength(data []byte) int {
	// Body starts with first character following a double-CRLF.
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx == -1 {
		return -1
	}

	bodyStart := idx + 4

	return len(data) - bodyStart
}

// detet is request by spaces
func isRequest(startLine string) bool {
	// SIP request lines contain precisely two spaces.
	ind := strings.IndexRune(startLine, ' ')
	if ind <= 0 {
		return false
	}

	// part0 := startLine[:ind]
	ind1 := strings.IndexRune(startLine[ind+1:], ' ')
	if ind1 <= 0 {
		return false
	}

	part2 := startLine[ind+1+ind1+1:]
	ind2 := strings.IndexRune(part2, ' ')
	if ind2 >= 0 {
		return false
	}

	if len(part2) < 3 {
		return false
	}

	return UriIsSIP(part2[:3])
}

// Detect is response by spaces
func isResponse(startLine string) bool {
	// SIP status lines contain at least two spaces.
	ind := strings.IndexRune(startLine, ' ')
	if ind <= 0 {
		return false
	}

	// part0 := startLine[:ind]
	ind1 := strings.IndexRune(startLine[ind+1:], ' ')
	if ind1 <= 0 {
		return false
	}

	return UriIsSIP(startLine[:3])
}

// Parse the first line of a SIP request, e.g:
//
//	INVITE bob@example.com SIP/2.0
//	REGISTER jane@telco.com SIP/1.0
func parseRequestLine(requestLine string, recipient *Uri) (
	method RequestMethod, sipVersion string, err error) {
	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 {
		err = fmt.Errorf("request line should have 2 spaces: '%s'", requestLine)
		return
	}

	method = RequestMethod(strings.ToUpper(parts[0]))
	err = ParseUri(parts[1], recipient)
	sipVersion = parts[2]

	if recipient.Wildcard {
		err = fmt.Errorf("wildcard URI '*' not permitted in request line: '%s'", requestLine)
		return
	}

	return
}

// Parse the first line of a SIP response, e.g:
//
//	SIP/2.0 200 OK
//	SIP/1.0 403 Forbidden
func parseStatusLine(statusLine string) (
	sipVersion string, statusCode int, reasonPhrase string, err error) {
	parts := strings.Split(statusLine, " ")
	if len(parts) < 3 {
		err = fmt.Errorf("status line has too few spaces: '%s'", statusLine)
		return
	}

	sipVersion = parts[0]
	statusCodeRaw, err := strconv.ParseUint(parts[1], 10, 16)
	statusCode = int(statusCodeRaw)
	reasonPhrase = strings.Join(parts[2:], " ")

	return
}

func filterABNF(s string) string {
	b := strings.Builder{}
	for _, c := range s {
		c = filterABNFRune(c)
		if c < 0 {
			continue
		}
		b.WriteRune(c)
	}

	return b.String()
}

// From std
var asciiSpace = [256]uint8{'\t': 1, '\n': 1, '\v': 1, '\f': 1, '\r': 1, ' ': 1}

// https://datatracker.ietf.org/doc/html/rfc3261#section-25.1
// returns negative rune in case shouldbe skipped
// TODO need to handle SWS, LWS cases better
func filterABNFRune(c rune) rune {
	// We need to detect empty space
	// LWS (Linear Whitespace).
	// All linear white space, including folding, has the same meaning as a single space (SP)
	//
	// SWS (Separating Whitespace) is used when linear white space is optional, typically between tokens and separators.

	if asciiSpace[c] == 1 {
		return -1
	}
	return c
}
