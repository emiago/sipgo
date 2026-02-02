package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
)

var (
	ErrDialogOutsideDialog   = errors.New("Call/Transaction Outside Dialog")
	ErrDialogDoesNotExists   = errors.New("Call/Transaction Does Not Exist")
	ErrDialogInviteNoContact = errors.New("no Contact header")
	ErrDialogInvalidCseq     = errors.New("invalid CSEQ number")
)

type ErrDialogResponse struct {
	Res *sip.Response
}

func (e ErrDialogResponse) Error() string {
	return fmt.Sprintf("Invite failed with response: %s", e.Res.StartLine())
}

type DialogStateFn func(s sip.DialogState)
type Dialog struct {
	ID string

	// InviteRequest is set when dialog is created. It is not thread safe!
	// Use it only as read only and use methods to change headers
	InviteRequest *sip.Request

	// lastCSeqNo is set for every request within dialog except ACK CANCEL
	lastCSeqNo   atomic.Uint32
	remoteCSeqNo atomic.Uint32

	// InviteResponse is last response received or sent. It is not thread safe!
	// Use it only as read only and do not change values
	InviteResponse *sip.Response

	state atomic.Int32

	ctx    context.Context
	cancel context.CancelCauseFunc

	onStatePointer atomic.Pointer[DialogStateFn]
}

// Init setups dialog state
func (d *Dialog) Init() {
	d.ctx, d.cancel = context.WithCancelCause(context.Background())
	d.state = atomic.Int32{}
	d.lastCSeqNo = atomic.Uint32{}

	// We may have sequence number initialized
	if cseq := d.InviteRequest.CSeq(); cseq != nil {
		d.lastCSeqNo.Store(cseq.SeqNo)
		d.remoteCSeqNo.Store(cseq.SeqNo)
	}
	d.onStatePointer = atomic.Pointer[DialogStateFn]{}
}

func (d *Dialog) OnState(f DialogStateFn) {
	for current := d.onStatePointer.Load(); current != nil; current = d.onStatePointer.Load() {
		cb := *current
		newCb := func(s sip.DialogState) {
			f(s)
			cb(s)
		}
		newCBState := DialogStateFn(newCb)
		if d.onStatePointer.CompareAndSwap(current, &newCBState) {
			return
		}
	}
	d.onStatePointer.Store(&f)
}

func (d *Dialog) InitWithState(s sip.DialogState) {
	d.Init()
	d.state.Store(int32(s))
}

func (d *Dialog) setState(s sip.DialogState) {
	old := d.state.Swap(int32(s))
	if old == int32(s) {
		// Safety
		return
	}

	if s == sip.DialogStateEnded {
		d.cancel(nil)
	}

	if f := d.onStatePointer.Load(); f != nil {
		cb := *f
		cb(s)
	}
}

// endWithCause sets dialog state ended and place context cause error
// Experimental
func (d *Dialog) endWithCause(err error) {
	s := sip.DialogStateEnded
	old := d.state.Swap(int32(s))
	if old == int32(s) {
		// Safety
		return
	}
	d.cancel(err)

	if f := d.onStatePointer.Load(); f != nil {
		cb := *f
		cb(s)
	}
}

// Err returns error that caused dialog termination
func (d *Dialog) err() error {
	return context.Cause(d.Context())
}

func (d *Dialog) LoadState() sip.DialogState {
	return sip.DialogState(d.state.Load())
}

func (d *Dialog) StateRead() <-chan sip.DialogState {
	ch := make(chan sip.DialogState, 5)
	d.OnState(func(s sip.DialogState) {
		select {
		case ch <- s:
		default:
		}
	})

	return ch
}

func (d *Dialog) CSEQ() uint32 {
	return d.lastCSeqNo.Load()
}

func (d *Dialog) Context() context.Context {
	return d.ctx
}

func (d *Dialog) ReadRequest(req *sip.Request, tx sip.ServerTransaction) error {
	// UAS role of dialog SHOULD be
	// prepared to receive and process requests with CSeq values more than
	// one higher than the previous received request.
	oldCseq := d.remoteCSeqNo.Load()
	newSeqNo := req.CSeq().SeqNo
	if newSeqNo < oldCseq {
		return ErrDialogInvalidCseq
	}

	// 	A UAS that receives a second INVITE before it sends the final
	//    response to a first INVITE with a lower CSeq sequence number on the
	//    same dialog MUST return a 500 (Server Internal Error) respons
	if !d.remoteCSeqNo.CompareAndSwap(oldCseq, newSeqNo) {
		return ErrDialogInvalidCseq
	}

	return nil
}
