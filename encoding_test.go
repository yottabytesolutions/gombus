package gombus

import (
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUintToBCD(t *testing.T) {
	cases := []struct {
		name    string
		value   uint64
		size    int
		want    string
		wantErr bool
	}{
		{name: "12345678", value: 12345678, size: 4, want: "78 56 34 12"},
		{name: "zero", value: 0, size: 4, want: "00 00 00 00"},
		{name: "fills every digit", value: 99999999, size: 4, want: "99 99 99 99"},
		{name: "leading zeroes kept", value: 1234, size: 4, want: "34 12 00 00"},
		{name: "single byte", value: 42, size: 1, want: "42"},
		// Truncating here would address a different meter on a live bus.
		{name: "overflow errors", value: 123456789, size: 4, wantErr: true},
		{name: "overflow by one digit", value: 100, size: 1, wantErr: true},
		{name: "zero size", value: 0, size: 0, wantErr: true},
		{name: "negative size", value: 0, size: -1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := uintToBCD(tc.value, tc.size)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, fmt.Sprintf("% x", b))
		})
	}
}

func TestBcdSigned(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "positive", in: "78563412", want: 12345678},
		{name: "zero", in: "00000000", want: 0},
		{name: "single byte", in: "99", want: 99},
		// 0xF in the most significant nibble is the type A sign marker.
		{name: "negative", in: "34120000F0", want: -1234},
		{name: "negative 4 byte", in: "341200F0", want: -1234},
		{name: "negative single byte", in: "F9", want: -9},
		{name: "negative zero", in: "F0", want: 0},
		{name: "negative max digits", in: "999999F9", want: -9999999},
		// Nibbles A-F outside the sign position are not BCD.
		{name: "invalid high nibble", in: "AB", wantErr: true},
		{name: "invalid low nibble", in: "0A", wantErr: true},
		{name: "sign nibble not in msb", in: "00F0FF00", wantErr: true},
		{name: "0xF as low nibble", in: "0F", wantErr: true},
		{name: "invalid in middle byte", in: "12CD34", wantErr: true},
		// The all-F "not available" marker must not decode as a large negative.
		{name: "all F marker", in: "FFFFFFFF", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := hex.DecodeString(tc.in)
			require.NoError(t, err)
			v, err := bcdSigned(b)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, v)
		})
	}
}

func TestBcdMagnitude(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    uint64
		wantErr bool
	}{
		{name: "positive", in: "78563412", want: 12345678},
		{name: "zero", in: "00000000", want: 0},
		{name: "single byte", in: "99", want: 99},
		{name: "max digits", in: "9999999999999999", want: 9999999999999999},
		// No sign handling here: the LVAR byte carries the sign, so a 0xF
		// nibble is just invalid BCD.
		{name: "sign nibble rejected", in: "341200F0", wantErr: true},
		{name: "invalid high nibble", in: "AB", wantErr: true},
		{name: "invalid low nibble", in: "0A", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := hex.DecodeString(tc.in)
			require.NoError(t, err)
			v, err := bcdMagnitude(b)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, v)
		})
	}
}

func TestBCDRoundTrip(t *testing.T) {
	cases := []struct {
		value uint64
		size  int
	}{
		{value: 0, size: 4},
		{value: 1, size: 4},
		{value: 1234, size: 4},
		{value: 12345678, size: 4},
		{value: 99999999, size: 4},
		{value: 42, size: 1},
		{value: 123456789012, size: 6},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d_in_%d", tc.value, tc.size), func(t *testing.T) {
			b, err := uintToBCD(tc.value, tc.size)
			require.NoError(t, err)

			mag, err := bcdMagnitude(b)
			require.NoError(t, err)
			assert.Equal(t, tc.value, mag)

			signed, err := bcdSigned(b)
			require.NoError(t, err)
			assert.Equal(t, int64(tc.value), signed)
		})
	}
}

func TestCheckKthBitSet(t *testing.T) {
	cases := []struct {
		n, k int
		want bool
	}{
		{0x80, 7, true},
		{0xf, 7, false},
		{0x2a, 7, false},
		{0x40, 6, true},
		{0x01, 0, true},
		{0x00, 0, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("0x%x_bit%d", tc.n, tc.k), func(t *testing.T) {
			assert.Equal(t, tc.want, checkKthBitSet(tc.n, tc.k))
		})
	}
}

func TestUintLE(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want uint64
	}{
		{name: "empty", in: nil, want: 0},
		{name: "one byte", in: []byte{0xFF}, want: 255},
		{name: "two bytes", in: []byte{0xCD, 0x12}, want: 0x12CD},
		// Raw bitfields keep their top bit as data, not as a sign.
		{name: "16 bit all ones", in: []byte{0xFF, 0xFF}, want: 65535},
		{name: "32 bit not available marker", in: []byte{0xFF, 0xFF, 0xFF, 0xFF}, want: 4294967295},
		{name: "32 bit error flag top bit", in: []byte{0x00, 0x00, 0x00, 0x80}, want: 2147483648},
		{name: "24 bit", in: []byte{0x15, 0x31, 0x00}, want: 12565},
		{name: "48 bit all ones", in: []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, want: 281474976710655},
		{
			name: "64 bit all ones",
			in:   []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			want: math.MaxUint64,
		},
		{
			name: "ignores bytes past the eighth",
			in:   []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0xFF},
			want: 0x0807060504030201,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, uintLE(tc.in))
		})
	}
}

func TestIntLE(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want int64
	}{
		// A zero-width field has no sign bit to extend from.
		{name: "empty", in: nil, want: 0},
		{name: "zero", in: []byte{0x00, 0x00}, want: 0},

		{name: "8 bit most positive", in: []byte{0x7F}, want: 127},
		{name: "8 bit minus one", in: []byte{0xFF}, want: -1},
		{name: "8 bit most negative", in: []byte{0x80}, want: -128},

		// The inline binary.LittleEndian.Uint16 call sites missed this one.
		{name: "16 bit minus ten degrees", in: []byte{0x9C, 0xFF}, want: -100},
		{name: "16 bit positive", in: []byte{0xCD, 0x12}, want: 0x12CD},
		{name: "16 bit most positive", in: []byte{0xFF, 0x7F}, want: 32767},
		{name: "16 bit minus one", in: []byte{0xFF, 0xFF}, want: -1},
		{name: "16 bit most negative", in: []byte{0x00, 0x80}, want: -32768},

		{name: "24 bit small positive", in: []byte{0x15, 0x31, 0x00}, want: 12565},
		{name: "24 bit most positive", in: []byte{0xFF, 0xFF, 0x7F}, want: 8388607},
		{name: "24 bit minus one", in: []byte{0xFF, 0xFF, 0xFF}, want: -1},
		{name: "24 bit most negative", in: []byte{0x00, 0x00, 0x80}, want: -8388608},
		{name: "24 bit minus hundred", in: []byte{0x9C, 0xFF, 0xFF}, want: -100},

		{name: "32 bit positive", in: []byte{0xCD, 0xAB, 0x34, 0x12}, want: 0x1234ABCD},
		{name: "32 bit most positive", in: []byte{0xFF, 0xFF, 0xFF, 0x7F}, want: 2147483647},
		{name: "32 bit minus one", in: []byte{0xFF, 0xFF, 0xFF, 0xFF}, want: -1},
		{name: "32 bit most negative", in: []byte{0x00, 0x00, 0x00, 0x80}, want: -2147483648},
		{name: "32 bit minus hundred", in: []byte{0x9C, 0xFF, 0xFF, 0xFF}, want: -100},

		// The 5-byte LVAR width the old helper set read as unsigned.
		{name: "40 bit minus one", in: []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, want: -1},
		{name: "40 bit most positive", in: []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, want: 549755813887},
		{name: "40 bit most negative", in: []byte{0x00, 0x00, 0x00, 0x00, 0x80}, want: -549755813888},

		{name: "48 bit 32 bit boundary", in: []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00}, want: 4294967295},
		{name: "48 bit most positive", in: []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, want: 140737488355327},
		{name: "48 bit minus one", in: []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, want: -1},
		{name: "48 bit most negative", in: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x80}, want: -140737488355328},
		{name: "48 bit minus hundred", in: []byte{0x9C, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, want: -100},

		{
			name: "64 bit positive",
			in:   []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			want: 0x0807060504030201,
		},
		{
			name: "64 bit most positive",
			in:   []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F},
			want: math.MaxInt64,
		},
		{
			name: "64 bit minus one",
			in:   []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			want: -1,
		},
		{
			name: "64 bit most negative",
			in:   []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80},
			want: math.MinInt64,
		},
		{
			name: "ignores bytes past the eighth",
			in:   []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0xFF},
			want: 0x0807060504030201,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, intLE(tc.in))
		})
	}
}

// intLE and uintLE must agree wherever the sign bit is clear.
func TestIntLEMatchesUintLEWhenPositive(t *testing.T) {
	cases := [][]byte{
		{0x00},
		{0x7F},
		{0xCD, 0x12},
		{0x15, 0x31, 0x00},
		{0xCD, 0xAB, 0x34, 0x12},
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
	}
	for _, in := range cases {
		t.Run(fmt.Sprintf("% x", in), func(t *testing.T) {
			assert.Equal(t, int64(uintLE(in)), intLE(in))
		})
	}
}

func TestBytesToFloat32(t *testing.T) {
	cases := []struct {
		in   []byte
		want float32
	}{
		{[]byte{0x00, 0x00, 0x00, 0x00}, 0.0},
		{[]byte{0x00, 0x00, 0x80, 0x3F}, 1.0},
		{[]byte{0x00, 0x00, 0x00, 0x40}, 2.0},
		{[]byte{0xCD, 0xCC, 0x8C, 0x3F}, 1.1},
	}
	for _, tc := range cases {
		v, err := bytesToFloat32(tc.in)
		require.NoError(t, err)
		assert.InDelta(t, float64(tc.want), float64(v), 1e-6)
	}
	_, err := bytesToFloat32([]byte{0x00, 0x00, 0x00})
	assert.Error(t, err)
}

func TestDecodeASCIIReversesBytes(t *testing.T) {
	assert.Equal(t, "olleH", decodeASCII([]byte("Hello")))
	assert.Empty(t, decodeASCII(nil))
}

func TestCalcCheckSum(t *testing.T) {
	// Sum of bytes truncated to one byte.
	assert.Equal(t, byte(0x10), calcCheckSum([]byte{0x01, 0x02, 0x03, 0x04, 0x06}))
	assert.Equal(t, byte(0x16), calcCheckSum([]byte{0x01, 0x02, 0x03, 0x04, 0x0c}))
	// Wraparound.
	assert.Equal(t, byte(0x00), calcCheckSum([]byte{0x80, 0x80}))
}
