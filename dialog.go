package sipgo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
)

var (
	ErrDialogOutsideDialog   = errors.New("Call/Transaction Outside Dialog")
	ErrDialogDoesNotExists   = errors.New("Call/Transaction Does Not Exist")
	ErrDialogInviteNoContact = errors.New("No Contact header")
	ErrDialogCanceled        = errors.New("Dialog canceled")
	ErrDialogInvalidCseq     = errors.New("Invalid CSEQ number")
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
	lastCSeqNo atomic.Uint32

	// InviteResponse is last response received or sent. It is not thread safe!
	// Use it only as read only and do not change values
	InviteResponse *sip.Response

	state atomic.Int32

	ctx    context.Context
	cancel context.CancelFunc

	onStatePointer atomic.Pointer[DialogStateFn]

	// store user values
	values sync.Map
}

// Init setups dialog state
func (d *Dialog) Init() {
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.state = atomic.Int32{}
	d.lastCSeqNo = atomic.Uint32{}

	cseq := d.InviteRequest.CSeq().SeqNo
	d.lastCSeqNo.Store(cseq)
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
		d.cancel()
	}

	if f := d.onStatePointer.Load(); f != nil {
		cb := *f
		cb(s)
	}
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

func (d *Dialog) Store(key string, value any) {
	d.values.Store(key, value)
}

func (d *Dialog) Load(key string) (any, bool) {
	return d.values.Load(key)
}

func (d *Dialog) Delete(key string) {
	d.values.Delete(key)
}
