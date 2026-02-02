package sip

import (
	"io"
)

type MessageHandler func(msg Message)

type RequestMethod string

func (r RequestMethod) String() string { return string(r) }

const (
	// https://datatracker.ietf.org/doc/html/rfc3261#section-21
	StatusTrying            = 100
	StatusRinging           = 180
	StatusCallIsForwarded   = 181
	StatusQueued            = 182
	StatusSessionInProgress = 183

	StatusOK       = 200
	StatusAccepted = 202

	StatusMovedPermanently = 301
	StatusMovedTemporarily = 302
	StatusUseProxy         = 305

	StatusBadRequest                   = 400
	StatusUnauthorized                 = 401
	StatusPaymentRequired              = 402
	StatusForbidden                    = 403
	StatusNotFound                     = 404
	StatusMethodNotAllowed             = 405
	StatusNotAcceptable                = 406
	StatusProxyAuthRequired            = 407
	StatusRequestTimeout               = 408
	StatusConflict                     = 409
	StatusGone                         = 410
	StatusRequestEntityTooLarge        = 413
	StatusRequestURITooLong            = 414
	StatusUnsupportedMediaType         = 415
	StatusRequestedRangeNotSatisfiable = 416
	StatusBadExtension                 = 420
	StatusExtensionRequired            = 421
	StatusIntervalToBrief              = 423
	StatusTemporarilyUnavailable       = 480
	StatusCallTransactionDoesNotExists = 481
	StatusLoopDetected                 = 482
	StatusTooManyHops                  = 483
	StatusAddressIncomplete            = 484
	StatusAmbiguous                    = 485
	StatusBusyHere                     = 486
	StatusRequestTerminated            = 487
	StatusNotAcceptableHere            = 488
	StatusRequestPending               = 491

	StatusInternalServerError = 500
	StatusNotImplemented      = 501
	StatusBadGateway          = 502
	StatusServiceUnavailable  = 503
	StatusGatewayTimeout      = 504
	StatusVersionNotSupported = 505
	StatusMessageTooLarge     = 513

	StatusGlobalBusyEverywhere       = 600
	StatusGlobalDecline              = 603
	StatusGlobalDoesNotExistAnywhere = 604
	StatusGlobalNotAcceptable        = 606
)

// method names are defined here as constants for convenience.
const (
	INVITE    RequestMethod = "INVITE"
	ACK       RequestMethod = "ACK"
	CANCEL    RequestMethod = "CANCEL"
	BYE       RequestMethod = "BYE"
	REGISTER  RequestMethod = "REGISTER"
	OPTIONS   RequestMethod = "OPTIONS"
	SUBSCRIBE RequestMethod = "SUBSCRIBE"
	NOTIFY    RequestMethod = "NOTIFY"
	REFER     RequestMethod = "REFER"
	INFO      RequestMethod = "INFO"
	MESSAGE   RequestMethod = "MESSAGE"
	PRACK     RequestMethod = "PRACK"
	UPDATE    RequestMethod = "UPDATE"
	PUBLISH   RequestMethod = "PUBLISH"
)

type Message interface {
	// String returns string representation of SIP message in RFC 3261 form.
	String() string
	// String write is same as String but lets you to provide writter and reduce allocations
	StringWrite(io.StringWriter)
	// GetHeaders returns slice of headers of the given type.
	GetHeaders(name string) []Header
	// PrependHeader prepends header to message.
	PrependHeader(header ...Header)
	// AppendHeader appends header to message.
	AppendHeader(header Header)
	// CallID returns 'Call-ID' header.
	CallID() *CallIDHeader
	// Via returns the top 'Via' header field.
	Via() *ViaHeader
	// From returns 'From' header field.
	From() *FromHeader
	// To returns 'To' header field.
	To() *ToHeader
	// CSeq returns 'CSeq' header field.
	CSeq() *CSeqHeader
	// Content Length headers
	ContentLength() *ContentLengthHeader

	// Body returns message body.
	Body() []byte
	// SetBody sets message body.
	SetBody(body []byte)

	Transport() string
	SetTransport(tp string)
	Source() string
	SetSource(src string)
	Destination() string
	SetDestination(dest string)

	remoteAddress() Addr
}

type MessageData struct {
	// message headers
	headers
	// Set to 2.0 version by default
	SipVersion string
	body       []byte
	tp         string

	// This is for internal routing
	src  string
	dest string
}

func (msg *MessageData) Body() []byte {
	return msg.body
}

// SetBody sets message body, calculates it length and add 'Content-Length' header.
func (msg *MessageData) SetBody(body []byte) {
	var length ContentLengthHeader
	msg.body = body

	// The Content-Length header field value is used to locate the end of
	//   each SIP message in a stream.  It will always be present when SIP
	//   messages are sent over stream-oriented transports.
	if body == nil {
		length = ContentLengthHeader(0)
	} else {
		length = ContentLengthHeader(len(body))
	}

	hdr := msg.ContentLength()
	if hdr != nil {
		if length == *hdr {
			//Skip appending if value is same
			return
		}
		// msg.appendHeader("content-length", &length)
		msg.ReplaceHeader(&length)
		return
	}

	msg.AppendHeader(&length)
}

func (msg *MessageData) Transport() string {
	return msg.tp
}

func (msg *MessageData) SetTransport(tp string) {
	msg.tp = tp
}

func (msg *MessageData) Source() string {
	return msg.src
}

func (msg *MessageData) SetSource(src string) {
	msg.src = src
}

func (msg *MessageData) Destination() string {
	return msg.dest
}

func (msg *MessageData) SetDestination(dest string) {
	msg.dest = dest
}
