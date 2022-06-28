package sip

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Header is a single SIP header.
type Header interface {
	// Name returns header name.
	Name() string
	Value() string
	// Clone() Header
	String() string
	// StringWrite is better way to reuse single buffer
	StringWrite(w io.StringWriter)

	// Next() Header
	headerClone() Header
}

type CopyHeader interface {
	headerClone() Header
}

func HeaderClone(h Header) Header {
	return h.headerClone()
}

type headers struct {
	headerOrder []Header

	via           *ViaHeader
	from          *FromHeader
	to            *ToHeader
	callid        *CallID
	contact       *ContactHeader
	cseq          *CSeq
	contentLength *ContentLength
	contentType   *ContentType
}

func (hs *headers) String() string {
	buffer := strings.Builder{}
	hs.StringWrite(&buffer)
	return buffer.String()
}

func (hs *headers) StringWrite(buffer io.StringWriter) {
	for typeIdx, header := range hs.headerOrder {
		if typeIdx > 0 {
			buffer.WriteString("\r\n")
		}
		// header := hs.headers[name]
		header.StringWrite(buffer)
		//TODO Next() to handle array of headers

		// for idx, header := range headers {
		// 	// buffer.WriteString(header.String())
		// 	header.StringWrite(buffer)
		// 	// buffer.WriteString(header.String())
		// if typeIdx < len(hs.headerOrder) || idx < len(headers) {

		// }
	}
	buffer.WriteString("\r\n")
}

// Add the given header.
func (hs *headers) AppendHeader(header Header) {
	hs.headerOrder = append(hs.headerOrder, header)
	switch m := header.(type) {
	case *ViaHeader:
		hs.via = m
	case *FromHeader:
		hs.from = m
	case *ToHeader:
		hs.to = m
	case *CallID:
		hs.callid = m
	case *CSeq:
		hs.cseq = m
	case *ContactHeader:
		hs.contact = m
	case *ContentLength:
		hs.contentLength = m
	case *ContentType:
		hs.contentType = m
	}

	// name := HeaderToLower(header.Name())
	// hs.appendHeader(name, header)
}

func (hs *headers) AppendHeaderAfter(header Header, name string) {
	ind := -1
	for i, h := range hs.headerOrder {
		if h.Name() == name {
			ind = i
		}
	}

	if ind < 0 {
		hs.AppendHeader(header)
		return
	}

	newOrder := make([]Header, len(hs.headerOrder)+1)
	copy(newOrder, hs.headerOrder[:ind+1])
	newOrder[ind] = header
	copy(newOrder[ind+1:], hs.headerOrder[ind:])
	hs.headerOrder = newOrder
}

func (hs *headers) appendHeader(name string, header Header) {
	// if _, ok := hs.headers[name]; ok {
	// 	// TODO SetNextHeader
	// 	// hs.headers[name] = append(hs.headers[name], header)
	// } else {
	// 	hs.headers[name] = header
	// 	hs.headerOrder = append(hs.headerOrder, name)
	// }
}

// // PrependHeader adds header to the front of header list
func (hs *headers) PrependHeader(headers ...Header) {
	offset := len(headers)
	newOrder := make([]Header, len(hs.headerOrder)+offset)
	for i, h := range headers {
		newOrder[i] = h
	}
	for i, h := range hs.headerOrder {
		newOrder[i+offset] = h
	}
	hs.headerOrder = newOrder
}

func (hs *headers) ReplaceHeader(header Header) {
	for i, h := range hs.headerOrder {
		if h.Name() == header.Name() {
			hs.headerOrder[i] = h
			break
		}
	}
}

// Gets some headers.
func (hs *headers) Headers() []Header {
	// hdrs := make([]Header, 0)
	// for _, key := range hs.headerOrder {
	// 	hdrs = append(hdrs, hs.headers[key])
	// }

	return hs.headerOrder
}

func (hs *headers) GetHeaders(name string) []Header {
	var hds []Header
	nameLower := HeaderToLower(name)
	for _, h := range hs.headerOrder {
		if HeaderToLower(h.Name()) == nameLower {
			hds = append(hds, h)
		}
	}
	return hds
}

// Return Header if exists, otherwise nil is returned
func (hs *headers) GetHeader(name string) Header {
	name = HeaderToLower(name)
	return hs.getHeader(name)
}

// getHeader is direct access, name must be lowercase
func (hs *headers) getHeader(nameLower string) Header {
	for _, h := range hs.headerOrder {
		if HeaderToLower(h.Name()) == nameLower {
			return h
		}
	}

	// if header, ok := hs.headers[nameLower]; ok {
	// 	return header
	// }
	return nil
}

func (hs *headers) RemoveHeader(name string) {
	// name = HeaderToLower(name)
	// delete(hs.headers, name)
	// update order slice
	for idx, entry := range hs.headerOrder {
		if entry.Name() == name {
			hs.headerOrder = append(hs.headerOrder[:idx], hs.headerOrder[idx+1:]...)
			break
		}
	}
}

// CloneHeaders returns all cloned headers in slice.
func (hs *headers) CloneHeaders() []Header {
	hdrs := make([]Header, 0)
	for _, h := range hs.headerOrder {
		hdrs = append(hdrs, h.headerClone())
	}
	return hdrs
}

func (hs *headers) CallID() (*CallID, bool) {
	return hs.callid, hs.callid != nil
}

func (hs *headers) Via() (*ViaHeader, bool) {
	return hs.via, hs.via != nil
}

func (hs *headers) From() (*FromHeader, bool) {
	return hs.from, hs.from != nil
}

func (hs *headers) To() (*ToHeader, bool) {
	return hs.to, hs.to != nil
}

func (hs *headers) CSeq() (*CSeq, bool) {
	return hs.cseq, hs.cseq != nil
}

func (hs *headers) ContentLength() (*ContentLength, bool) {
	return hs.contentLength, hs.contentLength != nil
}

func (hs *headers) ContentType() (*ContentType, bool) {
	return hs.contentType, hs.contentType != nil
}

func (hs *headers) Contact() (*ContactHeader, bool) {
	return hs.contact, hs.contact != nil
}

// Encapsulates a header that gossip does not natively support.
// This allows header data that is not understood to be parsed by gossip and relayed to the parent application.
type GenericHeader struct {
	// The name of the header.
	HeaderName string
	// The contents of the header, including any parameters.
	// This is transparent data that is not natively understood by gossip.
	Contents string
}

// Convert the header to a flat string representation.
func (h *GenericHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *GenericHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

// Pull out the h name.
func (h *GenericHeader) Name() string {
	return h.HeaderName
}

func (h *GenericHeader) Value() string {
	return h.Contents
}

// Copy the h.
func (h *GenericHeader) headerClone() Header {
	if h == nil {
		var newHeader *GenericHeader
		return newHeader
	}

	return &GenericHeader{
		HeaderName: h.HeaderName,
		Contents:   h.Contents,
	}
}

// ToHeader introduces SIP 'To' header
type ToHeader struct {
	// The display name from the header, may be omitted.
	DisplayName string
	Address     Uri
	// Any parameters present in the header.
	// Params Params
	Params HeaderParams
}

func (h *ToHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ToHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.ValueStringWrite(buffer)
}

func (h *ToHeader) Name() string { return "To" }

func (h *ToHeader) Value() string {
	var buffer strings.Builder
	h.ValueStringWrite(&buffer)
	return buffer.String()
}

func (h *ToHeader) ValueStringWrite(buffer io.StringWriter) {
	if h.DisplayName != "" {
		buffer.WriteString("\"")
		buffer.WriteString(h.DisplayName)
		buffer.WriteString("\" ")
	}

	// buffer.WriteString(fmt.Sprintf("<%s>", h.Address))
	buffer.WriteString("<")
	h.Address.StringWrite(buffer)
	// buffer.WriteString(h.Address.String())
	buffer.WriteString(">")

	if h.Params != nil && h.Params.Length() > 0 {
		buffer.WriteString(";")
		h.Params.ToStringWrite(';', buffer)
		// buffer.WriteString(h.Params.ToString(';'))
	}
}

func (header *ToHeader) Next() Header {
	return nil
}

// Copy the header.
func (h *ToHeader) headerClone() Header {
	var newTo *ToHeader
	if h == nil {
		return newTo
	}

	newTo = &ToHeader{
		DisplayName: h.DisplayName,
		Address:     h.Address,
	}
	// if h.Address != nil {
	// 	newTo.Address = h.Address.Clone()
	// }
	if h.Params != nil {
		newTo.Params = h.Params.Clone().(HeaderParams)
	}
	return newTo
}

type FromHeader struct {
	// The display name from the header, may be omitted.
	DisplayName string

	Address Uri

	// Any parameters present in the header.
	Params HeaderParams
}

func (h *FromHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *FromHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.ValueStringWrite(buffer)
}

func (h *FromHeader) Name() string { return "From" }

func (h *FromHeader) Value() string {
	var buffer strings.Builder
	h.ValueStringWrite(&buffer)
	return buffer.String()
}

func (h *FromHeader) ValueStringWrite(buffer io.StringWriter) {
	if h.DisplayName != "" {
		buffer.WriteString("\"")
		buffer.WriteString(h.DisplayName)
		buffer.WriteString("\" ")
	}

	buffer.WriteString("<")
	h.Address.StringWrite(buffer)
	buffer.WriteString(">")

	if h.Params != nil && h.Params.Length() > 0 {
		buffer.WriteString(";")
		// buffer.WriteString(h.Params.ToString(';'))
		h.Params.ToStringWrite(';', buffer)
	}
}

// Copy the header.
func (h *FromHeader) headerClone() Header {
	var newFrom *FromHeader
	if h == nil {
		return newFrom
	}

	newFrom = &FromHeader{
		DisplayName: h.DisplayName,
		Address:     h.Address,
	}
	// if h.Address != nil {
	// 	newFrom.Address = h.Address.Clone()
	// }
	if h.Params != nil {
		newFrom.Params = h.Params.Clone().(HeaderParams)
	}

	return newFrom
}

func (header *FromHeader) Next() Header {
	return nil
}

type ContactHeader struct {
	// The display name from the header, may be omitted.
	DisplayName string
	Address     Uri
	// Any parameters present in the header.
	Params HeaderParams
	Next   *ContactHeader
}

func (h *ContactHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ContactHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.ValueStringWrite(buffer)
}

func (h *ContactHeader) Name() string { return "Contact" }

func (h *ContactHeader) Value() string {
	var buffer strings.Builder
	h.ValueStringWrite(&buffer)
	return buffer.String()
}

func (h *ContactHeader) ValueStringWrite(buffer io.StringWriter) {
	hop := h
	for hop != nil {
		hop.valueWrite(buffer)
		if hop.Next != nil {
			buffer.WriteString(", ")
		}
		hop = hop.Next
	}
}

func (h *ContactHeader) valueWrite(buffer io.StringWriter) {

	switch h.Address.Wildcard {
	case true:
		// Treat the Wildcard URI separately as it must not be contained in < > angle brackets.
		buffer.WriteString("*")
		return
	default:

	}

	// Contact header can be without <>
	if h.DisplayName != "" {
		buffer.WriteString("\"")
		buffer.WriteString(h.DisplayName)
		buffer.WriteString("\" ")
	}

	buffer.WriteString("<")
	h.Address.StringWrite(buffer)
	buffer.WriteString(">")

	if (h.Params != nil) && (h.Params.Length() > 0) {
		buffer.WriteString(";")
		h.Params.ToStringWrite(';', buffer)
	}
}

// Copy the header.
func (h *ContactHeader) headerClone() Header {
	return h.Clone()
}

func (h *ContactHeader) Clone() *ContactHeader {
	newCnt := h.cloneFirst()

	newNext := newCnt
	for hop := h.Next; hop != nil; hop = hop.Next {
		newNext.Next = hop.Clone()
		newNext = newNext.Next
	}

	return newCnt
}

func (h *ContactHeader) cloneFirst() *ContactHeader {
	var newCnt *ContactHeader
	if h == nil {
		return newCnt
	}

	newCnt = &ContactHeader{
		DisplayName: h.DisplayName,
		Address:     *h.Address.Clone(),
	}

	if h.Params != nil {
		newCnt.Params = h.Params.Clone().(HeaderParams)
	}

	return newCnt
}

// CallID - 'Call-ID' header.
type CallID string

func (h *CallID) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *CallID) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *CallID) Name() string { return "Call-ID" }

func (h *CallID) Value() string { return string(*h) }

func (h *CallID) headerClone() Header {
	return h
}

type CSeq struct {
	SeqNo      uint32
	MethodName RequestMethod
}

func (h *CSeq) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *CSeq) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.ValueStringWrite(buffer)
}

func (h *CSeq) Name() string { return "CSeq" }

func (h *CSeq) Value() string {
	return fmt.Sprintf("%d %s", h.SeqNo, h.MethodName)
}

func (h *CSeq) ValueStringWrite(buffer io.StringWriter) {
	buffer.WriteString(strconv.Itoa(int(h.SeqNo)))
	buffer.WriteString(" ")
	buffer.WriteString(string(h.MethodName))
}

func (h *CSeq) headerClone() Header {
	if h == nil {
		var newCSeq *CSeq
		return newCSeq
	}

	return &CSeq{
		SeqNo:      h.SeqNo,
		MethodName: h.MethodName,
	}
}

type MaxForwards uint32

func (h *MaxForwards) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *MaxForwards) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *MaxForwards) Name() string { return "Max-Forwards" }

func (h *MaxForwards) Value() string { return strconv.Itoa(int(*h)) }

func (h *MaxForwards) headerClone() Header { return h }

type Expires uint32

func (h *Expires) String() string {
	return fmt.Sprintf("%s: %s", h.Name(), h.Value())
}

func (h *Expires) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(":")
	buffer.WriteString(h.Value())
}

func (h *Expires) Name() string { return "Expires" }

func (h Expires) Value() string { return strconv.Itoa(int(h)) }

func (h *Expires) headerClone() Header { return h }

type ContentLength uint32

func (h ContentLength) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h ContentLength) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *ContentLength) Name() string { return "Content-Length" }

func (h ContentLength) Value() string { return strconv.Itoa(int(h)) }

func (h *ContentLength) headerClone() Header { return h }

// Via header is linked list of multiple via if they are part of one header
type ViaHeader struct {
	// E.g. 'SIP'.
	ProtocolName string
	// E.g. '2.0'.
	ProtocolVersion string
	Transport       string
	Host            string
	// The port for this via hop. This is stored as a pointer type, since it is an optional field.
	Port   int
	Params HeaderParams
	Next   *ViaHeader
}

func (hop *ViaHeader) SentBy() string {
	var buf bytes.Buffer
	buf.WriteString(hop.Host)
	if hop.Port > 0 {
		buf.WriteString(fmt.Sprintf(":%d", hop.Port))
	}

	return buf.String()
}

func (h *ViaHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ViaHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *ViaHeader) Name() string { return "Via" }

func (h *ViaHeader) Value() string {
	var buffer bytes.Buffer
	h.ValueStringWrite(&buffer)
	return buffer.String()
}

func (h *ViaHeader) ValueStringWrite(buffer io.StringWriter) {
	hop := h
	for hop != nil {
		buffer.WriteString(hop.ProtocolName)
		buffer.WriteString("/")
		buffer.WriteString(hop.ProtocolVersion)
		buffer.WriteString("/")
		buffer.WriteString(hop.Transport)
		buffer.WriteString(" ")
		buffer.WriteString(hop.Host)

		if hop.Port > 0 {
			buffer.WriteString(":")
			buffer.WriteString(strconv.Itoa(hop.Port))
		}

		if hop.Params != nil && hop.Params.Length() > 0 {
			buffer.WriteString(";")
			hop.Params.ToStringWrite(';', buffer)
		}

		if hop.Next != nil {
			buffer.WriteString(", ")
		}
		hop = hop.Next
	}
}

// Return an exact copy of this ViaHeader.
func (h *ViaHeader) headerClone() Header {
	return h.Clone()
}

func (h *ViaHeader) Clone() *ViaHeader {
	newHop := h.cloneFirst()

	newNext := newHop
	for next := h.Next; next != nil; next = next.Next {
		newNext.Next = next.cloneFirst()
		newNext = newNext.Next
	}
	return newHop
}

func (h *ViaHeader) cloneFirst() *ViaHeader {
	var newHop *ViaHeader
	if h == nil {
		return newHop
	}

	newHop = &ViaHeader{
		ProtocolName:    h.ProtocolName,
		ProtocolVersion: h.ProtocolVersion,
		Transport:       h.Transport,
		Host:            h.Host,
	}
	if h.Port > 0 {
		newHop.Port = h.Port
	}
	if h.Params != nil {
		newHop.Params = h.Params.clone()
	}
	return newHop
}

type ContentType string

func (h *ContentType) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ContentType) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *ContentType) Name() string { return "Content-Type" }

func (h ContentType) Value() string { return string(h) }

func (h *ContentType) headerClone() Header { return h }

type RouteHeader struct {
	Address Uri
	Next    *RouteHeader
}

func (h *RouteHeader) Name() string { return "Route" }

func (h *RouteHeader) Value() string {
	var buffer bytes.Buffer
	h.ValueStringWrite(&buffer)
	return buffer.String()
}

func (h *RouteHeader) ValueStringWrite(buffer io.StringWriter) {
	for hop := h; hop != nil; hop = hop.Next {
		buffer.WriteString("<")
		hop.Address.StringWrite(buffer)
		buffer.WriteString(">")
		if hop.Next != nil {
			buffer.WriteString(", ")
		}
	}
}

func (h *RouteHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *RouteHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.ValueStringWrite(buffer)
}

func (h *RouteHeader) headerClone() Header {
	return h.Clone()
}

func (h *RouteHeader) Clone() *RouteHeader {
	newRoute := h.cloneFirst()
	newNext := newRoute
	for hop := h.Next; hop != nil; hop = hop.Next {
		newNext.Next = hop.Next.cloneFirst()
		newNext = newNext.Next
	}
	return newRoute
}

func (h *RouteHeader) cloneFirst() *RouteHeader {
	var newRoute *RouteHeader
	newRoute = &RouteHeader{
		Address: h.Address,
	}
	return newRoute
}

type RecordRouteHeader struct {
	Address Uri
	Next    *RecordRouteHeader
}

func (h *RecordRouteHeader) Name() string { return "Record-Route" }

func (h *RecordRouteHeader) Value() string {
	var buffer bytes.Buffer
	h.ValueStringWrite(&buffer)
	return buffer.String()
}

func (h *RecordRouteHeader) ValueStringWrite(buffer io.StringWriter) {
	for hop := h; hop != nil; hop = hop.Next {
		buffer.WriteString("<")
		hop.Address.StringWrite(buffer)
		buffer.WriteString(">")
		if hop.Next != nil {
			buffer.WriteString(", ")
		}
	}
}

func (h *RecordRouteHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *RecordRouteHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.ValueStringWrite(buffer)
}

func (h *RecordRouteHeader) headerClone() Header {
	return h.Clone()
}

func (h *RecordRouteHeader) Clone() *RecordRouteHeader {
	newRoute := h.cloneFirst()
	newNext := newRoute
	for hop := h.Next; hop != nil; hop = hop.Next {
		newNext.Next = hop.Next.cloneFirst()
		newNext = newNext.Next
	}
	return newRoute
}

func (h *RecordRouteHeader) cloneFirst() *RecordRouteHeader {
	var newRoute *RecordRouteHeader
	newRoute = &RecordRouteHeader{
		Address: h.Address,
	}
	return newRoute
}

// Copy all headers of one type from one message to another.
// Appending to any headers that were already there.
func CopyHeaders(name string, from, to Message) {
	name = HeaderToLower(name)
	for _, h := range from.GetHeaders(name) {
		to.AppendHeader(h.headerClone())
	}
}

// func PrependCopyHeaders(name string, from, to Message) {
// 	name = HeaderToLower(name)
// 	for _, h := range from.GetHeaders(name) {
// 		to.PrependHeader(h.Clone())
// 	}
// }
