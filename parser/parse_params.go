package parser

import (
	"github.com/emiago/sipgo/sip"
)

const (
	paramsStateNone = iota
	paramsStateKey
	paramsStateEqual
	paramsStateValue
	paramsStateQuote
)

func UnmarshalParams(s string, seperator rune, ending rune, p sip.HeaderParams) (n int, err error) {
	var start, sep, quote int = 0, 0, -1
	state := paramsStateKey
	n = len(s)
	for i, c := range s {
		if c == ending {
			n = i
			break
		}

		switch state {
		case paramsStateKey:
			sep = 0
			start = i
			state = paramsStateEqual

		case paramsStateEqual:
			if c == seperator {
				// Add support for empty values
				p.Add(s[start:i], "")
				state = paramsStateKey
				continue
			}

			if c != '=' {
				continue
			}

			sep = i
			state = paramsStateValue

		case paramsStateValue:
			switch c {
			case '"':
				state = paramsStateQuote
				quote = i
			case seperator:
				p.Add(s[start:sep], s[sep+1:i])
				start = sep + 1
				state = paramsStateKey
			}
		case paramsStateQuote:
			if c != '"' {
				//End quoute
				continue
			}
			p.Add(s[start:], s[quote+1:i])
			state = paramsStateKey
		}
	}

	// Do the last one
	if sep > 0 && n >= 0 && (start < sep) {
		p.Add(s[start:sep], s[sep+1:n])
	}
	// No seperator
	if sep == 0 && start < n && n >= 0 {
		p.Add(s[start:], "")
	}

	return n, nil
}
