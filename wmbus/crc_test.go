package wmbus

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// crcReference computes the EN 13757-4 CRC by long division: the message
// followed by 16 zero bits, reduced modulo the generator polynomial, then
// complemented. It is deliberately a different construction from crc16, so
// the two agreeing is evidence rather than a tautology.
func crcReference(data []byte) uint16 {
	const generator = 0x10000 | crcPolynomial // x^16 term included

	var register uint32
	feed := func(bit uint32) {
		register = register<<1 | bit
		if register&0x10000 != 0 {
			register ^= generator
		}
	}
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			feed(uint32(b>>uint(i)) & 1)
		}
	}
	for range 16 {
		feed(0)
	}
	return ^uint16(register)
}

func TestCRC16CheckValue(t *testing.T) {
	// The published check value of CRC-16/EN-13757 over the ASCII digits
	// "123456789".
	require.Equal(t, uint16(0xC2B7), crc16([]byte("123456789")))
}

func TestCRC16MatchesLongDivision(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: []byte{}},
		{name: "single zero byte", data: []byte{0x00}},
		{name: "single high byte", data: []byte{0xFF}},
		{name: "check string", data: []byte("123456789")},
		{name: "link layer header", data: []byte{0x2E, 0x44, 0x2C, 0x2D, 0x12, 0x34, 0x56, 0x78, 0x1B, 0x07}},
		{name: "sixteen byte block", data: make([]byte, 16)},
		{name: "alternating bits", data: []byte{0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55}},
	}

	for _, tt := range tests {
		t.Run(
			tt.name, func(t *testing.T) {
				require.Equal(t, crcReference(tt.data), crc16(tt.data))
			},
		)
	}
}

func TestCheckCRC(t *testing.T) {
	block := []byte{0x2E, 0x44, 0x2C, 0x2D, 0x12, 0x34, 0x56, 0x78, 0x1B, 0x07}
	sum := crc16(block)
	good := []byte{byte(sum >> 8), byte(sum)}
	bad := []byte{good[0], good[1] ^ 0x01}

	require.NoError(t, checkCRC(block, good))
	require.ErrorIs(t, checkCRC(block, bad), ErrCRC)
}
