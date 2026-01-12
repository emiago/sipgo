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
	ErrMessageTooLarge         = errors.New("Message exceeds ParseMaxMessageLength")

	defaultParser = NewParser()
)

var (
	ParseMaxMessageLength = 65535
)

func ParseMessage(msgData []byte) (Message, error) {
	return defaultParser.ParseSIP(msgData)
}

// Parser is implementation of SIPParser
// It is optimized with faster header parsing
type Parser struct {
	// HeadersParsers uses default list of headers to be parsed. Smaller list parser will be faster
	headersParsers HeadersParser

	MaxMessageLength int
}

// ParserOption are addition option for NewParser. Check WithParser...
type ParserOption func(p *Parser)

// Create a new Parser.
func NewParser(options ...ParserOption) *Parser {
	p := &Parser{
		headersParsers:   DefaultHeadersParser(),
		MaxMessageLength: ParseMaxMessageLength,
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

// ParseHeaders parses all headers of a SIP message. It returns the number of bytes read.
// Data must contain a full SIP message header section, including double CRLF (\r\n).
//
// If the message is cut in the middle of a header or the first line, io.ErrUnexpectedEOF is returned.
// It may return an error wrapping ErrParseLineNoCRLF if one of the header lines is malformed,
// or if there's no CRLF (\r\n) delimiter after headers.
func (p *Parser) ParseHeaders(data []byte, stream bool) (Message, int, error) {
	msg, _, n, err := p.parseHeaders(data, stream)
	return msg, n, err
}

func (p *Parser) parseHeaders(data []byte, stream bool) (Message, *ContentLengthHeader, int, error) {
	msg, total, err := p.parseStartLine(data, stream)
	if err != nil {
		return msg, nil, total, err
	}
	data = data[total:]

	contentLength, n, err := p.parseHeadersOnly(msg, data)
	total += n
	return msg, contentLength, total, err
}

func (p *Parser) parseStartLine(data []byte, stream bool) (Message, int, error) {
	var (
		total   int
		skipped bool
	)

	if stream {
		// RFC 3261 - 7.5.
		// Implementations processing SIP messages over stream-oriented
		// transports MUST ignore any CRLF appearing before the start-line.
		for len(data) >= 2 && data[0] == '\r' && data[1] == '\n' {
			data = data[2:]
			total += 2
			skipped = true
		}
	}

	startLine, n, err := nextLine(data)
	if err != nil {
		if err == io.EOF && skipped {
			return nil, total, io.ErrUnexpectedEOF
		}
		return nil, total, err
	}
	total += n

	msg, err := parseLine(string(startLine))
	if err != nil {
		return nil, total, err
	}
	return msg, total, nil
}

var errParseNoMoreHeaders = errors.New("no more headers")

func (p *Parser) parseNextHeader(out []Header, data []byte) ([]Header, int, error) {
	line, n, err := nextLine(data)
	if err != nil {
		if err == io.EOF {
			return out, 0, io.ErrUnexpectedEOF
		}

		// NOTE: n > 0  but we return 0 as we need to read more bytes
		return out, 0, err
	}

	// Advance only after a successful parse.
	if len(line) == 0 {
		// We've hit the end of the header section.
		return out, n, errParseNoMoreHeaders
	}
	out, err = p.headersParsers.ParseHeader(out, line)
	if err != nil {
		// We might not need to return n here?
		return out, n, err
	}
	return out, n, nil
}

func (p *Parser) parseHeadersOnly(msg Message, data []byte) (*ContentLengthHeader, int, error) {
	var (
		total, n      int
		headerBuf     []Header
		contentLength *ContentLengthHeader
		err           error
	)
	for {
		headerBuf, n, err = p.parseNextHeader(headerBuf[:0], data)
		data = data[n:]
		total += n
		for _, h := range headerBuf {
			switch h := h.(type) {
			case *ContentLengthHeader:
				contentLength = h
			}
			msg.AppendHeader(h)
		}
		if err == errParseNoMoreHeaders {
			return contentLength, total, nil
		}
		if err != nil {
			return contentLength, total, err
		}
	}
}

// Parse data to a SIP message. It returns the number of bytes read. Data must contain a full SIP message.
//
// If the message is cut in the middle of a header or a first line, io.ErrUnexpectedEOF is returned.
// It may return an error wrapping ErrParseLineNoCRLF if one of the header lines is malformed,
// or if there's no CRLF (\r\n) delimiter after headers.
//
// In case the end of the body cannot be determined, or the body is incomplete,
// an ErrParseReadBodyIncomplete is returned.
func (p *Parser) Parse(data []byte, stream bool) (Message, int, error) {
	if len(data) > p.MaxMessageLength {
		return nil, 0, ErrMessageTooLarge
	}
	msg, contentLength, total, err := p.parseHeaders(data, stream)
	if err != nil {
		return msg, total, err
	}
	data = data[total:]
	bodySize := -1
	if contentLength != nil {
		bodySize = int(*contentLength)
	} else if !stream {
		bodySize = len(data)
	}
	if bodySize < 0 {
		// RFC 3261 - 7.5.
		// The Content-Length header field value is used to locate the end of
		// each SIP message in a stream. It will always be present when SIP
		// messages are sent over stream-oriented transports.
		return msg, total, ErrParseReadBodyIncomplete
	}
	if bodySize == 0 {
		return msg, total, nil
	}
	body := make([]byte, bodySize)
	n := copy(body, data)
	total += n
	msg.SetBody(body)
	// RFC 3261 - 18.3.
	if n != bodySize {
		return msg, total, ErrParseReadBodyIncomplete
	}
	return msg, total, nil
}

// ParseSIP converts data to sip message. Buffer must contain full sip message
func (p *Parser) ParseSIP(data []byte) (msg Message, err error) {
	msg, _, err = p.Parse(data, false)
	if err == io.ErrUnexpectedEOF {
		err = ErrParseEOF
	}
	return msg, err
}

// NewSIPStream implements SIP parsing contructor for IO that stream SIP message
// It should be created per each stream
func (p *Parser) NewSIPStream() *ParserStream {
	if p == nil {
		p = NewParser()
	}
	return &ParserStream{
		p: p, // safe as it read only
	}
}

func parseLine(startLine string) (msg Message, err error) {
	if parts, ok := split3(startLine); ok {
		if isRequest(parts) {
			recipient := Uri{}
			method, sipVersion, err := parseRequestLine(parts, &recipient)
			if err != nil {
				return nil, err
			}

			m := NewRequest(method, recipient)
			m.SipVersion = sipVersion
			return m, nil
		}
		if isResponse(parts) {
			sipVersion, statusCode, reason, err := parseStatusLine(parts)
			if err != nil {
				return nil, err
			}

			m := NewResponse(statusCode, reason)
			m.SipVersion = sipVersion
			return m, nil
		}
	}
	return nil, fmt.Errorf("transmission beginning '%s' is not a SIP message", startLine)
}

// nextLine reads the next line of a SIP message and the number of bytes read.
//
// It returns io.ErrUnexpectedEOF is there's no CRLF (\r\n) in the data.
// If there's a CR (\r) which is not followed by LF (\n), a ErrParseLineNoCRLF is returned.
// As a special case, it returns io.EOF if data is empty.
func nextLine(data []byte) ([]byte, int, error) {
	if len(data) == 0 {
		return nil, 0, io.EOF
	}
	// https://www.rfc-editor.org/rfc/rfc3261.html#section-7
	// The start-line, each message-header line, and the empty line MUST be
	// terminated by a carriage-return line-feed sequence (CRLF).  Note that
	// the empty line MUST be present even if the message-body is not.

	// Lines could be multiline as well so this is also acceptable
	// TO :\n
	// sip:vivekg@chair-dnrc.example.com ;   tag    = 1918181833n
	i := bytes.IndexByte(data, '\r')
	if i < 0 {
		return data, len(data), io.ErrUnexpectedEOF
	}
	line := data[:i]
	if i+1 >= len(data) {
		return line, i + 1, io.ErrUnexpectedEOF
	}
	if data[i+1] != '\n' {
		return line, i + 1, ErrParseLineNoCRLF
	}
	return line, i + 2, nil
}

// detect is request by spaces
func isRequest(parts [3]string) bool {
	// SIP request lines contain precisely two spaces.
	part2 := parts[2]
	if len(part2) < 3 {
		return false
	}
	i := strings.IndexByte(part2, ' ')
	if i >= 0 {
		return false
	}
	return UriIsSIP(part2[:3])
}

// Detect is response by spaces
func isResponse(parts [3]string) bool {
	part0 := parts[0]
	if len(part0) < 3 {
		return false
	}
	return UriIsSIP(part0[:3])
}

func split3(s string) (parts [3]string, ok bool) {
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return
	}
	parts[0] = s[:i]
	s = s[i+1:]

	i = strings.IndexByte(s, ' ')
	if i < 0 {
		return
	}
	parts[1] = s[:i]
	s = s[i+1:]
	parts[2] = s
	return parts, true
}

// Parse the first line of a SIP request, e.g:
//
//	INVITE bob@example.com SIP/2.0
//	REGISTER jane@telco.com SIP/1.0
func parseRequestLine(parts [3]string, recipient *Uri) (method RequestMethod, sipVersion string, err error) {
	method = RequestMethod(strings.ToUpper(parts[0]))
	err = ParseUri(parts[1], recipient)
	sipVersion = parts[2]

	if recipient.Wildcard {
		err = fmt.Errorf("wildcard URI '*' not permitted in request line")
		return
	}

	return
}

// Parse the first line of a SIP response, e.g:
//
//	SIP/2.0 200 OK
//	SIP/1.0 403 Forbidden
func parseStatusLine(parts [3]string) (sipVersion string, statusCode int, reasonPhrase string, err error) {
	sipVersion = parts[0]
	statusCodeRaw, err := strconv.ParseUint(parts[1], 10, 16)
	statusCode = int(statusCodeRaw)
	reasonPhrase = parts[2]
	return
}
