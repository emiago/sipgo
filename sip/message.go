package sip

import (
	"io"

	uuid "github.com/satori/go.uuid"
)

type MessageHandler func(msg Message)

type RequestMethod string

func (r RequestMethod) String() string { return string(r) }

// StatusCode - response status code: 1xx - 6xx
type StatusCode int

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

type MessageID string

func NextMessageID() MessageID {
	return MessageID(uuid.Must(uuid.NewV4()).String())
}

type Message interface {
	// Start line returns message start line.
	StartLine() string
	// Start line returns message start line.
	StartLineWrite(io.StringWriter)
	// 	// String returns string representation of SIP message in RFC 3261 form.
	String() string
	// String write is same as String but lets you to provide writter and reduce allocations
	StringWrite(io.StringWriter)
	// Short returns short string info about message.
	Short() string
	// SipVersion returns SIP protocol version.

	// Headers returns all message headers.
	Headers() []Header
	// GetHeaders returns slice of headers of the given type.
	GetHeaders(name string) []Header
	// GetHeader returns first header with same name
	GetHeader(name string) Header
	// PrependHeader prepends header to message.
	PrependHeader(header ...Header)
	// AppendHeader appends header to message.
	AppendHeader(header Header)
	// RemoveHeader removes header from message.
	RemoveHeader(name string)
	ReplaceHeader(header Header)
	/* Helper getters for common headers */
	// CallID returns 'Call-ID' header.
	CallID() (*CallID, bool)
	// Via returns the top 'Via' header field.
	Via() (*ViaHeader, bool)
	// From returns 'From' header field.
	From() (*FromHeader, bool)
	// To returns 'To' header field.
	To() (*ToHeader, bool)
	// CSeq returns 'CSeq' header field.
	CSeq() (*CSeq, bool)
	ContentLength() (*ContentLength, bool)

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
}

type MessageData struct {
	// message headers
	headers
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
	msg.body = body
	if body == nil {
		return
	}

	hdr, exists := msg.ContentLength()
	length := ContentLength(len(body))
	if exists {
		if length == *hdr {
			//Skip appending if value is same
			return
		}
		msg.appendHeader("content-length", &length)
		return
	}
	msg.ReplaceHeader(&length)
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
