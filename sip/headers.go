package sip

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const ()

// Header is a single SIP header.
type Header interface {
	// Name returns header name.
	Name() string
	Value() string
	String() string
	// StringWrite is better way to reuse single buffer
	StringWrite(w io.StringWriter)

	// Next() Header
	headerClone() Header
}

// CopyHeader is internal interface for cloning headers.
// Maybe it will be full exposed later
type CopyHeader interface {
	headerClone() Header
}

// HeaderClone is generic function for cloning header
func HeaderClone(h Header) Header {
	return h.headerClone()
}

type headers struct {
	headerOrder []Header

	via           *ViaHeader
	from          *FromHeader
	to            *ToHeader
	callid        *CallIDHeader
	contact       *ContactHeader
	cseq          *CSeqHeader
	contentLength *ContentLengthHeader
	contentType   *ContentTypeHeader
	route         *RouteHeader
	recordRoute   *RecordRouteHeader
	maxForwards   *MaxForwardsHeader
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

// setHeaderRef should be always called when new header is added
// it should point to TOPMOST header value
// it creates fast access to header
func (hs *headers) setHeaderRef(header Header) {
	switch m := header.(type) {
	case *ViaHeader:
		hs.via = m
	case *FromHeader:
		hs.from = m
	case *ToHeader:
		hs.to = m
	case *CallIDHeader:
		hs.callid = m
	case *CSeqHeader:
		hs.cseq = m
	case *ContactHeader:
		hs.contact = m
	case *RouteHeader:
		hs.route = m
	case *RecordRouteHeader:
		hs.recordRoute = m
	case *ContentLengthHeader:
		hs.contentLength = m
	case *ContentTypeHeader:
		hs.contentType = m
	case *MaxForwardsHeader:
		hs.maxForwards = m
	}
}

// AppendHeader adds header at end of header list
func (hs *headers) AppendHeader(header Header) {
	hs.headerOrder = append(hs.headerOrder, header)
	// update only if no multiple headers. TODO find this better
	switch m := header.(type) {
	case *ViaHeader:
		if hs.via == nil {
			hs.via = m
		}
	case *RouteHeader:
		if hs.route == nil {
			hs.route = m
		}
	case *RecordRouteHeader:
		if hs.recordRoute == nil {
			hs.recordRoute = m
		}
	default:
		hs.setHeaderRef(header)
	}
}

// AppendHeaderAfter adds header after specified header. In case header does not exist normal AppendHeader is called
// Use it only if you need it
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

// PrependHeader adds header to the front of header list
// using as list reduces need of realloc underneath array
func (hs *headers) PrependHeader(headers ...Header) {
	offset := len(headers)
	newOrder := make([]Header, len(hs.headerOrder)+offset)
	for i, h := range headers {
		newOrder[i] = h
		hs.setHeaderRef(h)
	}
	for i, h := range hs.headerOrder {
		newOrder[i+offset] = h
	}
	hs.headerOrder = newOrder
}

// ReplaceHeader replaces first header with same name
func (hs *headers) ReplaceHeader(header Header) {
	for i, h := range hs.headerOrder {
		if h.Name() == header.Name() {
			hs.headerOrder[i] = h
			hs.setHeaderRef(h)
			break
		}
	}
}

// Headers gets some headers.
func (hs *headers) Headers() []Header {
	// hdrs := make([]Header, 0)
	// for _, key := range hs.headerOrder {
	// 	hdrs = append(hdrs, hs.headers[key])
	// }

	return hs.headerOrder
}

// GetHeaders returns list of headers with same name
// Use lower case to avoid allocs
// Headers are pointers, always Clone them for change
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

// GetHeader returns Header if exists, otherwise nil is returned
// Use lower case to avoid allocs
// Headers are pointers, always Clone them for change
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

// RemoveHeader removes header by name
func (hs *headers) RemoveHeader(name string) (removed bool) {
	// name = HeaderToLower(name)
	// delete(hs.headers, name)
	// update order slice
	foundIdx := -1
	for idx, entry := range hs.headerOrder {
		if entry.Name() == name {
			foundIdx = idx
			hs.headerOrder = append(hs.headerOrder[:idx], hs.headerOrder[idx+1:]...)
			break
		}
	}

	removed = foundIdx > 0
	// Update refs
	if removed {
		for _, entry := range hs.headerOrder[foundIdx:] {
			if entry.Name() == name {
				hs.setHeaderRef(entry)
				break
			}
		}
	}

	return removed
}

// RemoveHeaderOn before removes header with name, yields (calls func) once it gets found
func (hs *headers) RemoveHeaderOn(name string, f func(h Header) bool) (removed bool) {
	// name = HeaderToLower(name)
	// delete(hs.headers, name)
	// update order slice
	foundIdx := -1
	for idx, entry := range hs.headerOrder {
		if entry.Name() == name {
			if f(entry) {
				foundIdx = idx
				hs.headerOrder = append(hs.headerOrder[:idx], hs.headerOrder[idx+1:]...)
				break
			}
		}
	}

	// Update refs
	if foundIdx > 0 {
		for _, entry := range hs.headerOrder[foundIdx:] {
			if entry.Name() == name {
				hs.setHeaderRef(entry)
				break
			}
		}
	}
	return
}

// CloneHeaders returns all cloned headers in slice.
func (hs *headers) CloneHeaders() []Header {
	hdrs := make([]Header, 0)
	for _, h := range hs.headerOrder {
		hdrs = append(hdrs, h.headerClone())
	}
	return hdrs
}

func (hs *headers) CallID() (*CallIDHeader, bool) {
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

func (hs *headers) CSeq() (*CSeqHeader, bool) {
	return hs.cseq, hs.cseq != nil
}

func (hs *headers) ContentLength() (*ContentLengthHeader, bool) {
	return hs.contentLength, hs.contentLength != nil
}

func (hs *headers) ContentType() (*ContentTypeHeader, bool) {
	return hs.contentType, hs.contentType != nil
}

func (hs *headers) Contact() (*ContactHeader, bool) {
	return hs.contact, hs.contact != nil
}

func (hs *headers) Route() (*RouteHeader, bool) {
	return hs.route, hs.route != nil
}

func (hs *headers) RecordRoute() (*RecordRouteHeader, bool) {
	return hs.recordRoute, hs.recordRoute != nil
}

func (hs *headers) MaxForwards() (*MaxForwardsHeader, bool) {
	return hs.maxForwards, hs.maxForwards != nil
}

// NewHeader creates generic type of header.
// Use it for unknown type of header
func NewHeader(name, value string) Header {
	return &genericHeader{
		HeaderName: name,
		Contents:   value,
	}
}

// genericHeader is generic struct for unknown headers
type genericHeader struct {
	// The name of the header.
	HeaderName string
	// The contents of the header, including any parameters.
	Contents string
}

func (h *genericHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *genericHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *genericHeader) Name() string {
	return h.HeaderName
}

func (h *genericHeader) Value() string {
	return h.Contents
}

func (h *genericHeader) headerClone() Header {
	if h == nil {
		var newHeader *genericHeader
		return newHeader
	}

	return &genericHeader{
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

// ContactHeader is Contact header representation
type ContactHeader struct {
	// The display name from the header, may be omitted.
	DisplayName string
	Address     Uri
	// Any parameters present in the header.
	Params HeaderParams
}

func (h *ContactHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ContactHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.valueWrite(buffer)
}

func (h *ContactHeader) Name() string { return "Contact" }

func (h *ContactHeader) Value() string {
	var buffer strings.Builder
	h.valueWrite(&buffer)
	return buffer.String()
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

// CallIDHeader is a Call-ID header presentation
type CallIDHeader string

func (h *CallIDHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *CallIDHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *CallIDHeader) Name() string { return "Call-ID" }

func (h *CallIDHeader) Value() string { return string(*h) }

func (h *CallIDHeader) headerClone() Header {
	return h
}

// CSeq is CSeq header
type CSeqHeader struct {
	SeqNo      uint32
	MethodName RequestMethod
}

func (h *CSeqHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *CSeqHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.ValueStringWrite(buffer)
}

func (h *CSeqHeader) Name() string { return "CSeq" }

func (h *CSeqHeader) Value() string {
	return fmt.Sprintf("%d %s", h.SeqNo, h.MethodName)
}

func (h *CSeqHeader) ValueStringWrite(buffer io.StringWriter) {
	buffer.WriteString(strconv.Itoa(int(h.SeqNo)))
	buffer.WriteString(" ")
	buffer.WriteString(string(h.MethodName))
}

func (h *CSeqHeader) headerClone() Header {
	if h == nil {
		var newCSeq *CSeqHeader
		return newCSeq
	}

	return &CSeqHeader{
		SeqNo:      h.SeqNo,
		MethodName: h.MethodName,
	}
}

// MaxForwardsHeader is Max-Forwards header representation
type MaxForwardsHeader uint32

func (h *MaxForwardsHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *MaxForwardsHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *MaxForwardsHeader) Name() string { return "Max-Forwards" }

func (h *MaxForwardsHeader) Value() string { return strconv.Itoa(int(*h)) }

func (h *MaxForwardsHeader) headerClone() Header { return h }

func (h *MaxForwardsHeader) Dec() {
	*h = MaxForwardsHeader(uint32(*h) - 1)
}

func (h MaxForwardsHeader) Val() uint32 {
	return uint32(h)
}

// ExpiresHeader is Expires header representation
type ExpiresHeader uint32

func (h *ExpiresHeader) String() string {
	return fmt.Sprintf("%s: %s", h.Name(), h.Value())
}

func (h *ExpiresHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(":")
	buffer.WriteString(h.Value())
}

func (h *ExpiresHeader) Name() string { return "Expires" }

func (h ExpiresHeader) Value() string { return strconv.Itoa(int(h)) }

func (h *ExpiresHeader) headerClone() Header { return h }

// ContentLengthHeader is Content-Length header representation
type ContentLengthHeader uint32

func (h ContentLengthHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h ContentLengthHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *ContentLengthHeader) Name() string { return "Content-Length" }

func (h ContentLengthHeader) Value() string { return strconv.Itoa(int(h)) }

func (h *ContentLengthHeader) headerClone() Header { return h }

// ViaHeader is Via header representation.
// It can be linked list of multiple via if they are part of one header
type ViaHeader struct {
	// E.g. 'SIP'.
	ProtocolName string
	// E.g. '2.0'.
	ProtocolVersion string
	Transport       string
	// TODO consider changing Host Port as struct Addr from transport
	Host   string
	Port   int // This is optional
	Params HeaderParams
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
	buffer.WriteString(h.ProtocolName)
	buffer.WriteString("/")
	buffer.WriteString(h.ProtocolVersion)
	buffer.WriteString("/")
	buffer.WriteString(h.Transport)
	buffer.WriteString(" ")
	buffer.WriteString(h.Host)

	if h.Port > 0 {
		buffer.WriteString(":")
		buffer.WriteString(strconv.Itoa(h.Port))
	}

	if h.Params != nil && h.Params.Length() > 0 {
		buffer.WriteString(";")
		h.Params.ToStringWrite(';', buffer)
	}
}

// Return an exact copy of this ViaHeader.
func (h *ViaHeader) headerClone() Header {
	return h.Clone()
}

func (h *ViaHeader) Clone() *ViaHeader {
	newHop := &ViaHeader{
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

// ContentTypeHeader  is Content-Type header representation.
type ContentTypeHeader string

func (h *ContentTypeHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ContentTypeHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *ContentTypeHeader) Name() string { return "Content-Type" }

func (h ContentTypeHeader) Value() string { return string(h) }

func (h *ContentTypeHeader) headerClone() Header { return h }

// RouteHeader  is Route header representation.
type RouteHeader struct {
	Address Uri
}

func (h *RouteHeader) Name() string { return "Route" }

func (h *RouteHeader) Value() string {
	var buffer bytes.Buffer
	h.ValueStringWrite(&buffer)
	return buffer.String()
}

func (h *RouteHeader) ValueStringWrite(buffer io.StringWriter) {
	buffer.WriteString("<")
	h.Address.StringWrite(buffer)
	buffer.WriteString(">")
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
	return newRoute
}

func (h *RouteHeader) cloneFirst() *RouteHeader {
	var newRoute *RouteHeader
	newRoute = &RouteHeader{
		Address: h.Address,
	}
	return newRoute
}

// RecordRouteHeader is Record-Route header representation.
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
	for _, h := range from.GetHeaders(name) {
		to.AppendHeader(h.headerClone())
	}
}
