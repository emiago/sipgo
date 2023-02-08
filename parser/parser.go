// Originaly forked from github.com/StefanKopieczek/gossip by @StefanKopieczek
package parser

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/emiago/sipgo/sip"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// The whitespace characters recognised by the Augmented Backus-Naur Form syntax
// that SIP uses (RFC 3261 S.25).
const abnfWs = " \t"

// The maximum permissible CSeq number in a SIP message (2**31 - 1).
// C.f. RFC 3261 S. 8.1.1.5.
const maxCseq = 2147483647

// A HeaderParser is any function that turns raw header data into one or more Header objects.
type HeaderParser func(headerName string, headerData string) (sip.Header, error)

// This needs to kept minimalistic in order to avoid overhead of parsing
var headersParsers = map[string]HeaderParser{
	"to":             parseToAddressHeader,
	"t":              parseToAddressHeader,
	"from":           parseFromAddressHeader,
	"f":              parseFromAddressHeader,
	"contact":        parseContactAddressHeader,
	"m":              parseContactAddressHeader,
	"call-id":        parseCallId,
	"i":              parseCallId,
	"cseq":           parseCSeq,
	"via":            parseViaHeader,
	"v":              parseViaHeader,
	"max-forwards":   parseMaxForwards,
	"content-length": parseContentLength,
	"l":              parseContentLength,
	"content-type":   parseContentType,
	"c":              parseContentType,
	"route":          parseRouteHeader,
	"record-route":   parseRecordRouteHeader,
}

var bufReader = sync.Pool{
	New: func() interface{} {
		// The Pool's New function should generally only return pointer
		// types, since a pointer can be put into the return interface
		// value without an allocation:
		return new(bytes.Buffer)
	},
}

// The buffer size of the parser input channel.
// SipParser is interface for decoding full message into sip message
type SIPParser interface {
	Parse(data []byte) (sip.Message, error)
}

// SIPParserStreamed is parser that allows streamed data for parsing
type SIPParserStreamed interface {
	Write(data []byte) (int, error)
	Output() chan sip.Message
}

// Parse a SIP message by creating a parser on the fly.
func ParseMessage(msgData []byte) (sip.Message, error) {
	parser := NewParser()
	return parser.Parse(msgData)
}

type Parser struct {
	log zerolog.Logger
}

// Create a new Parser.
func NewParser() *Parser {
	p := &Parser{
		log: log.Logger,
	}
	return p
}

func (p *Parser) SetLogger(l zerolog.Logger) {
	p.log = l
}

// Parse converts data to sip message. Buffer must contain full sip message
func (p *Parser) Parse(data []byte) (msg sip.Message, err error) {
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
			return nil, err
		}

		if len(line) == 0 {
			// We've hit the end of the header section.
			break
		}

		header, err := p.ParseHeader(line)
		if err == nil {
			msg.AppendHeader(header)
		} else {
			p.log.Info().Err(err).Str("line", line).Msg("skip header due to error")
		}
	}

	contentLength := getBodyLength(data)

	if contentLength <= 0 {
		return msg, nil
	}

	// p.log.Debugf("%s reads body with length = %d bytes", p, contentLength)
	body := make([]byte, contentLength)
	total, err := nextChunk(reader, body)
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

	if len(bytes.TrimSpace(body)) > 0 {
		msg.SetBody(body)
	}
	return msg, nil
}

func ParseLine(startLine string) (msg sip.Message, err error) {
	if isRequest(startLine) {
		recipient := sip.Uri{}
		method, sipVersion, err := ParseRequestLine(startLine, &recipient)
		if err != nil {
			return nil, err
		}

		msg = sip.NewRequest(method, &recipient, sipVersion)
		return msg, nil
	}

	if isResponse(startLine) {
		sipVersion, statusCode, reason, err := ParseStatusLine(startLine)
		if err != nil {
			return nil, err
		}

		msg = sip.NewResponse(sipVersion, statusCode, reason)
		return msg, nil
	}
	return nil, fmt.Errorf("transmission beginning '%s' is not a SIP message", startLine)
}

func nextLineFast(reader *bytes.Buffer) (line []byte, err error) {
	line, err = reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	lenline := len(line)
	if lenline > 1 && line[lenline-2] == '\r' {
		line = line[:lenline-2]
		return line, err

	}
	return nil, fmt.Errorf("fail to read")
}

func nextLine(reader *bytes.Buffer) (line string, err error) {
	// Scan full line without buffer
	// If we need to continue then try to grow
	line, err = reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			return "", nil
		}

		return "", err
	}

	lenline := len(line)
	// https://www.rfc-editor.org/rfc/rfc3261.html#section-7
	// The start-line, each message-header line, and the empty line MUST be
	// terminated by a carriage-return line-feed sequence (CRLF).  Note that
	// the empty line MUST be present even if the message-body is not.
	if lenline > 1 && line[lenline-2] == '\r' {
		line = line[:lenline-2]
		return line, nil
	}

	return line, nil

	// This is now slow part
	var buffer strings.Builder
	var data string
	var b byte
	buffer.WriteString(line)

	for {
		// bufio.ScanLines()
		data, err = reader.ReadString('\r')
		if err != nil {
			if lenline > 0 && errors.Is(err, io.EOF) {
				// Non found \r means we only have normal breaks
				return line, nil
			}

			return "", err
		}

		buffer.WriteString(data)

		b, err = reader.ReadByte()
		if err != nil {
			return "", err
		}

		buffer.WriteByte(b)
		if b != '\n' {
			continue
		}

		line = buffer.String()
		// STILL NOT SURE DO WE NEED TO DO THIS
		if strings.Contains(abnfWs, line[0:1]) {
			buffer.WriteString(" ")
			continue
		}

		line = line[:len(line)-2]
		return line, nil
	}
}

func nextChunk(reader *bytes.Buffer, buf []byte) (n int, err error) {
	var read int
	total := 0
	for total < len(buf) {
		read, err = reader.Read(buf[total:])
		total += read
		if err != nil {
			return
		}
	}
	return total, nil
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

	return sip.UriIsSIP(part2[:3])
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

	return sip.UriIsSIP(startLine[:3])
}

// Parse the first line of a SIP request, e.g:
//
//	INVITE bob@example.com SIP/2.0
//	REGISTER jane@telco.com SIP/1.0
func ParseRequestLine(requestLine string, recipient *sip.Uri) (
	method sip.RequestMethod, sipVersion string, err error) {
	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 {
		err = fmt.Errorf("request line should have 2 spaces: '%s'", requestLine)
		return
	}

	method = sip.RequestMethod(strings.ToUpper(parts[0]))
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
	sipVersion string, statusCode sip.StatusCode, reasonPhrase string, err error) {
	parts := strings.Split(statusLine, " ")
	if len(parts) < 3 {
		err = fmt.Errorf("status line has too few spaces: '%s'", statusLine)
		return
	}

	sipVersion = parts[0]
	statusCodeRaw, err := strconv.ParseUint(parts[1], 10, 16)
	statusCode = sip.StatusCode(statusCodeRaw)
	reasonPhrase = strings.Join(parts[2:], " ")

	return
}

func (p *Parser) ParseHeader(headerText string) (header sip.Header, err error) {
	// p.log.Tracef("parsing header \"%s\"", headerText)

	colonIdx := strings.Index(headerText, ":")
	if colonIdx == -1 {
		err = fmt.Errorf("field name with no value in header: %s", headerText)
		return
	}

	fieldName := strings.TrimSpace(headerText[:colonIdx])
	lowerFieldName := sip.HeaderToLower(fieldName)
	fieldText := strings.TrimSpace(headerText[colonIdx+1:])
	if headerParser, ok := headersParsers[lowerFieldName]; ok {
		// We have a registered parser for this header type - use it.
		// header, err = headerParser(lowerFieldName, fieldText)
		header, err = headerParser(lowerFieldName, fieldText)
	} else {
		// We have no registered parser for this header type,
		// so we encapsulate the header data in a GenericHeader struct.
		// p.log.Tracef("no parser for header type %s", fieldName)

		header = &sip.GenericHeader{
			HeaderName: fieldName,
			Contents:   fieldText,
		}
	}

	return
}

// Parse a string representation of a Call-ID header, returning a slice of at most one CallID.
func parseCallId(headerName string, headerText string) (
	header sip.Header, err error) {
	headerText = strings.TrimSpace(headerText)

	// if strings.ContainsAny(string(callId), abnfWs) {
	// 	err = fmt.Errorf("unexpected whitespace in CallID header body '%s'", headerText)
	// 	return
	// }
	// if strings.Contains(string(callId), ";") {
	// 	err = fmt.Errorf("unexpected semicolon in CallID header body '%s'", headerText)
	// 	return
	// }
	if len(headerText) == 0 {
		err = fmt.Errorf("empty Call-ID body")
		return
	}

	var callId = sip.CallIDHeader(headerText)

	return &callId, nil
}

func parseMaxForwards(headerName string, headerText string) (header sip.Header, err error) {
	val, err := strconv.ParseUint(headerText, 10, 32)
	if err != nil {
		return nil, err
	}

	maxfwd := sip.MaxForwardsHeader(val)
	return &maxfwd, nil
}
