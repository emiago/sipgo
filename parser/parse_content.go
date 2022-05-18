package parser

import (
	"strconv"
	"strings"

	"github.com/emiraganov/sipgo/sip"
)

func parseContentLength(headerName string, headerText string) (
	header sip.Header, err error) {
	var contentLength sip.ContentLength
	var value uint64
	value, err = strconv.ParseUint(strings.TrimSpace(headerText), 10, 32)
	contentLength = sip.ContentLength(value)
	return &contentLength, err
}

func parseContentType(headerName string, headerText string) (headers sip.Header, err error) {
	// var contentType sip.ContentType
	headerText = strings.TrimSpace(headerText)
	contentType := sip.ContentType(headerText)
	return &contentType, nil
}
