package sip

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Here we have collection of headers parsing.
// Some of headers parsing are moved to different files for better maintance

// A HeaderParser is any function that turns raw header data into one or more Header objects.
type HeaderParser func(headerName []byte, headerData string) (Header, error)

type HeadersParser map[string]HeaderParser

type errComaDetected int

func (e errComaDetected) Error() string {
	return "comma detected"
}

// This needs to kept minimalistic in order to avoid overhead of parsing
// Headers compact form
// a	Accept-Contact	draft-ietf-sip-callerprefs	--
// b	Referred-By	-refer-	"by"
// c	Content-Type	RFC 3261
// e	Content-Encoding	RFC 3261
// f	From	RFC 3261
// i	Call-ID	RFC 3261
// k	Supported	RFC 3261	"know"
// l	Content-Length	RFC 3261
// m	Contact	RFC 3261	"moved"
// o	Event	-event-	"occurance"
// r	Refer-To	-refer-
// s	Subject	RFC 3261
// t	To	RFC 3261
// u	Allow-Events	-events-	"understand"
// v	Via	RFC 3261
var headersParsers = HeadersParser{
	"c":              headerParserContentType,
	"content-type":   headerParserContentType,
	"f":              headerParserFrom,
	"from":           headerParserFrom,
	"to":             headerParserTo,
	"t":              headerParserTo,
	"contact":        headerParserContact,
	"m":              headerParserContact,
	"i":              headerParserCallId,
	"call-id":        headerParserCallId,
	"cseq":           headerParserCSeq,
	"via":            headerParserVia,
	"v":              headerParserVia,
	"max-forwards":   headerParserMaxForwards,
	"content-length": headerParserContentLength,
	"l":              headerParserContentLength,
	"route":          headerParserRoute,
	"record-route":   headerParserRecordRoute,
	"refer-to":       headerParserReferTo,
	"referred-by":    headerParserReferredBy,
}

// DefaultHeadersParser returns minimal version header parser.
// It can be extended or overwritten.
func DefaultHeadersParser() map[string]HeaderParser {
	return headersParsers
}

// ParseHeader parses a SIP header from the line and appends it to out.
func (headersParser HeadersParser) ParseHeader(out []Header, line []byte) ([]Header, error) {
	colonIdx := bytes.IndexByte(line, ':')
	if colonIdx == -1 {
		return out, fmt.Errorf("field name with no value in header: %q", line)
	}

	fieldName := bytes.TrimSpace(line[:colonIdx])
	lowerFieldName := headerToLower(fieldName)
	fieldValue := bytes.TrimSpace(line[colonIdx+1:])

	headerParser, ok := headersParser[string(lowerFieldName)]
	if !ok {
		// We have no registered parser for this header type,
		// so we encapsulate the header data in a GenericHeader struct.
		// We do only forwarding on this with trimmed space. Validation and parsing is required by user
		h := NewHeader(string(fieldName), string(fieldValue))
		out = append(out, h)
		return out, nil
	}

	fieldText := string(fieldValue)
	// Support comma separated values
	for {
		// We have a registered parser for this header type - use it.
		// headerParser should detect comma (,) and return as error
		h, err := headerParser(lowerFieldName, fieldText)
		if err == nil {
			out = append(out, h)
			return out, nil
		}

		commaErr, ok := err.(errComaDetected)
		if !ok {
			return out, err
		}
		// Ok we detected we have comma in header value
		out = append(out, h)
		fieldText = fieldText[commaErr+1:]
	}
}

func headerParserCallId(headerName []byte, headerText string) (header Header, err error) {
	var callId CallIDHeader
	return &callId, parseCallIdHeader(headerText, &callId)
}

// parseCallIdHeader parses Call-ID header
func parseCallIdHeader(headerText string, callId *CallIDHeader) error {
	headerText = strings.TrimSpace(headerText)
	if len(headerText) == 0 {
		return fmt.Errorf("empty Call-ID body")
	}

	*callId = CallIDHeader(headerText)
	return nil
}

func headerParserMaxForwards(headerName []byte, headerText string) (header Header, err error) {
	var maxfwd MaxForwardsHeader
	return &maxfwd, parseMaxForwardsHeader(headerText, &maxfwd)
}

// parseMaxForwardsHeader parses MaxForward header
func parseMaxForwardsHeader(headerText string, maxfwd *MaxForwardsHeader) error {
	val, err := strconv.ParseUint(headerText, 10, 32)
	*maxfwd = MaxForwardsHeader(val)
	return err
}

func headerParserCSeq(headerName []byte, headerText string) (headers Header, err error) {
	var cseq CSeqHeader
	return &cseq, parseCSeqHeader(headerText, &cseq)
}

// parseCSeqHeader parses CSeq header
func parseCSeqHeader(headerText string, cseq *CSeqHeader) error {
	ind := strings.IndexAny(headerText, abnf)
	if ind < 1 || len(headerText)-ind < 2 {
		return fmt.Errorf("CSeq field should have precisely one whitespace section: '%s'", headerText)
	}

	seqno, err := strconv.ParseUint(headerText[:ind], 10, 32)
	if err != nil {
		return err
	}

	if seqno > maxCseq {
		return fmt.Errorf("invalid CSeq %d: exceeds maximum permitted value "+"2**31 - 1", seqno)
	}

	cseq.SeqNo = uint32(seqno)
	cseq.MethodName = RequestMethod(headerText[ind+1:])
	return nil
}

func headerParserContentLength(headerName []byte, headerText string) (header Header, err error) {
	var contentLength ContentLengthHeader
	return &contentLength, parseContentLengthHeader(headerText, &contentLength)
}

// parseContentLengthHeader parses ContentLength header
func parseContentLengthHeader(headerText string, contentLength *ContentLengthHeader) error {
	value, err := strconv.ParseUint(strings.TrimSpace(headerText), 10, 32)
	*contentLength = ContentLengthHeader(value)
	return err
}

// headerParserContentType parses ContentType header
func headerParserContentType(headerName []byte, headerText string) (headers Header, err error) {
	var contentType ContentTypeHeader
	return &contentType, parseContentTypeHeader(headerText, &contentType)
}

func parseContentTypeHeader(headerText string, contentType *ContentTypeHeader) error {
	headerText = strings.TrimSpace(headerText)
	if len(headerText) == 0 {
		return fmt.Errorf("empty Content-Type body")
	}

	*contentType = ContentTypeHeader(headerText)
	return nil
}
