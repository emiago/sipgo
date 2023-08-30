package sip

import (
	"fmt"
	"strings"
)

const (
	MTU uint = 1500

	DefaultHost     = "127.0.0.1"
	DefaultProtocol = "UDP"

	DefaultUdpPort int = 5060
	DefaultTcpPort int = 5060
	DefaultTlsPort int = 5061
	DefaultWsPort  int = 80
	DefaultWssPort int = 443

	RFC3261BranchMagicCookie = "z9hG4bK"
)

// The buffer size of the parser input channel.
// Parser is interface for decoding full message into sip message
type Parser interface {
	ParseSIP(data []byte) (Message, error)
}

// GenerateBranch returns random unique branch ID.
func GenerateBranch() string {
	return GenerateBranchN(16)
}

// GenerateBranchN returns random unique branch ID in format MagicCookie.<n chars>
func GenerateBranchN(n int) string {
	sb := &strings.Builder{}
	generateBranchStringWrite(sb, n)
	return sb.String()
}

func generateBranchStringWrite(sb *strings.Builder, n int) {
	sb.Grow(len(RFC3261BranchMagicCookie) + n + 1)
	sb.WriteString(RFC3261BranchMagicCookie)
	sb.WriteString(".")
	RandStringBytesMask(sb, n)
}

func GenerateTagN(n int) string {
	sb := &strings.Builder{}
	RandStringBytesMask(sb, n)
	return sb.String()
}

// DefaultPort returns transport default port by network.
func DefaultPort(transport string) int {
	switch ASCIIToLower(transport) {
	case "tls":
		return DefaultTlsPort
	case "tcp":
		return DefaultTcpPort
	case "udp":
		return DefaultUdpPort
	case "ws":
		return DefaultWsPort
	case "wss":
		return DefaultWssPort
	default:
		return DefaultTcpPort
	}
}

func MakeDialogIDFromRequest(msg *Request) (string, error) {
	var callID, innerID, externalID string = "", "", ""
	if err := getDialogIDFromMessage(msg, &callID, &innerID, &externalID); err != nil {
		return "", err
	}
	return MakeDialogID(callID, innerID, externalID), nil
}

func MakeDialogIDFromResponse(msg *Response) (string, error) {
	var callID, innerID, externalID string = "", "", ""
	if err := getDialogIDFromMessage(msg, &callID, &externalID, &innerID); err != nil {
		return "", err
	}
	return MakeDialogID(callID, innerID, externalID), nil
}

// MakeDialogIDFromMessage creates dialog ID of message.
// returns error if callid or to tag or from tag does not exists
// Deprecated! Will be removed
func MakeDialogIDFromMessage(msg Message) (string, error) {
	switch m := msg.(type) {
	case *Request:
		return MakeDialogIDFromRequest(m)
	case *Response:
		return MakeDialogIDFromResponse(m)
	}
	return "", fmt.Errorf("unknown message format")
}

func getDialogIDFromMessage(msg Message, callId, innerId, externalId *string) error {
	callID, ok := msg.CallID()
	if !ok {
		return fmt.Errorf("missing Call-ID header")
	}

	to, ok := msg.To()
	if !ok {
		return fmt.Errorf("missing To header")
	}

	toTag, ok := to.Params.Get("tag")
	if !ok {
		return fmt.Errorf("missing tag param in To header")
	}

	from, ok := msg.From()
	if !ok {
		return fmt.Errorf("missing From header")
	}

	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return fmt.Errorf("missing tag param in From header")
	}
	*callId = string(*callID)
	*innerId = toTag
	*externalId = fromTag
	return nil
}

func MakeDialogID(callID, innerID, externalID string) string {
	return strings.Join([]string{callID, innerID, externalID}, "__")
}
