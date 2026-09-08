package sipgo

import (
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
)

func TestDialogState(t *testing.T) {
	inv, _, _ := createTestInvite(t, "sip:nowhere", "udp", "127.0.0.1")
	d := Dialog{
		InviteRequest: inv,
	}
	d.Init()

	ch := d.StateRead()
	ch2 := d.StateRead()
	go func() {
		d.setState(sip.DialogStateEstablished)
		d.setState(sip.DialogStateConfirmed)
		d.setState(sip.DialogStateEnded)
	}()

	assert.Equal(t, sip.DialogStateEstablished, <-ch)
	assert.Equal(t, sip.DialogStateConfirmed, <-ch)
	assert.Equal(t, sip.DialogStateEnded, <-ch)

	assert.Equal(t, sip.DialogStateEstablished, <-ch2)
	assert.Equal(t, sip.DialogStateConfirmed, <-ch2)
	assert.Equal(t, sip.DialogStateEnded, <-ch2)

}

func BenchmarkDialogSettingState(b *testing.B) {
	inv, _, _ := createTestInvite(b, "sip:nowhere", "udp", "127.0.0.1")
	d := Dialog{
		InviteRequest: inv,
	}
	d.Init()
	mustBeCalled := false
	d.OnState(func(s sip.DialogState) {
		mustBeCalled = true
	})
	for i := 0; i < b.N; i++ {
		d.setState(sip.DialogStateConfirmed)
	}

	if !mustBeCalled {
		b.Error("On state not called")
	}

}
