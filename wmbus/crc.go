package wmbus

import (
	"encoding/binary"
	"fmt"
)

// crcPolynomial is the EN 13757-4 CRC-16 generator polynomial
// x^16 + x^13 + x^12 + x^11 + x^10 + x^8 + x^6 + x^5 + x^2 + 1.
const crcPolynomial = 0x3D65

// crc16 computes the EN 13757-4 block CRC: polynomial 0x3D65, initial value
// 0x0000, no input or output reflection, final one's complement. The check
// value over the ASCII string "123456789" is 0xC2B7.
func crc16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ crcPolynomial
			} else {
				crc <<= 1
			}
		}
	}
	return ^crc
}

// checkCRC verifies the two CRC bytes that follow a block. wM-Bus transmits
// the CRC most significant byte first, unlike every other multi-byte field in
// the protocol.
func checkCRC(data, crcBytes []byte) error {
	want := binary.BigEndian.Uint16(crcBytes)
	got := crc16(data)
	if got != want {
		return fmt.Errorf("%w: got 0x%04X, want 0x%04X", ErrCRC, got, want)
	}
	return nil
}
