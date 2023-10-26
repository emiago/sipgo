package sip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// The whitespace characters recognised by the Augmented Backus-Naur Form syntax
// that SIP uses (RFC 3261 S.25).
const abnfWs = " \t"

// The maximum permissible CSeq number in a SIP message (2**31 - 1).
// C.f. RFC 3261 S. 8.1.1.5.
const maxCseq = 2147483647

var (
	ErrParseLineNoCRLF     = errors.New("line has no CRLF")
	ErrParseInvalidMessage = errors.New("invalid SIP message")

	// Stream parse errors
	ErrParseSipPartial         = errors.New("SIP partial data")
	ErrParseReadBodyIncomplete = errors.New("reading body incomplete")
	ErrParseMoreMessages       = errors.New("Stream has more message")
)

var bufReader = sync.Pool{
	New: func() interface{} {
		// The Pool's New function should generally only return pointer
		// types, since a pointer can be put into the return interface
		// value without an allocation:
		return new(bytes.Buffer)
	},
}

func ParseMessage(msgData []byte) (Message, error) {
	parser := NewParser()
	return parser.ParseSIP(msgData)
}

// Parser is implementation of SIPParser
// It is optimized with faster header parsing
type Parser struct {
	log zerolog.Logger
	// HeadersParsers uses default list of headers to be parsed. Smaller list parser will be faster
	headersParsers mapHeadersParser
}

// ParserOption are addition option for NewParser. Check WithParser...
type ParserOption func(p *Parser)

// Create a new Parser.
func NewParser(options ...ParserOption) *Parser {
	p := &Parser{
		log:            log.Logger,
		headersParsers: headersParsers,
	}

	for _, o := range options {
		o(p)
	}

	return p
}

// WithServerLogger allows customizing parser logger
func WithParserLogger(logger zerolog.Logger) ParserOption {
	return func(p *Parser) {
		p.log = logger
	}
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
	reader := bufReader.Get().(*bytes.Buffer)
	defer bufReader.Put(reader)
	reader.Reset()
	reader.Write(data)

	startLine, err := nextLine(reader)
	if err != nil {
		return nil, err
	}

	msg, err = ParseLine(startLine)
	if err != nil {
		return nil, err
	}

	for {
		line, err := nextLine(reader)

		if err != nil {
			if err == io.EOF {
				return nil, ErrParseInvalidMessage
			}
			return nil, err
		}

		if len(line) == 0 {
			// We've hit the end of the header section.
			break
		}

		err = p.headersParsers.parseMsgHeader(msg, line)
		if err != nil {
			p.log.Info().Err(err).Str("line", line).Msg("skip header due to error")
		}
	}

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

// NewSIPStream implements SIP parsing contructor for stream
// should be called per single stream
func (p *Parser) NewSIPStream() *ParserStream {
	return &ParserStream{
		headersParsers: p.headersParsers, // safe as it read only
	}
}

func ParseLine(startLine string) (msg Message, err error) {
	if isRequest(startLine) {
		recipient := Uri{}
		method, sipVersion, err := ParseRequestLine(startLine, &recipient)
		if err != nil {
			return nil, err
		}

		m := NewRequest(method, &recipient)
		m.SipVersion = sipVersion
		return m, nil
	}

	if isResponse(startLine) {
		sipVersion, statusCode, reason, err := ParseStatusLine(startLine)
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
	// Scan full line without buffer
	// If we need to continue then try to grow
	line, err = reader.ReadString('\n')
	if err != nil {
		// if err == io.EOF {
		// 	if len(line) > 0 {
		// 		return line, ErrParseLineNoCRLF
		// 	}

		// 	return line, nil
		// }

		// We may get io.EOF and line till it was read
		return line, err
	}

	// https://www.rfc-editor.org/rfc/rfc3261.html#section-7
	// The start-line, each message-header line, and the empty line MUST be
	// terminated by a carriage-return line-feed sequence (CRLF).  Note that
	// the empty line MUST be present even if the message-body is not.
	lenline := len(line)
	if lenline < 2 {
		return line, ErrParseLineNoCRLF
	}

	if line[lenline-2] != '\r' {
		return line, ErrParseLineNoCRLF
	}

	line = line[:lenline-2]
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

// Heuristic to determine if the given transmission looks like a SIP request.
// It is guaranteed that any RFC3261-compliant request will pass this test,
// but invalid messages may not necessarily be rejected.
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

// Heuristic to determine if the given transmission looks like a SIP response.
// It is guaranteed that any RFC3261-compliant response will pass this test,
// but invalid messages may not necessarily be rejected.
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
func ParseRequestLine(requestLine string, recipient *Uri) (
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
func ParseStatusLine(statusLine string) (
	sipVersion string, statusCode StatusCode, reasonPhrase string, err error) {
	parts := strings.Split(statusLine, " ")
	if len(parts) < 3 {
		err = fmt.Errorf("status line has too few spaces: '%s'", statusLine)
		return
	}

	sipVersion = parts[0]
	statusCodeRaw, err := strconv.ParseUint(parts[1], 10, 16)
	statusCode = StatusCode(statusCodeRaw)
	reasonPhrase = strings.Join(parts[2:], " ")

	return
}
