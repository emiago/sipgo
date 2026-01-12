package sip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
)

type parserState int

const (
	stateStartLine = parserState(iota)
	stateHeader
	stateContent
)

var streamBufReader = sync.Pool{
	New: func() interface{} {
		// The Pool's New function should generally only return pointer
		// types, since a pointer can be put into the return interface
		// value without an allocation:
		return new(bytes.Buffer)
	},
}

type ParserStream struct {
	p *Parser

	// runtime values
	buf           *bytes.Buffer
	state         parserState
	totalRead     int
	msg           Message
	headerBuf     []Header
	contentLength *ContentLengthHeader
	contentOff    int
}

func (p *ParserStream) reset() {
	p.state = stateStartLine
	p.totalRead = 0
	p.msg = nil
	for i := range p.headerBuf {
		p.headerBuf[i] = nil
	}
	p.headerBuf = p.headerBuf[:0]
	p.contentLength = nil
	p.contentOff = 0
}

// Reset the parser and the internal buffer.
func (p *ParserStream) Reset() {
	p.reset()
	if p.buf != nil {
		p.buf.Reset()
	}
}

// Close the parser and free the associated resources.
func (p *ParserStream) Close() {
	p.reset()
	buf := p.buf
	p.buf = nil
	if buf != nil {
		streamBufReader.Put(buf)
	}
}

// parseSIPStreamFull parsing messages comming in stream
// It has slight overhead vs parsing full message
func (p *ParserStream) parseSIPStreamFull(data []byte) (msgs []Message, err error) {
	err = p.ParseSIPStream(data, func(msg Message) {
		msgs = append(msgs, msg)
	})
	return msgs, err
}

// ParseSIPStream parses SIP stream and calls callback as soon first SIP message is parsed
func (p *ParserStream) ParseSIPStream(data []byte, cb func(msg Message)) error {
	if _, err := p.Write(data); err != nil {
		return err
	}
	for p.buf.Len() > 0 {
		msg, _, err := p.ParseNext()
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return ErrParseSipPartial
		} else if err != nil {
			return err
		}
		cb(msg)
	}
	return nil
}

// Buffer returns an internal buffer used by the parser.
// This allows to inspect the current parser state and possibly recover the stream with Discard.
func (p *ParserStream) Buffer() *bytes.Buffer {
	if p.buf == nil {
		p.buf = streamBufReader.Get().(*bytes.Buffer)
		p.buf.Reset()
	}
	return p.buf
}

// Discard specified amount of data and reset the parser.
// Can be used to skip malformed messages and recover the stream.
func (p *ParserStream) Discard(n int) {
	p.reset()
	if p.buf != nil {
		_ = p.buf.Next(n)
	}
}

// Write data to the internal buffer. Must be called before ParseNext.
func (p *ParserStream) Write(data []byte) (int, error) {
	buf := p.Buffer()
	buf.Write(data) // This should append to our existing buffer
	return len(data), nil
}

// ParseNext parses the next SIP message from an internal buffer.
// It may return io.ErrUnexpectedEOF, indicating that more data needs to be written with Write.
func (p *ParserStream) ParseNext() (Message, int, error) {
	if p.buf == nil {
		return nil, 0, io.ErrUnexpectedEOF
	}
	err := p.parseSingle()
	reset := err == nil
	msg, n := p.msg, p.totalRead
	if err == nil && p.totalRead > p.p.MaxMessageLength {
		err = ErrMessageTooLarge
	}
	if reset {
		p.reset()
	}
	return msg, n, err
}

func (p *ParserStream) advance(n int) {
	p.totalRead += n
	_ = p.buf.Next(n)
}

func (p *ParserStream) parseSingle() error {
	if p.buf == nil {
		return io.ErrUnexpectedEOF
	}
	var (
		n   int
		err error
	)
	switch p.state {
	case stateStartLine:
		var msg Message
		msg, n, err = p.p.parseStartLine(p.buf.Bytes(), true)
		p.advance(n)
		if err != nil {
			return err
		}
		p.state = stateHeader
		p.msg = msg
		fallthrough
	case stateHeader:
		for {
			p.headerBuf, n, err = p.p.parseNextHeader(p.headerBuf[:0], p.buf.Bytes())
			p.advance(n)
			for _, h := range p.headerBuf {
				switch h := h.(type) {
				case *ContentLengthHeader:
					p.contentLength = h
				}
				p.msg.AppendHeader(h)
			}
			if err == errParseNoMoreHeaders {
				break
			}
			if err != nil {
				return err
			}
		}
		if p.contentLength == nil {
			// RFC 3261 - 7.5.
			// The Content-Length header field value is used to locate the end of
			// each SIP message in a stream. It will always be present when SIP
			// messages are sent over stream-oriented transports.
			return ErrParseReadBodyIncomplete
		}
		contentLength := int(*p.contentLength)
		if contentLength == 0 {
			p.state = -1
			return nil
		}
		body := make([]byte, contentLength)
		p.msg.SetBody(body)
		p.state = stateContent
		fallthrough
	case stateContent:
		body := p.msg.Body()
		contentLength := len(body)

		n = copy(body[p.contentOff:], p.buf.Bytes())
		p.advance(n)
		p.contentOff += n

		if p.contentOff < contentLength {
			return io.ErrUnexpectedEOF
		}
		p.state = -1
		return nil
	default:
		return fmt.Errorf("Parser is in unknown state")
	}
}
