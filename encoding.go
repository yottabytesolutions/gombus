package gombus

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// bcdSignNibble marks a negative BCD value when it occupies the most
// significant nibble of the field (EN 13757-3 data type A).
const bcdSignNibble = 0xF

// uintToBCD writes value as little-endian binary-coded-decimal (BCD), using
// size bytes. Each byte holds two decimal digits (high nibble = tens), so the
// field holds size*2 digits. It errors when value needs more digits than that,
// because a truncated meter address silently selects a different meter.
func uintToBCD(value uint64, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("BCD size must be positive, got %d", size)
	}
	buf := make([]byte, size)
	remainder := value
	for i := range buf {
		tail := byte(remainder % 100)
		buf[i] = (tail/10)<<4 | tail%10
		remainder /= 100
	}
	if remainder != 0 {
		return nil, fmt.Errorf("value %d needs more than %d BCD digits", value, size*2)
	}
	return buf, nil
}

// bcdDigits accumulates the decimal digits of a little-endian BCD field, most
// significant byte last. skipSignNibble drops the most significant nibble,
// which bcdSigned has already consumed as the sign.
func bcdDigits(b []byte, skipSignNibble bool) (uint64, error) {
	size := len(b)
	if size == 0 {
		return 0, fmt.Errorf("BCD conversion needs at least 1 byte")
	}
	var value uint64
	for k := range b {
		by := b[size-1-k]
		hi, lo := by>>4, by&0x0F
		if k == 0 && skipSignNibble {
			hi = 0
		}
		if hi > 9 || lo > 9 {
			return 0, fmt.Errorf("invalid BCD byte 0x%02X at index %d", by, size-1-k)
		}
		digit := uint64(hi)*10 + uint64(lo)
		if value > (math.MaxUint64-digit)/100 {
			return 0, fmt.Errorf("BCD value of %d bytes overflows uint64", size)
		}
		value = value*100 + digit
	}
	return value, nil
}

// bcdMagnitude decodes little-endian BCD bytes into their unsigned value. Two
// decimal digits per byte (high nibble = tens), most significant byte last.
// Every nibble must be 0-9.
//
// It applies no sign handling. LVAR carries the sign in the LVAR byte rather
// than in a nibble, so that caller owns the sign.
func bcdMagnitude(b []byte) (uint64, error) {
	return bcdDigits(b, false)
}

// bcdSigned decodes little-endian BCD bytes into a signed value. Per EN 13757-3
// data type A the most significant nibble carries the sign: 0xF there means the
// value is negative and the nibble is not a digit. Every other nibble must be
// 0-9; A-F elsewhere are illegal BCD and error rather than decode as bogus
// digits.
//
// The all-F "invalid / not available" marker some meters send fails as invalid
// BCD, because only the sign nibble may hold 0xF. It does not decode as a large
// negative number.
func bcdSigned(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, fmt.Errorf("BCD conversion needs at least 1 byte")
	}
	negative := b[len(b)-1]>>4 == bcdSignNibble
	value, err := bcdDigits(b, negative)
	if err != nil {
		return 0, err
	}
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("BCD value %d overflows int64", value)
	}
	if negative {
		return -int64(value), nil
	}
	return int64(value), nil
}

// maxLEBytes is the widest little-endian integer these primitives read.
const maxLEBytes = 8

// uintLE reads up to 8 little-endian bytes as raw unsigned bits, ignoring any
// byte past the eighth. Use it for fields the unit table marks Raw: bitfields
// that are not numbers and must not be sign-extended.
func uintLE(b []byte) uint64 {
	if len(b) > maxLEBytes {
		b = b[:maxLEBytes]
	}
	var v uint64
	for i, by := range b {
		v |= uint64(by) << (8 * i)
	}
	return v
}

// intLE reads up to 8 little-endian bytes as a two's complement signed integer
// (EN 13757-3 data type B), sign-extending from the most significant byte
// supplied. The width is len(b), so a 3-byte field extends from bit 23. This is
// the default for DIF data fields 0x01-0x07.
func intLE(b []byte) int64 {
	if len(b) > maxLEBytes {
		b = b[:maxLEBytes]
	}
	v := uintLE(b)
	// An empty field has no sign bit, and 8 bytes already fill the result.
	if len(b) == 0 || len(b) == maxLEBytes {
		return int64(v)
	}
	shift := uint(64 - 8*len(b))
	return int64(v<<shift) >> shift
}

// bytesToFloat32 reads a little-endian IEEE-754 single-precision float;
// errors when len != 4.
func bytesToFloat32(data []byte) (float32, error) {
	if len(data) != 4 {
		return 0, fmt.Errorf("float32 conversion needs 4 bytes, got %d", len(data))
	}
	return math.Float32frombits(binary.LittleEndian.Uint32(data)), nil
}

// decodeASCII reads bytes in reverse order (M-Bus stores ASCII strings
// little-endian, so the natural reading order is reversed).
func decodeASCII(data []byte) string {
	var sb strings.Builder
	sb.Grow(len(data))
	for i := len(data) - 1; i >= 0; i-- {
		sb.WriteByte(data[i])
	}
	return sb.String()
}

// calcCheckSum returns the M-Bus arithmetic checksum: the low byte of the
// sum of the input bytes.
func calcCheckSum(data []byte) byte {
	var sum byte
	for _, v := range data {
		sum += v
	}
	return sum
}

// checkKthBitSet reports whether bit k of n is set (k=0 is the LSB).
func checkKthBitSet(n, k int) bool {
	return n&(1<<k) != 0
}
