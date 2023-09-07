package parser

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/emiago/sipgo/sip"
)

const (
	stateStartLine = 0
	stateHeader    = 1
	stateContent   = 2
	// stateParsed = 1
)

var ()

var streamBufReader = sync.Pool{
	New: func() interface{} {
		// The Pool's New function should generally only return pointer
		// types, since a pointer can be put into the return interface
		// value without an allocation:
		return new(bytes.Buffer)
	},
}

type ParserStream struct {
	// HeadersParsers uses default list of headers to be parsed. Smaller list parser will be faster
	headersParsers mapHeadersParser

	// runtime values
	reader            *bytes.Buffer
	msg               sip.Message
	readContentLength int
	state             int
}

func (p *ParserStream) reset() {
	p.state = stateStartLine
	p.reader = nil
	p.msg = nil
	p.readContentLength = 0
}

// ParseSIPStream parsing messages comming in stream
// It has slight overhead vs parsing full message
func (p *ParserStream) ParseSIPStream(data []byte) (msg sip.Message, err error) {
	if p.reader == nil {
		p.reader = streamBufReader.Get().(*bytes.Buffer)
		p.reader.Reset()
	}

	reader := p.reader
	reader.Write(data) // This should append to our already buffer

	unparsed := reader.Bytes() // TODO find a better way as we only want to move our offset

	msg, err = func(reader *bytes.Buffer) (msg sip.Message, err error) {

		// TODO change this with functions and store last function state
		switch p.state {
		case stateStartLine:
			startLine, err := nextLine(reader)

			if err != nil {
				if err == io.EOF {
					return nil, ErrParseLineNoCRLF
				}
				return nil, err
			}

			msg, err = ParseLine(startLine)
			if err != nil {
				return nil, err
			}

			p.state = stateHeader
			p.msg = msg
			fallthrough
		case stateHeader:
			msg := p.msg
			for {
				line, err := nextLine(reader)

				if err != nil {
					if err == io.EOF {
						// No more to read
						return nil, ErrParseLineNoCRLF
					}
					return nil, err
				}

				if len(line) == 0 {
					// We've hit second CRLF
					break
				}

				err = p.headersParsers.parseMsgHeader(msg, line)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", err.Error(), ErrParseInvalidMessage)
					// log.Info().Err(err).Str("line", line).Msg("skip header due to error")
				}
				unparsed = reader.Bytes()
			}
			unparsed = reader.Bytes()

			// Grab content length header
			// TODO: Maybe this is not best approach
			hdrs := msg.GetHeaders("Content-Length")
			if len(hdrs) == 0 {
				// No body then
				return msg, nil
			}

			h := hdrs[0]

			// TODO: Have fast reference instead casting
			var contentLength int
			if clh, ok := h.(*sip.ContentLengthHeader); ok {
				contentLength = int(*clh)
			} else {
				n, err := strconv.Atoi(h.Value())
				if err != nil {
					return nil, fmt.Errorf("fail to parse content length: %w", err)
				}
				contentLength = n
			}

			if contentLength <= 0 {
				return msg, nil
			}

			body := make([]byte, contentLength)
			msg.SetBody(body)

			p.state = stateContent
			fallthrough
		case stateContent:
			msg := p.msg
			body := msg.Body()
			contentLength := len(body)

			n, err := reader.Read(body[p.readContentLength:])
			unparsed = reader.Bytes()
			if err != nil {
				return nil, fmt.Errorf("read message body failed: %w", err)
			}
			p.readContentLength += n

			if p.readContentLength < contentLength {
				return nil, ErrParseReadBodyIncomplete
			}

			p.state = -1 // Clear state
			return msg, nil
		default:
			return nil, fmt.Errorf("Parser is in unknown state")
		}

	}(reader)

	switch err {
	case ErrParseLineNoCRLF, ErrParseReadBodyIncomplete:
		reader.Reset()
		reader.Write(unparsed)
		return nil, ErrParseSipPartial
	}

	// IN all other cases do reset
	streamBufReader.Put(reader)
	p.reset()

	return msg, err
}
