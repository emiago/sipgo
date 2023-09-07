package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/emiago/sipgo/sip"
)

// Here we have collection of headers parsing.
// Some of headers parsing are moved to different files for better maintance

// A HeaderParser is any function that turns raw header data into one or more Header objects.
type HeaderParser func(headerName string, headerData string) (sip.Header, error)

type mapHeadersParser map[string]HeaderParser

type errComaDetected int

func (e errComaDetected) Error() string {
	return "comma detected"
}

// This needs to kept minimalistic in order to avoid overhead of parsing
var headersParsers = mapHeadersParser{
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

// DefaultHeadersParser returns minimal version header parser.
// It can be extended or overwritten. Removing some defaults can break SIP functionality
//
// NOTE this API call may change
func DefaultHeadersParser() map[string]HeaderParser {
	return headersParsers
}

// parseMsgHeader will append any parsed header
// in case comma seperated values it will add them as new in case comma is detected
func (headersParser mapHeadersParser) parseMsgHeader(msg sip.Message, headerText string) (err error) {
	// p.log.Tracef("parsing header \"%s\"", headerText)

	colonIdx := strings.Index(headerText, ":")
	if colonIdx == -1 {
		err = fmt.Errorf("field name with no value in header: %s", headerText)
		return
	}

	fieldName := strings.TrimSpace(headerText[:colonIdx])
	lowerFieldName := sip.HeaderToLower(fieldName)
	fieldText := strings.TrimSpace(headerText[colonIdx+1:])

	headerParser, ok := headersParsers[lowerFieldName]
	if !ok {
		// We have no registered parser for this header type,
		// so we encapsulate the header data in a GenericHeader struct.

		// TODO Should we check for comma here as well ??
		header := sip.NewHeader(fieldName, fieldText)
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

// parseCallId generates sip.CallIDHeader
func parseCallId(headerName string, headerText string) (
	header sip.Header, err error) {
	headerText = strings.TrimSpace(headerText)

	if len(headerText) == 0 {
		err = fmt.Errorf("empty Call-ID body")
		return
	}

	var callId = sip.CallIDHeader(headerText)

	return &callId, nil
}

// parseCallId generates sip.MaxForwardsHeader
func parseMaxForwards(headerName string, headerText string) (header sip.Header, err error) {
	val, err := strconv.ParseUint(headerText, 10, 32)
	if err != nil {
		return nil, err
	}

	maxfwd := sip.MaxForwardsHeader(val)
	return &maxfwd, nil
}

// parseCSeq generates sip.CSeqHeader
func parseCSeq(headerName string, headerText string) (
	headers sip.Header, err error) {
	var cseq sip.CSeqHeader
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
	cseq.MethodName = sip.RequestMethod(headerText[ind+1:])
	return &cseq, nil
}

// parseContentLength generates sip.ContentLengthHeader
func parseContentLength(headerName string, headerText string) (header sip.Header, err error) {
	var contentLength sip.ContentLengthHeader
	var value uint64
	value, err = strconv.ParseUint(strings.TrimSpace(headerText), 10, 32)
	contentLength = sip.ContentLengthHeader(value)
	return &contentLength, err
}

// parseContentLength generates sip.ContentTypeHeader
func parseContentType(headerName string, headerText string) (headers sip.Header, err error) {
	// var contentType sip.ContentType
	headerText = strings.TrimSpace(headerText)
	contentType := sip.ContentTypeHeader(headerText)
	return &contentType, nil
}
