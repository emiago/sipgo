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

// PartialParser extracts full SIP messages from a stream
type PartialParser interface {
	Process(data []byte) error
	OnMessage(func(msg []byte))
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

// MakeDialogIDFromMessage creates dialog ID of message.
// returns error if callid or to tag or from tag does not exists
func MakeDialogIDFromMessage(msg Message) (string, error) {
	callID, ok := msg.CallID()
	if !ok {
		return "", fmt.Errorf("missing Call-ID header")
	}

	to, ok := msg.To()
	if !ok {
		return "", fmt.Errorf("missing To header")
	}

	toTag, ok := to.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("missing tag param in To header")
	}

	from, ok := msg.From()
	if !ok {
		return "", fmt.Errorf("missing From header")
	}

	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("missing tag param in From header")
	}

	return MakeDialogID(string(*callID), toTag, fromTag), nil
}

func MakeDialogID(callID, innerID, externalID string) string {
	return strings.Join([]string{callID, innerID, externalID}, "__")
}
