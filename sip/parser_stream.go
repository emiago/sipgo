package sip

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

const (
	stateStartLine = 0
	stateHeader    = 1
	stateContent   = 2
	// stateParsed = 1
)

var (
	ParseMaxMessageLength = 65535
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
	// HeadersParsers uses default list of headers to be parsed. Smaller list parser will be faster
	headersParsers mapHeadersParser

	// runtime values
	reader            *bytes.Buffer
	msg               Message
	readContentLength int
	state             int
}

func (p *ParserStream) reset() {
	p.state = stateStartLine
	p.reader = nil
	p.msg = nil
	p.readContentLength = 0
}

// parseSIPStreamFull parsing messages comming in stream
// It has slight overhead vs parsing full message
func (p *ParserStream) parseSIPStreamFull(data []byte) (msgs []Message, err error) {
	return msgs, p.ParseSIPStream(data, func(msg Message) {
		msgs = append(msgs, msg)
	})
}

// ParseSIPStream parses SIP stream and calls callback as soon first SIP message is parsed
func (p *ParserStream) ParseSIPStream(data []byte, cb func(msg Message)) (err error) {
	if p.reader == nil {
		p.reader = streamBufReader.Get().(*bytes.Buffer)
		p.reader.Reset()
	}

	reader := p.reader
	if reader.Len()+len(data) > ParseMaxMessageLength {
		return fmt.Errorf("Message exceeds ParseMaxMessageLength")
	}

	reader.Write(data) // This should append to our already buffer

	unparsed := reader.Bytes() // TODO find a better way as we only want to move our offset
	for {
		err := p.parseSingle(reader, &unparsed)
		switch err {
		case ErrParseLineNoCRLF, ErrParseReadBodyIncomplete:
			reader.Reset()
			reader.Write(unparsed)
			return ErrParseSipPartial
		}

		if err != nil {
			return err
		}

		cb(p.msg)
		if len(unparsed) == 0 {
			// Maybe we need to check did empty spaces left
			break
		}

		p.reset()
		reader.Reset()
		reader.Write(unparsed)
		p.reader = reader
	}

	// IN all other cases do reset
	streamBufReader.Put(reader)
	p.reset()

	return
}

func (p *ParserStream) parseSingle(reader *bytes.Buffer, unparsed *[]byte) (err error) {
	// TODO change this with functions and store last function state
	switch p.state {
	case stateStartLine:
		startLine, err := nextLine(reader)

		if err != nil {
			if err == io.EOF {
				return ErrParseLineNoCRLF
			}
			return err
		}

		msg, err := parseLine(startLine)
		if err != nil {
			return err
		}

		*unparsed = reader.Bytes()
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
					return ErrParseLineNoCRLF
				}
				return err
			}

			if len(line) == 0 {
				// We've hit second CRLF
				break
			}

			err = p.headersParsers.parseMsgHeader(msg, line)
			if err != nil {
				return fmt.Errorf("%s: %w", err.Error(), ErrParseEOF)
				// log.Info().Err(err).Str("line", line).Msg("skip header due to error")
			}
			*unparsed = reader.Bytes()
		}
		*unparsed = reader.Bytes()

		h := msg.ContentLength()
		if h == nil {
			return nil
		}

		contentLength := int(*h)
		if contentLength <= 0 {
			return nil
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
		*unparsed = reader.Bytes()
		if err != nil {
			return fmt.Errorf("read message body failed: %w", err)
		}
		p.readContentLength += n

		if p.readContentLength < contentLength {
			return ErrParseReadBodyIncomplete
		}

		p.state = -1 // Clear state
		return nil
	default:
		return fmt.Errorf("Parser is in unknown state")
	}
}
