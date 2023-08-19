package parser

import (
	"bytes"
	"errors"
	"strconv"
)

type partialMessageParserState int

const (
	Start   partialMessageParserState = 0
	TopLine                           = 1
	Headers                           = 2
	Content                           = 3
)

var (
	contentLengthHeader = bytes.NewBufferString("content-length:")
)

type PartialMessageParser struct {
	state partialMessageParserState

	inputBuffer_ *bytes.Buffer
	currentLine_ *bytes.Buffer

	hadContentLengthHeader bool
	contentBytesLeft       int
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (p *PartialMessageParser) reset() {
	p.state = Start
	p.hadContentLengthHeader = false
	p.contentBytesLeft = 0
	p.inputBuffer_.Reset()
	p.currentLine_.Reset()
}

func endsWithCRLF(buffer []byte) bool {
	return len(buffer) >= 2 && buffer[len(buffer)-1] == '\n' && buffer[len(buffer)-2] == '\r'
}

func endsWithDoubleCRLF(buffer []byte) bool {
	return len(buffer) >= 4 &&
		buffer[len(buffer)-1] == '\n' &&
		buffer[len(buffer)-2] == '\r' &&
		buffer[len(buffer)-3] == '\n' &&
		buffer[len(buffer)-4] == '\r'
}

func isContentLengthHeader(buffer []byte) bool {
	if len(buffer) < contentLengthHeader.Len() {
		return false
	}

	return bytes.EqualFold(contentLengthHeader.Bytes(), buffer[0:contentLengthHeader.Len()])
}

func (p *PartialMessageParser) Process(data []byte) (messages [][]byte, err error) {
	i := 0

	if p.currentLine_ == nil {
		p.currentLine_ = bytes.NewBuffer(nil)
	}

	if p.inputBuffer_ == nil {
		p.inputBuffer_ = bytes.NewBuffer(nil)
	}

	for i < len(data) {
		dataByte := data[i]
		switch p.state {
		case Start:
			{
				if dataByte != '\r' && dataByte != '\n' {
					p.state = TopLine
				} else {
					i++
				}
			}
		case TopLine:
			{
				p.inputBuffer_.WriteByte(dataByte)
				i++

				if dataByte == '\n' && endsWithCRLF(p.inputBuffer_.Bytes()) {
					p.state = Headers
				}
			}
		case Headers:
			{
				p.inputBuffer_.WriteByte(dataByte)
				p.currentLine_.WriteByte(dataByte)

				i++

				if dataByte == '\n' && endsWithCRLF(p.currentLine_.Bytes()) {
					if isContentLengthHeader(p.currentLine_.Bytes()) {
						str := p.currentLine_.Bytes()
						str = str[contentLengthHeader.Len() : len(str)-2]
						str = bytes.TrimSpace(str)

						p.contentBytesLeft, err = strconv.Atoi(string(str))
						if err != nil {
							return nil, err
						}

						p.hadContentLengthHeader = true
					}

					p.currentLine_.Reset()

					// Each message header line must end with CRLF, and there must be one line containing only CRLF between headers and body
					if endsWithDoubleCRLF(p.inputBuffer_.Bytes()) {
						p.state = Content

						if !p.hadContentLengthHeader {
							return nil, errors.New("Missing Content-Length header")
						}
					}
				}

				if p.state == Content && p.contentBytesLeft == 0 {
					messages = append(messages, p.inputBuffer_.Bytes())
					p.reset()
				}
			}
		case Content:
			{
				if p.contentBytesLeft > 0 {
					bytesToConsume := min(p.contentBytesLeft, len(data)-i)
					p.inputBuffer_.Write(data[i : i+bytesToConsume])
					i += bytesToConsume
					p.contentBytesLeft -= bytesToConsume

				}

				if p.contentBytesLeft == 0 {
					messages = append(messages, p.inputBuffer_.Bytes())
					p.reset()
				}
			}
		}
	}

	return
}
