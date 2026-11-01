package sip

import (
	"errors"
	"syscall"
)

func isRecvMsgSizeErr(err error) bool {
	const wsaEMSGSIZE syscall.Errno = 10040
	return errors.Is(err, wsaEMSGSIZE)
}
