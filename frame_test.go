package gombus

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fcbFcvFrame captures the FCB/FCV mutator interface common to ShortFrame
// and LongFrame, so the round-trip test below can exercise both.
type fcbFcvFrame interface {
	SetFCB()
	ClearFCB()
	SetFCV()
	ClearFCV()
	C() C
}

func assertFCBFCVRoundTrip(t *testing.T, f fcbFcvFrame) {
	t.Helper()
	f.SetFCB()
	assert.True(t, f.C().FCB())
	f.ClearFCB()
	assert.False(t, f.C().FCB())

	f.SetFCV()
	assert.True(t, f.C().FCV())
	f.ClearFCV()
	assert.False(t, f.C().FCV())
}

func TestShortFrameAccessors(t *testing.T) {
	f := NewShortFrame()
	f.SetAddress(0x05)
	f.SetC(0x40)
	f.SetChecksum()

	assert.Equal(t, byte(0x05), f.A())
	assert.Equal(t, C(0x40), f.C())
	assert.Equal(t, "10 40 05 45 16", fmt.Sprintf("% x", f))
}

func TestShortFrameFCBFCV(t *testing.T) {
	f := NewShortFrame()
	f.SetC(0x40)
	assertFCBFCVRoundTrip(t, f)
}

func TestLongFrameAccessors(t *testing.T) {
	f := LongFrame{0x68, 0x06, 0x06, 0x68, 0x73, 0x01, 0x50, 0x01, 0x7a, 0x03, 0x00, 0x16}
	f.SetLength()
	f.SetChecksum()

	assert.Equal(t, 6, f.L())
	assert.Equal(t, byte(0x01), f.A())
	assert.Equal(t, byte(0x50), f.CI())
	assert.Equal(t, C(0x73), f.C())
}

func TestLongFrameFCBFCV(t *testing.T) {
	f := LongFrame{0x68, 0x06, 0x06, 0x68, 0x73, 0x01, 0x50, 0x01, 0x7a, 0x03, 0x00, 0x16}
	assertFCBFCVRoundTrip(t, f)
}

func TestCBitsRoundTrip(t *testing.T) {
	cases := []struct {
		c        C
		fcb, fcv bool
	}{
		{0x00, false, false},
		{ControlMaskFcb, true, false},
		{ControlMaskFcv, false, true},
		{ControlMaskFcb | ControlMaskFcv, true, true},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.fcb, tc.c.FCB(), "FCB for c=0x%x", byte(tc.c))
		assert.Equal(t, tc.fcv, tc.c.FCV(), "FCV for c=0x%x", byte(tc.c))
	}
}
