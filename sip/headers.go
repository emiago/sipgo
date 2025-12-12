package sip

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

const ()

// Header is a single SIP header.
type Header interface {
	// Name returns underlying header name.
	Name() string
	Value() string
	String() string
	// StringWrite is better way to reuse single buffer
	StringWrite(w io.StringWriter)

	// Next() Header
	headerClone() Header
	valueStringWrite(w io.StringWriter)
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

	// Here we only need headers that have frequent access.
	// DO not add any custom headers, or more specific headers
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

	// CompactHeaders
	CompactHeaders bool
}

func (hs *headers) String() string {
	buffer := strings.Builder{}
	hs.StringWrite(&buffer)
	return buffer.String()
}

func (hs *headers) StringWrite(buffer io.StringWriter) {
	if hs.CompactHeaders {
		for typeIdx, header := range hs.headerOrder {
			if typeIdx > 0 {
				buffer.WriteString("\r\n")
			}

			// https://www.cs.columbia.edu/sip/compact.html
			name := header.Name()
			buffer.WriteString(compactHeaderName(name))
			buffer.WriteString(": ")
			header.valueStringWrite(buffer)
		}
		buffer.WriteString("\r\n")
		return
	}

	for typeIdx, header := range hs.headerOrder {
		if typeIdx > 0 {
			buffer.WriteString("\r\n")
		}
		buffer.WriteString(header.Name())
		buffer.WriteString(": ")
		header.valueStringWrite(buffer)
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

func (hs *headers) unref(header Header) {
	switch header.(type) {
	case *ViaHeader:
		hs.via = nil
	case *FromHeader:
		hs.from = nil
	case *ToHeader:
		hs.to = nil
	case *CallIDHeader:
		hs.callid = nil
	case *CSeqHeader:
		hs.cseq = nil
	case *ContactHeader:
		hs.contact = nil
	case *RouteHeader:
		hs.route = nil
	case *RecordRouteHeader:
		hs.recordRoute = nil
	case *ContentLengthHeader:
		hs.contentLength = nil
	case *ContentTypeHeader:
		hs.contentType = nil
	case *MaxForwardsHeader:
		hs.maxForwards = nil
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

	if ind == -1 {
		hs.AppendHeader(header)
		return
	}

	if ind+1 == len(hs.headerOrder) {
		hs.AppendHeader(header)
		return
	}

	newOrder := make([]Header, len(hs.headerOrder)+1)
	copy(newOrder, hs.headerOrder[:ind+1])
	newOrder[ind+1] = header
	hs.setHeaderRef(header)
	copy(newOrder[ind+2:], hs.headerOrder[ind+1:])
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
			hs.headerOrder[i] = header
			hs.setHeaderRef(header)
			break
		}
	}
}

// Headers  returns list of headers.
// NOT THREAD SAFE for updating. Clone them
func (hs *headers) Headers() []Header {
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
			hs.unref(entry)
			break
		}
	}

	removed = foundIdx >= 0
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

// CloneHeaders returns all cloned headers in slice.
func (hs *headers) CloneHeaders() []Header {
	hdrs := make([]Header, 0)
	for _, h := range hs.headerOrder {
		hdrs = append(hdrs, h.headerClone())
	}
	return hdrs
}

// Here are most used headers with quick reference

// CallID returns underlying CallID parsed header or nil if not exists
func (hs *headers) CallID() *CallIDHeader {
	if hs.callid == nil {
		var h CallIDHeader
		if parseHeaderLazy(hs, parseCallIdHeader, []string{"call-id", "i"}, &h) {
			hs.callid = &h
		}
	}
	return hs.callid
}

// Via returns underlying Via parsed header or nil if not exists
func (hs *headers) Via() *ViaHeader {
	if hs.via == nil {
		h := &ViaHeader{}
		if parseHeaderLazy(hs, parseViaHeader, []string{"via", "v"}, h) {
			hs.via = h
		}
	}
	return hs.via
}

// From returns underlying From parsed header or nil if not exists
func (hs *headers) From() *FromHeader {
	if hs.from == nil {
		h := &FromHeader{}
		if parseHeaderLazy(hs, parseFromHeader, []string{"from", "f"}, h) {
			hs.from = h
		}
	}
	return hs.from
}

// To returns underlying To parsed header or nil if not exists
func (hs *headers) To() *ToHeader {
	if hs.to == nil {
		h := &ToHeader{}
		if parseHeaderLazy(hs, parseToHeader, []string{"to", "t"}, h) {
			hs.to = h
		}
	}
	return hs.to
}

// CSeq returns underlying CSEQ parsed header or nil if not exists
func (hs *headers) CSeq() *CSeqHeader {
	if hs.cseq == nil {
		h := &CSeqHeader{}
		if parseHeaderLazy(hs, parseCSeqHeader, []string{"cseq"}, h) {
			hs.cseq = h
		}
	}
	return hs.cseq
}

// MaxForwards returns underlying Max-Forwards parsed header or nil if not exists
func (hs *headers) MaxForwards() *MaxForwardsHeader {
	if hs.maxForwards == nil {
		var h MaxForwardsHeader
		if parseHeaderLazy(hs, parseMaxForwardsHeader, []string{"max-forwards"}, &h) {
			hs.maxForwards = &h
		}
	}
	return hs.maxForwards
}

// ContentLength returns underlying Content-Length parsed header or nil if not exists
func (hs *headers) ContentLength() *ContentLengthHeader {
	if hs.contentLength == nil {
		var h ContentLengthHeader
		if parseHeaderLazy(hs, parseContentLengthHeader, []string{"content-length", "l"}, &h) {
			hs.contentLength = &h
		}
	}

	return hs.contentLength
}

// ContentType returns underlying Content-Type parsed header or nil if not exists
func (hs *headers) ContentType() *ContentTypeHeader {
	if hs.contentType == nil {
		var h ContentTypeHeader
		if parseHeaderLazy(hs, parseContentTypeHeader, []string{"content-type", "c"}, &h) {
			hs.contentType = &h
		}
	}

	return hs.contentType
}

// Contact returns underlying Contact parsed header or nil if not exists
func (hs *headers) Contact() *ContactHeader {
	if hs.contact == nil {
		h := &ContactHeader{}
		if parseHeaderLazy(hs, parseContactHeader, []string{"contact", "m"}, h) {
			hs.contact = h
		}
	}

	return hs.contact
}

// Route returns underlying Route parsed header or nil if not exists
func (hs *headers) Route() *RouteHeader {
	if hs.route == nil {
		h := &RouteHeader{}
		if parseHeaderLazy(hs, parseRouteHeader, []string{"route"}, h) {
			hs.route = h
		}
	}
	return hs.route
}

// RecordRoute returns underlying Record-Route parsed header or nil if not exists
func (hs *headers) RecordRoute() *RecordRouteHeader {
	if hs.recordRoute == nil {
		h := &RecordRouteHeader{}
		if parseHeaderLazy(hs, parseRecordRouteHeader, []string{"record-route"}, h) {
			hs.recordRoute = h
		}
	}
	return hs.recordRoute
}

// ReferTo parses underlying Refer-To header or nil if not exists
func (hs *headers) ReferTo() *ReferToHeader {
	h := &ReferToHeader{}
	if parseHeaderLazy(hs, parseReferToHeader, []string{"refer-to"}, h) {
		return h
	}
	return nil
}

// ReferredBy parses underlying Referred-By header or nil if not exists
func (hs *headers) ReferredBy() *ReferredByHeader {
	h := &ReferredByHeader{}
	if parseHeaderLazy(hs, parseReferredByHeader, []string{"referred-by"}, h) {
		return h
	}
	return nil
}

// NewHeader creates generic type of header
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

func (h *genericHeader) valueStringWrite(buffer io.StringWriter) {
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
	h.valueStringWrite(buffer)
}

func (h *ToHeader) Name() string { return "To" }

func (h *ToHeader) Value() string {
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *ToHeader) valueStringWrite(buffer io.StringWriter) {
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

func (h *ToHeader) AsFrom() FromHeader {
	return FromHeader{
		Address:     *h.Address.Clone(),
		Params:      h.Params.Clone(),
		DisplayName: h.DisplayName,
	}
}

// Copy the header.
func (h *ToHeader) headerClone() Header {
	var newTo *ToHeader
	if h == nil {
		return newTo
	}

	newTo = &ToHeader{
		DisplayName: h.DisplayName,
		Address:     *h.Address.Clone(),
	}
	// if h.Address != nil {
	// 	newTo.Address = h.Address.Clone()
	// }
	if h.Params != nil {
		newTo.Params = h.Params.Clone()
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
	h.valueStringWrite(buffer)
}

func (h *FromHeader) Name() string { return "From" }

func (h *FromHeader) Value() string {
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *FromHeader) valueStringWrite(buffer io.StringWriter) {
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
		Address:     *h.Address.Clone(),
	}
	// if h.Address != nil {
	// 	newFrom.Address = h.Address.Clone()
	// }
	if h.Params != nil {
		newFrom.Params = h.Params.Clone()
	}

	return newFrom
}

func (h *FromHeader) AsTo() ToHeader {
	return ToHeader{
		Address:     *h.Address.Clone(),
		Params:      h.Params.Clone(),
		DisplayName: h.DisplayName,
	}
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
	h.valueStringWrite(buffer)
}

func (h *ContactHeader) Name() string { return "Contact" }

func (h *ContactHeader) Value() string {
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *ContactHeader) valueStringWrite(buffer io.StringWriter) {

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
		newCnt.Params = h.Params.Clone()
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

func (h *CallIDHeader) valueStringWrite(buffer io.StringWriter) {
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
	h.valueStringWrite(buffer)
}

func (h *CSeqHeader) Name() string { return "CSeq" }

func (h *CSeqHeader) Value() string {
	return fmt.Sprintf("%d %s", h.SeqNo, h.MethodName)
}

func (h *CSeqHeader) valueStringWrite(buffer io.StringWriter) {
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

func (h *MaxForwardsHeader) valueStringWrite(buffer io.StringWriter) {
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
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *ExpiresHeader) valueStringWrite(buffer io.StringWriter) {
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

func (h *ContentLengthHeader) valueStringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Value())
}

func (h *ContentLengthHeader) Name() string { return "Content-Length" }

func (h ContentLengthHeader) Value() string { return strconv.Itoa(int(h)) }

func (h *ContentLengthHeader) headerClone() Header { return h }

// ViaHeader is Via header representation.
type ViaHeader struct {
	// E.g. 'SIP'.
	ProtocolName string
	// E.g. '2.0'.
	ProtocolVersion string
	Transport       string
	Host            string
	Port            int // This is optional
	Params          HeaderParams
}

func (hop *ViaHeader) SentBy() string {
	var buf strings.Builder
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
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *ViaHeader) valueStringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.ProtocolName)
	buffer.WriteString("/")
	buffer.WriteString(h.ProtocolVersion)
	buffer.WriteString("/")
	buffer.WriteString(h.Transport)
	buffer.WriteString(" ")
	buffer.WriteString(uriIP(h.Host))

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

func (h *ContentTypeHeader) valueStringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Value())
}

func (h *ContentTypeHeader) Name() string { return "Content-Type" }

func (h *ContentTypeHeader) Value() string { return string(*h) }

func (h *ContentTypeHeader) headerClone() Header { return h }

// RouteHeader  is Route header representation.
type RouteHeader struct {
	Address Uri
}

func (h *RouteHeader) Name() string { return "Route" }

func (h *RouteHeader) Value() string {
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *RouteHeader) valueStringWrite(buffer io.StringWriter) {
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
	h.valueStringWrite(buffer)
}

func (h *RouteHeader) headerClone() Header {
	return h.Clone()
}

func (h *RouteHeader) Clone() *RouteHeader {
	newRoute := &RouteHeader{
		Address: *h.Address.Clone(),
	}
	return newRoute
}

// RecordRouteHeader is Record-Route header representation.
type RecordRouteHeader struct {
	Address Uri
}

func (h *RecordRouteHeader) Name() string { return "Record-Route" }

func (h *RecordRouteHeader) Value() string {
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *RecordRouteHeader) valueStringWrite(buffer io.StringWriter) {
	buffer.WriteString("<")
	h.Address.StringWrite(buffer)
	buffer.WriteString(">")
}

func (h *RecordRouteHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *RecordRouteHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.valueStringWrite(buffer)
}

func (h *RecordRouteHeader) headerClone() Header {
	return h.Clone()
}

func (h *RecordRouteHeader) Clone() *RecordRouteHeader {
	newRoute := &RecordRouteHeader{
		Address: *h.Address.Clone(),
	}
	return newRoute
}

// ReferToHeader is Refer-To header representation.
type ReferToHeader struct {
	Address Uri
}

func (h *ReferToHeader) Name() string { return "Refer-To" }

func (h *ReferToHeader) Value() string {
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *ReferToHeader) valueStringWrite(buffer io.StringWriter) {
	buffer.WriteString("<")
	h.Address.StringWrite(buffer)
	buffer.WriteString(">")
}

func (h *ReferToHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ReferToHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.valueStringWrite(buffer)
}

func (h *ReferToHeader) headerClone() Header {
	return h.Clone()
}

func (h *ReferToHeader) Clone() *ReferToHeader {
	newTarget := &ReferToHeader{
		Address: *h.Address.Clone(),
	}
	return newTarget
}

// ReferredByHeader is Referred-By header representation.
type ReferredByHeader struct {
	DisplayName string
	Address     Uri
	Params      HeaderParams
}

func (h *ReferredByHeader) Name() string { return "Referred-By" }

func (h *ReferredByHeader) Value() string {
	var buffer strings.Builder
	h.valueStringWrite(&buffer)
	return buffer.String()
}

func (h *ReferredByHeader) valueStringWrite(buffer io.StringWriter) {
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
		h.Params.ToStringWrite(';', buffer)
	}
}

func (h *ReferredByHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *ReferredByHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	h.valueStringWrite(buffer)
}

func (h *ReferredByHeader) headerClone() Header {
	return h.Clone()
}

func (h *ReferredByHeader) Clone() *ReferredByHeader {
	newTarget := &ReferredByHeader{
		DisplayName: h.DisplayName,
		Address:     *h.Address.Clone(),
	}
	if h.Params != nil {
		newTarget.Params = h.Params.Clone()
	}
	return newTarget
}

// Copy all headers of one type from one message to another.
// Appending to any headers that were already there.
func CopyHeaders(name string, from, to Message) {
	for _, h := range from.GetHeaders(name) {
		to.AppendHeader(h.headerClone())
	}
}

type headerPointerReceiver[T any] interface {
	Header
	*T
}

func parseHeaderLazy[T any, HP headerPointerReceiver[T]](hs *headers, f func(headerText string, h HP) error, headerNames []string, h HP) bool {
	for _, n := range headerNames {
		hdr := hs.getHeader(n)
		if hdr == nil {
			continue
		}

		if err := f(hdr.Value(), h); err != nil {
			DefaultLogger().Debug("Lazy header parsing failed", "header", hdr.Name(), "error", err)
			return false
		}
		return true
	}
	return false
}

func compactHeaderName(full string) string {
	switch full {
	case "Via":
		return "v"
	case "From":
		return "f"
	case "To":
		return "t"
	case "Call-ID":
		return "i"
	case "Content-Type":
		return "c"
	case "Content-Length":
		return "l"
	case "Contact":
		return "m"
	case "Refer-To":
		return "r"
	case "Content-Encoding":
		return "e"
	case "Accept-Contact":
		return "a"
	case "Referred-By":
		return "b"
	case "Supported":
		return "k"
	case "Event":
		return "o"
	case "Subject":
		return "s"
	case "Allow-Events":
		return "u"

	default:
		return full
	}
}
