package sip

import (
	"fmt"
	"strings"
)

const (
	RFC3261BranchMagicCookie = "z9hG4bK"
)

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

// MakeDialogIDFromMessage creates dialog ID of message.
// Use UASReadRequestDialogID UACReadRequestDialogID for more specific
// returns error if callid or to tag or from tag does not exists
func MakeDialogIDFromRequest(msg *Request) (string, error) {
	return UASReadRequestDialogID(msg)
}

// MakeDialogIDFromResponse creates dialog ID of message.
// returns error if callid or to tag or from tag does not exists
func MakeDialogIDFromResponse(msg *Response) (string, error) {
	var callID, toTag, fromTag string = "", "", ""
	if err := getDialogIDFromMessage(msg, &callID, &toTag, &fromTag); err != nil {
		return "", err
	}
	return MakeDialogID(callID, toTag, fromTag), nil
}

// UASReadRequestDialogID creates dialog ID of message if receiver has UAS role.
// returns error if callid or to tag or from tag does not exists
func UASReadRequestDialogID(msg *Request) (string, error) {
	var callID, toTag, fromTag string = "", "", ""
	if err := getDialogIDFromMessage(msg, &callID, &toTag, &fromTag); err != nil {
		return "", err
	}
	return MakeDialogID(callID, toTag, fromTag), nil
}

// UACReadRequestDialogID creates dialog ID of message if receiver has UAC role.
// returns error if callid or to tag or from tag does not exists
func UACReadRequestDialogID(msg *Request) (string, error) {
	var callID, toTag, fromTag string = "", "", ""
	if err := getDialogIDFromMessage(msg, &callID, &toTag, &fromTag); err != nil {
		return "", err
	}
	return MakeDialogID(callID, fromTag, toTag), nil
}

func getDialogIDFromMessage(msg Message, callId, toHeaderTag, fromHeaderTag *string) error {
	callID := msg.CallID()
	if callID == nil {
		return fmt.Errorf("missing Call-ID header")
	}

	to := msg.To()
	if to == nil {
		return fmt.Errorf("missing To header")
	}

	toTag, ok := to.Params.Get("tag")
	if !ok {
		return fmt.Errorf("missing tag param in To header")
	}

	from := msg.From()
	if from == nil {
		return fmt.Errorf("missing From header")
	}

	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return fmt.Errorf("missing tag param in From header")
	}
	*callId = string(*callID)
	*toHeaderTag = toTag
	*fromHeaderTag = fromTag
	return nil
}

func MakeDialogID(callID, innerID, externalID string) string {
	return strings.Join([]string{callID, innerID, externalID}, TxSeperator)
}
