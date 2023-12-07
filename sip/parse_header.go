package sip

import (
	"fmt"
	"strconv"
	"strings"
)

// Here we have collection of headers parsing.
// Some of headers parsing are moved to different files for better maintance

// A HeaderParser is any function that turns raw header data into one or more Header objects.
type HeaderParser func(headerName string, headerData string) (Header, error)

type mapHeadersParser map[string]HeaderParser

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
var headersParsers = mapHeadersParser{
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
}

// DefaultHeadersParser returns minimal version header parser.
// It can be extended or overwritten. Removing some defaults can break SIP functionality
//
// NOTE this API call may change
func DefaultHeadersParser() map[string]HeaderParser {
	return headersParsers
}

// parseMsgHeader will append any parsed header
// in case comma seperated values it will add them as new in case comma is detected
func (headersParser mapHeadersParser) parseMsgHeader(msg Message, headerText string) (err error) {
	// p.log.Tracef("parsing header \"%s\"", headerText)

	colonIdx := strings.Index(headerText, ":")
	if colonIdx == -1 {
		err = fmt.Errorf("field name with no value in header: %s", headerText)
		return
	}

	fieldName := strings.TrimSpace(headerText[:colonIdx])
	lowerFieldName := HeaderToLower(fieldName)
	fieldText := strings.TrimSpace(headerText[colonIdx+1:])

	headerParser, ok := headersParsers[lowerFieldName]
	if !ok {
		// We have no registered parser for this header type,
		// so we encapsulate the header data in a GenericHeader struct.

		// TODO Should we check for comma here as well ??
		header := NewHeader(fieldName, fieldText)
		msg.AppendHeader(header)
		return nil
	}

	// Support comma seperated value
	for {
		// We have a registered parser for this header type - use it.
		// headerParser should detect comma (,) and return as error
		header, err := headerParser(lowerFieldName, fieldText)

		// Mostly we will run with no error
		if err == nil {
			msg.AppendHeader(header)
			return nil
		}

		commaErr, ok := err.(errComaDetected)
		if !ok {
			return err
		}

		// Ok we detected we have comma in header value
		msg.AppendHeader(header)
		fieldText = fieldText[commaErr+1:]
	}
}

func parseHeaderErrorNoComma(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(errComaDetected)
	return ok
}

func headerParserGeneric(lowHeaderName string) HeaderParser {
	return func(headerName, headerData string) (Header, error) {
		header := NewHeader(lowHeaderName, headerData)
		return header, nil
	}
}

// headerParserCallId generates CallIDHeader
func headerParserCallId(headerName string, headerText string) (header Header, err error) {
	headerText = strings.TrimSpace(headerText)

	if len(headerText) == 0 {
		err = fmt.Errorf("empty Call-ID body")
		return
	}

	var callId = CallIDHeader(headerText)

	return &callId, nil
}

// parseCallId generates MaxForwardsHeader
func headerParserMaxForwards(headerName string, headerText string) (header Header, err error) {
	val, err := strconv.ParseUint(headerText, 10, 32)
	if err != nil {
		return nil, err
	}

	maxfwd := MaxForwardsHeader(val)
	return &maxfwd, nil
}

// headerParserCSeq generates CSeqHeader
func headerParserCSeq(headerName string, headerText string) (
	headers Header, err error) {
	var cseq CSeqHeader
	ind := strings.IndexAny(headerText, abnfWs)
	if ind < 1 || len(headerText)-ind < 2 {
		err = fmt.Errorf(
			"CSeq field should have precisely one whitespace section: '%s'",
			headerText,
		)
		return
	}

	var seqno uint64
	seqno, err = strconv.ParseUint(headerText[:ind], 10, 32)
	if err != nil {
		return
	}

	if seqno > maxCseq {
		err = fmt.Errorf("invalid CSeq %d: exceeds maximum permitted value "+
			"2**31 - 1", seqno)
		return
	}

	cseq.SeqNo = uint32(seqno)
	cseq.MethodName = RequestMethod(headerText[ind+1:])
	return &cseq, nil
}

// headerParserContentLength generates ContentLengthHeader
func headerParserContentLength(headerName string, headerText string) (header Header, err error) {
	var contentLength ContentLengthHeader
	var value uint64
	value, err = strconv.ParseUint(strings.TrimSpace(headerText), 10, 32)
	contentLength = ContentLengthHeader(value)
	return &contentLength, err
}

// parseContentLength generates ContentTypeHeader
func headerParserContentType(headerName string, headerText string) (headers Header, err error) {
	// var contentType ContentType
	headerText = strings.TrimSpace(headerText)
	contentType := ContentTypeHeader(headerText)
	return &contentType, nil
}
