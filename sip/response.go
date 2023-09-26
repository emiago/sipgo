package sip

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	uuid "github.com/satori/go.uuid"
)

// Response RFC 3261 - 7.2.
type Response struct {
	MessageData

	Reason     string     // e.g. "200 OK"
	StatusCode StatusCode // e.g. 200
}

// NewResponse creates base structure of response.
func NewResponse(
	statusCode StatusCode,
	reason string,
) *Response {
	res := &Response{}
	res.SipVersion = "SIP/2.0"
	res.headers = headers{
		// headers:     make(map[string]Header),
		headerOrder: make([]Header, 0),
	}
	res.StatusCode = statusCode
	res.Reason = reason
	res.body = nil

	return res
}

// Short is textual short version of response
func (res *Response) Short() string {
	if res == nil {
		return "<nil>"
	}

	return fmt.Sprintf("response status=%d reason=%s transport=%s source=%s",
		res.StatusCode,
		res.Reason,
		res.Transport(),
		res.Source(),
	)
}

// StartLine returns Response Status Line - RFC 2361 7.2.
func (res *Response) StartLine() string {
	var buffer strings.Builder
	// Every SIP response starts with a Status Line - RFC 2361 7.2.
	res.StartLineWrite(&buffer)
	return buffer.String()
}

func (res *Response) StartLineWrite(buffer io.StringWriter) {
	statusCode := strconv.Itoa(int(res.StatusCode))
	buffer.WriteString(res.SipVersion)
	buffer.WriteString(" ")
	buffer.WriteString(statusCode)
	buffer.WriteString(" ")
	buffer.WriteString(res.Reason)
}

func (res *Response) String() string {
	var buffer strings.Builder
	res.StringWrite(&buffer)
	return buffer.String()
}

func (res *Response) StringWrite(buffer io.StringWriter) {
	res.StartLineWrite(buffer)
	buffer.WriteString("\r\n")
	// Write the headers.
	res.headers.StringWrite(buffer)
	// Empty line
	buffer.WriteString("\r\n")
	// message body
	if res.body != nil {
		// buffer.WriteString("\r\n")
		buffer.WriteString(string(res.body))
		return
	}
	// buffer.WriteString("\r\n")
}

func (res *Response) Clone() *Response {
	return cloneResponse(res)
}

func (res *Response) IsProvisional() bool {
	return res.StatusCode < 200
}

func (res *Response) IsSuccess() bool {
	return res.StatusCode >= 200 && res.StatusCode < 300
}

func (res *Response) IsRedirection() bool {
	return res.StatusCode >= 300 && res.StatusCode < 400
}

func (res *Response) IsClientError() bool {
	return res.StatusCode >= 400 && res.StatusCode < 500
}

func (res *Response) IsServerError() bool {
	return res.StatusCode >= 500 && res.StatusCode < 600
}

func (res *Response) IsGlobalError() bool {
	return res.StatusCode >= 600
}

func (res *Response) IsAck() bool {
	if cseq, ok := res.CSeq(); ok {
		return cseq.MethodName == ACK
	}
	return false
}

func (res *Response) IsCancel() bool {
	if cseq, ok := res.CSeq(); ok {
		return cseq.MethodName == CANCEL
	}
	return false
}

func (res *Response) Transport() string {
	if tp := res.MessageData.Transport(); tp != "" {
		return tp
	}

	var tp string
	if viaHop, ok := res.Via(); ok && viaHop.Transport != "" {
		tp = viaHop.Transport
	} else {
		tp = DefaultProtocol
	}

	return tp
}

func (res *Response) Destination() string {
	if dest := res.MessageData.Destination(); dest != "" {
		return dest
	}

	viaHop, ok := res.Via()
	if !ok {
		return ""
	}

	var (
		host string
		port int
	)

	host = viaHop.Host
	if viaHop.Port > 0 {
		port = viaHop.Port
	} else {
		port = int(DefaultPort(res.Transport()))
	}

	if viaHop.Params != nil {
		if received, ok := viaHop.Params.Get("received"); ok && received != "" {
			host = received
		}
		if rport, ok := viaHop.Params.Get("rport"); ok && rport != "" {
			if p, err := strconv.Atoi(rport); err == nil {
				port = p
			}
		}
	}

	return fmt.Sprintf("%v:%v", host, port)
}

// RFC 3261 - 8.2.6
func NewResponseFromRequest(
	req *Request,
	statusCode StatusCode,
	reason string,
	body []byte,
) *Response {
	res := NewResponse(
		statusCode,
		reason,
	)
	res.SipVersion = req.SipVersion
	CopyHeaders("Record-Route", req, res)
	CopyHeaders("Via", req, res)
	if h, _ := req.From(); h != nil {
		res.AppendHeader(h.headerClone())
	}

	if h, _ := req.To(); h != nil {
		res.AppendHeader(h.headerClone())
	}

	if h, _ := req.CallID(); h != nil {
		res.AppendHeader(h.headerClone())
	}

	if h, _ := req.CSeq(); h != nil {
		res.AppendHeader(h.headerClone())
	}

	// 8.2.6.2 Headers and Tags
	// the response (with the exception of the 100 (Trying) response, in
	// which a tag MAY be present). This serves to identify the UAS that is
	// responding, possibly resulting in a component of a dialog ID. The
	// same tag MUST be used for all responses to that request, both final
	// and provisional (again excepting the 100 (Trying)). Procedures for
	// the generation of tags are defined in Section 19.3.
	switch statusCode {
	case 100:
		CopyHeaders("Timestamp", req, res)
	default:
		if _, ok := res.to.Params["tag"]; !ok {
			uuid, _ := uuid.NewV4()
			res.to.Params["tag"] = uuid.String()
		}
	}

	res.SetBody(body)
	res.SetTransport(req.Transport())
	res.SetSource(req.Destination())
	res.SetDestination(req.Source())

	return res
}

// NewSDPResponseFromRequest is wrapper for 200 response with SDP body
func NewSDPResponseFromRequest(req *Request, body []byte) *Response {
	res := NewResponseFromRequest(req, StatusOK, "OK", body)
	res.AppendHeader(NewHeader("Content-Type", "application/sdp"))
	res.SetBody(body)
	return res
}

func cloneResponse(res *Response) *Response {
	newRes := NewResponse(
		res.StatusCode,
		res.Reason,
	)
	newRes.SipVersion = res.SipVersion

	for _, h := range res.CloneHeaders() {
		newRes.AppendHeader(h)
	}

	newRes.SetBody(res.Body())

	// newRes.SetPrevious(res.Previous())
	newRes.SetTransport(res.Transport())
	newRes.SetSource(res.Source())
	newRes.SetDestination(res.Destination())

	return newRes
}

func CopyResponse(res *Response) *Response {
	return cloneResponse(res)
}
