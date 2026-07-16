package gombus

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestUD2(t *testing.T) {
	f := RequestUD2(2)
	assert.Equal(t, "10 5b 02 5d 16", fmt.Sprintf("% x", f))
	assert.Equal(t, byte(2), f.A())
	assert.Equal(t, C(0x5b), f.C())
}

func TestSndNKE(t *testing.T) {
	f := SndNKE(3)
	assert.Equal(t, "10 40 03 43 16", fmt.Sprintf("% x", f))
	assert.Equal(t, byte(3), f.A())
	assert.Equal(t, C(0x40), f.C())
}

func TestApplicationReset(t *testing.T) {
	f := ApplicationReset(1)
	// L=03, C=73, A=01, CI=50; checksum = 0x73+0x01+0x50 = 0xC4
	assert.Equal(t, "68 03 03 68 73 01 50 c4 16", fmt.Sprintf("% x", f))
	assert.Equal(t, 3, f.L())
	assert.Equal(t, byte(1), f.A())
	assert.Equal(t, byte(0x50), f.CI())
}

// TestSendUD2 asserts the full frame. Asserting only L/A/CI let the ID bytes
// sit at 00 00 00 00, which selects the slave whose identification number is
// literally 00000000 rather than wildcarding every slave.
func TestSendUD2(t *testing.T) {
	f := SendUD2()
	assert.Equal(t, "68 0b 0b 68 73 fd 52 ff ff ff ff ff ff ff ff ba 16",
		fmt.Sprintf("% x", f))
	assert.Equal(t, 11, f.L())
	assert.Equal(t, byte(0xFD), f.A())
	assert.Equal(t, byte(0x52), f.CI())
}

func TestSetPrimaryUsingSecondary(t *testing.T) {
	tests := []struct {
		name      string
		secondary uint64
		primary   uint8
		want      string
	}{
		{
			name:      "8 digit secondary",
			secondary: 19004636,
			primary:   2,
			want:      "68 0e 0e 68 73 fd 51 36 46 00 19 ff ff ff ff 01 7a 02 cf 16",
		},
		{
			name:      "largest secondary that fits 8 BCD digits",
			secondary: 99999999,
			primary:   1,
			want:      "68 0e 0e 68 73 fd 51 99 99 99 99 ff ff ff ff 01 7a 01 9d 16",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := SetPrimaryUsingSecondary(tc.secondary, tc.primary)
			require.NoError(t, err)
			assert.Equal(t, tc.want, fmt.Sprintf("% x", f))
		})
	}
}

// TestSetPrimaryUsingSecondaryRejects covers the two ways this builder used to
// produce a frame that writes a primary address into the wrong slave, or into
// the right slave in a way that bricks its addressing.
func TestSetPrimaryUsingSecondaryRejects(t *testing.T) {
	tests := []struct {
		name      string
		secondary uint64
		primary   uint8
	}{
		// uintToBCD used to truncate: 123456789 became BCD 89 67 45 23, which
		// round-trips to 23456789 and addresses a different meter.
		{name: "9 digit secondary truncates", secondary: 123456789, primary: 2},
		{name: "smallest 9 digit secondary", secondary: 100000000, primary: 2},
		{name: "secondary overflow", secondary: ^uint64(0), primary: 2},
		{name: "primary 0 unconfigured", secondary: 19004636, primary: 0},
		{name: "primary 251 reserved", secondary: 19004636, primary: 251},
		{name: "primary 253 secondary addressing", secondary: 19004636, primary: 0xFD},
		{name: "primary 254 broadcast with reply", secondary: 19004636, primary: 0xFE},
		{name: "primary 255 broadcast", secondary: 19004636, primary: 0xFF},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := SetPrimaryUsingSecondary(tc.secondary, tc.primary)
			require.Error(t, err)
			assert.Nil(t, f)
		})
	}
}

// TestSetPrimaryUsingPrimary covers both arguments. They take opposite rules:
// oldPrimary is a destination, so broadcast 0xFE is legal there, while
// newPrimary is written into the slave, so 0xFE is rejected. wantErrArg names
// the argument the error must blame, so a caller can tell them apart.
func TestSetPrimaryUsingPrimary(t *testing.T) {
	const (
		errOld = "current primary address"
		errNew = "new primary address"
	)

	tests := []struct {
		name       string
		oldPrimary uint8
		newPrimary uint8
		want       string
		wantErrArg string
	}{
		// oldPrimary as a destination: 1..250, 253 and 254 are legal.
		{
			name:       "old primary 1 lower bound",
			oldPrimary: 1,
			newPrimary: 44,
			want:       "68 06 06 68 73 01 51 01 7a 2c 6c 16",
		},
		{
			name:       "old primary 250 upper bound",
			oldPrimary: 250,
			newPrimary: 44,
			want:       "68 06 06 68 73 fa 51 01 7a 2c 65 16",
		},
		{
			name:       "old primary 253 secondary addressing destination",
			oldPrimary: 0xFD,
			newPrimary: 44,
			want:       "68 06 06 68 73 fd 51 01 7a 2c 68 16",
		},
		{
			// The point of the split. Broadcast-with-reply is the standard
			// way to set the address of a single new meter on its own bus, so
			// 254 must be accepted HERE while rejected as newPrimary below.
			name:       "old primary 254 broadcast with reply is legal",
			oldPrimary: 0xFE,
			newPrimary: 44,
			want:       "68 06 06 68 73 fe 51 01 7a 2c 69 16",
		},
		{
			name:       "old primary 0 unconfigured",
			oldPrimary: 0,
			newPrimary: 44,
			wantErrArg: errOld,
		},
		{
			name:       "old primary 251 reserved",
			oldPrimary: 251,
			newPrimary: 44,
			wantErrArg: errOld,
		},
		{
			name:       "old primary 255 broadcast without reply",
			oldPrimary: 0xFF,
			newPrimary: 44,
			wantErrArg: errOld,
		},

		// newPrimary is written into the slave: strict 1..250.
		{
			name:       "new primary 1 lower bound",
			oldPrimary: 1,
			newPrimary: 1,
			want:       "68 06 06 68 73 01 51 01 7a 01 41 16",
		},
		{
			name:       "new primary 250 upper bound",
			oldPrimary: 1,
			newPrimary: 250,
			want:       "68 06 06 68 73 01 51 01 7a fa 3a 16",
		},
		{
			name:       "new primary 0 unconfigured",
			oldPrimary: 1,
			newPrimary: 0,
			wantErrArg: errNew,
		},
		{
			name:       "new primary 251 reserved",
			oldPrimary: 1,
			newPrimary: 251,
			wantErrArg: errNew,
		},
		{
			name:       "new primary 253 secondary addressing",
			oldPrimary: 1,
			newPrimary: 0xFD,
			wantErrArg: errNew,
		},
		{
			// 0xFE as a destination is fine, but writing it into the slave
			// makes it answer every broadcast and become unaddressable.
			name:       "new primary 254 broadcast with reply is not assignable",
			oldPrimary: 44,
			newPrimary: 0xFE,
			wantErrArg: errNew,
		},
		{
			name:       "new primary 255 broadcast",
			oldPrimary: 1,
			newPrimary: 0xFF,
			wantErrArg: errNew,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := SetPrimaryUsingPrimary(tc.oldPrimary, tc.newPrimary)
			if tc.wantErrArg != "" {
				require.ErrorIs(t, err, ErrInvalidPrimaryID)
				assert.Contains(t, err.Error(), tc.wantErrArg)
				assert.Nil(t, f)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, fmt.Sprintf("% x", f))
		})
	}
}
