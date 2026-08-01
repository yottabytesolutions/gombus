package wmbus

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/yottabytesolutions/gombus"
)

// Format identifies the link layer framing of a raw telegram.
type Format int

const (
	// FormatA is EN 13757-4 frame format A: a 10 byte first block, then 16
	// byte blocks, every block followed by its own 2 byte CRC. The L field
	// counts neither the L byte itself nor any CRC byte.
	FormatA Format = iota + 1

	// FormatB is EN 13757-4 frame format B: one CRC over the first 126 bytes
	// and, for longer telegrams, a second CRC over the rest. The L field
	// counts the CRC bytes, so the frame is L+1 bytes long.
	FormatB

	// FormatStripped is input from a receiver that already verified and
	// removed the CRCs, which is what several dongles (for example the
	// IMST iM871A) hand over.
	FormatStripped
)

// String names the format for error messages and logs.
func (f Format) String() string {
	switch f {
	case FormatA:
		return "A"
	case FormatB:
		return "B"
	case FormatStripped:
		return "CRC-stripped"
	default:
		return "unknown"
	}
}

// dllHeaderLen is the length of the data link layer header, L field included:
// L, C, manufacturer (2), identification number (4), version, device type.
const dllHeaderLen = 10

// Frame is a link layer telegram with its CRCs verified and removed. Payload
// starts at the first CI byte, so it is the input of the transport layer.
type Frame struct {
	// Format is the framing the frame was parsed as.
	Format Format

	// C is the link layer control field (SND-NR, SND-IR and friends).
	C byte

	// ManufacturerCode is the two byte manufacturer field as transmitted.
	ManufacturerCode uint16

	// Ident is the four byte identification number, BCD, as transmitted.
	Ident [4]byte

	// Version is the meter's generation byte.
	Version byte

	// DeviceType is the raw medium byte. Telegram carries its decoded name.
	DeviceType byte

	// Payload is the transport layer, starting at the CI field.
	Payload []byte
}

// Manufacturer returns the three letter manufacturer code, or the empty
// string when the two raw bytes do not decode to letters.
func (f *Frame) Manufacturer() string {
	return manufacturerOf(f.ManufacturerCode)
}

// SerialNumber returns the identification number as a decimal serial. The
// field is BCD per EN 13757-3, so a manufacturer that fills it with arbitrary
// bytes yields an error rather than a made-up number.
func (f *Frame) SerialNumber() (int, error) {
	v, err := identSerial(f.Ident)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

// Parse parses a raw telegram that still carries its CRCs. Frame format A and
// B cannot be told apart from the bytes alone, so Parse tries A first and
// falls back to B. Use ParseFormatA or ParseFormatB when the receiver tells
// you which format it delivers, and ParseWithoutCRC when it strips the CRCs.
func Parse(raw []byte) (*Frame, error) {
	frame, errA := ParseFormatA(raw)
	if errA == nil {
		return frame, nil
	}
	frame, errB := ParseFormatB(raw)
	if errB == nil {
		return frame, nil
	}
	return nil, fmt.Errorf("%w: neither frame format A nor B: %w", ErrInvalidFrame, errors.Join(errA, errB))
}

// ParseFormatA parses frame format A. The first byte is the L field, as
// receivers hand the telegram over. Bytes past the end of the frame are
// ignored, which is what receivers that append a link quality byte produce.
func ParseFormatA(raw []byte) (*Frame, error) {
	if len(raw) < dllHeaderLen+2 {
		return nil, fmt.Errorf("%w: format A needs at least %d bytes, have %d", ErrInvalidFrame, dllHeaderLen+2, len(raw))
	}
	l := int(raw[0])
	if l < dllHeaderLen-1 {
		return nil, fmt.Errorf("%w: L field %d is shorter than the link layer header", ErrInvalidFrame, l)
	}
	payloadLen := l - (dllHeaderLen - 1)

	if err := checkCRC(raw[:dllHeaderLen], raw[dllHeaderLen:dllHeaderLen+2]); err != nil {
		return nil, fmt.Errorf("first block: %w", err)
	}

	payload, err := stripFormatABlocks(raw, payloadLen)
	if err != nil {
		return nil, err
	}
	return newFrame(FormatA, raw, payload), nil
}

// stripFormatABlocks verifies and removes the per block CRCs of the data
// blocks that follow the first block. Every block holds 16 data bytes except
// the last one, which holds the remainder.
func stripFormatABlocks(raw []byte, payloadLen int) ([]byte, error) {
	const blockLen = 16

	payload := make([]byte, 0, payloadLen)
	offset := dllHeaderLen + 2
	for remaining := payloadLen; remaining > 0; {
		n := min(remaining, blockLen)
		if len(raw) < offset+n+2 {
			return nil, fmt.Errorf(
				"%w: format A block at offset %d needs %d bytes, have %d",
				ErrInvalidFrame, offset, n+2, len(raw)-offset,
			)
		}
		if err := checkCRC(raw[offset:offset+n], raw[offset+n:offset+n+2]); err != nil {
			return nil, fmt.Errorf("block at offset %d: %w", offset, err)
		}
		payload = append(payload, raw[offset:offset+n]...)
		offset += n + 2
		remaining -= n
	}
	return payload, nil
}

// formatBFirstCRCEnd is the end of the first format B CRC block. Block one and
// two together are at most 128 bytes, the last two of which are the CRC.
const formatBFirstCRCEnd = 128

// ParseFormatB parses frame format B. The L field counts the CRC bytes here,
// so the telegram is L+1 bytes long. One CRC covers the first 126 bytes; a
// telegram longer than 128 bytes carries a second CRC over the rest.
func ParseFormatB(raw []byte) (*Frame, error) {
	if len(raw) < dllHeaderLen+2 {
		return nil, fmt.Errorf("%w: format B needs at least %d bytes, have %d", ErrInvalidFrame, dllHeaderLen+2, len(raw))
	}
	total := int(raw[0]) + 1
	if total < dllHeaderLen+2 {
		return nil, fmt.Errorf("%w: L field %d is too short for a format B frame", ErrInvalidFrame, raw[0])
	}
	if len(raw) < total {
		return nil, fmt.Errorf("%w: format B frame claims %d bytes, have %d", ErrInvalidFrame, total, len(raw))
	}

	if total <= formatBFirstCRCEnd {
		if err := checkCRC(raw[:total-2], raw[total-2:total]); err != nil {
			return nil, fmt.Errorf("format B block: %w", err)
		}
		return newFrame(FormatB, raw, raw[dllHeaderLen:total-2]), nil
	}

	// A second CRC block exists, so it must hold at least one data byte on top
	// of its own two CRC bytes.
	if total < formatBFirstCRCEnd+3 {
		return nil, fmt.Errorf("%w: format B second block is empty (total %d bytes)", ErrInvalidFrame, total)
	}
	if err := checkCRC(raw[:formatBFirstCRCEnd-2], raw[formatBFirstCRCEnd-2:formatBFirstCRCEnd]); err != nil {
		return nil, fmt.Errorf("format B first block: %w", err)
	}
	if err := checkCRC(raw[formatBFirstCRCEnd:total-2], raw[total-2:total]); err != nil {
		return nil, fmt.Errorf("format B second block: %w", err)
	}

	payload := make([]byte, 0, total-dllHeaderLen-4)
	payload = append(payload, raw[dllHeaderLen:formatBFirstCRCEnd-2]...)
	payload = append(payload, raw[formatBFirstCRCEnd:total-2]...)
	return newFrame(FormatB, raw, payload), nil
}

// ParseWithoutCRC parses a telegram whose CRCs the receiver already verified
// and removed. The L field keeps its format A meaning: the frame is L+1 bytes
// long, CRCs excluded. Feed a CRC-stripped format B telegram in only after
// subtracting the removed CRC bytes from its L field.
func ParseWithoutCRC(raw []byte) (*Frame, error) {
	if len(raw) < dllHeaderLen {
		return nil, fmt.Errorf("%w: link layer header needs %d bytes, have %d", ErrInvalidFrame, dllHeaderLen, len(raw))
	}
	total := int(raw[0]) + 1
	if total < dllHeaderLen {
		return nil, fmt.Errorf("%w: L field %d is shorter than the link layer header", ErrInvalidFrame, raw[0])
	}
	if len(raw) < total {
		return nil, fmt.Errorf("%w: frame claims %d bytes, have %d", ErrInvalidFrame, total, len(raw))
	}
	return newFrame(FormatStripped, raw, raw[dllHeaderLen:total]), nil
}

// newFrame fills the link layer fields from the header bytes. payload is
// copied so the returned frame does not alias the caller's buffer, which is
// what lets a receiver reuse its read buffer.
func newFrame(format Format, raw, payload []byte) *Frame {
	frame := &Frame{
		Format:           format,
		C:                raw[1],
		ManufacturerCode: binary.LittleEndian.Uint16(raw[2:4]),
		Version:          raw[8],
		DeviceType:       raw[9],
		Payload:          append([]byte(nil), payload...),
	}
	copy(frame.Ident[:], raw[4:8])
	return frame
}

// manufacturerOf decodes the packed three letter manufacturer code. It reuses
// the wired package's decoder by handing it a buffer of the shape that
// decoder expects, so both layers spell manufacturers the same way.
func manufacturerOf(code uint16) string {
	const manufacturerOffset = 11
	buf := make(gombus.LongFrame, manufacturerOffset+2)
	binary.LittleEndian.PutUint16(buf[manufacturerOffset:], code)
	name, err := buf.DecodeManufacturer()
	if err != nil {
		return ""
	}
	return name
}

// identSerial decodes the four BCD bytes of the identification number,
// least significant byte first.
func identSerial(ident [4]byte) (uint32, error) {
	var serial uint32
	for i := len(ident) - 1; i >= 0; i-- {
		hi, lo := ident[i]>>4, ident[i]&0x0F
		if hi > 9 || lo > 9 {
			return 0, fmt.Errorf("%w: identification number byte %d is not BCD (0x%02X)", ErrInvalidFrame, i, ident[i])
		}
		serial = serial*100 + uint32(hi)*10 + uint32(lo)
	}
	return serial, nil
}
