package sip

import (
	"io"
	"strings"
)

// UserAgentHeader  is User-Agent header representation.
type UserAgentHeader string

func (h *UserAgentHeader) String() string {
	var buffer strings.Builder
	h.StringWrite(&buffer)
	return buffer.String()
}

func (h *UserAgentHeader) StringWrite(buffer io.StringWriter) {
	buffer.WriteString(h.Name())
	buffer.WriteString(": ")
	buffer.WriteString(h.Value())
}

func (h *UserAgentHeader) Name() string { return "User-Agent" }

func (h *UserAgentHeader) Value() string {
	if h == nil {
		return ""
	} else {
		return string(*h)
	}
}

func (h *UserAgentHeader) headerClone() Header { return h }
