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

// GenerateBranch returns random unique branch ID.
func GenerateBranch() string {
	return strings.Join([]string{
		RFC3261BranchMagicCookie,
		RandString(32),
	}, ".")
}

// DefaultPort returns protocol default port by network.
func DefaultPort(protocol string) int {
	switch strings.ToLower(protocol) {
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
