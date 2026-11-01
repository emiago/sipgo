//go:build !windows

package sip

func isRecvMsgSizeErr(error) bool { return false }
