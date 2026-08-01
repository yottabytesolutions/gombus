package wmbus

import (
	"encoding/binary"
	"fmt"

	"github.com/yottabytesolutions/gombus"
)

// Telegram is a fully decoded wireless M-Bus telegram.
type Telegram struct {
	// Manufacturer is the three letter code of the meter.
	Manufacturer string

	// SerialNumber is the meter's identification number. It is zero when the
	// meter fills the field with bytes that are not BCD.
	SerialNumber int

	// Version is the meter's generation byte.
	Version byte

	// DeviceType is the decoded medium, for example "Water" or "Heat".
	DeviceType string

	// CI is the transport layer CI field the telegram was decoded as.
	CI byte

	// AccessNumber counts the meter's transmissions. It is zero for a
	// telegram without a transport header.
	AccessNumber byte

	// Status is the application status byte, with the same bit meanings as in
	// the wired protocol.
	Status byte

	// EncryptionMode is the security mode of the configuration word: 0 for
	// plaintext, 5 or 7 for the two supported AES modes.
	EncryptionMode int

	// DataRecords are the application layer records, decoded by the wired
	// package so the same helpers and matchers apply.
	DataRecords []gombus.DecodedDataRecord
}

// KeyRing maps a meter identification number to its 16 byte AES key.
type KeyRing map[uint64][]byte

// KeyFor returns the key of a meter, or nil when the ring has none.
func (kr KeyRing) KeyFor(serial uint64) []byte {
	return kr[serial]
}

// Decode parses a raw telegram and decodes it. key may be nil for
// unencrypted telegrams; an encrypted telegram without a key returns
// ErrKeyRequired. Frame format is auto-detected the way Parse does it.
func Decode(raw, key []byte) (*Telegram, error) {
	frame, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	return frame.Decode(key)
}

// DecodeWithKeyRing parses a raw telegram and decodes it with the key the ring
// holds for the sending meter. A meter that is not in the ring is decoded
// without a key, which succeeds only for an unencrypted telegram.
func DecodeWithKeyRing(raw []byte, keys KeyRing) (*Telegram, error) {
	frame, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	return frame.DecodeWithKeyRing(keys)
}

// DecodeWithKeyRing decodes a parsed frame with the key the ring holds for the
// link layer identification number.
func (f *Frame) DecodeWithKeyRing(keys KeyRing) (*Telegram, error) {
	var key []byte
	if serial, err := identSerial(f.Ident); err == nil {
		key = keys.KeyFor(uint64(serial))
	}
	return f.Decode(key)
}

// Decode walks the transport layer, decrypts when needed and decodes the
// application data records. key may be nil for unencrypted telegrams.
func (f *Frame) Decode(key []byte) (*Telegram, error) {
	tr, err := parseTransport(f)
	if err != nil {
		return nil, err
	}
	payload, err := decrypt(tr, key)
	if err != nil {
		return nil, err
	}

	decoded, err := decodeApplication(tr, payload)
	if err != nil {
		return nil, err
	}

	return &Telegram{
		Manufacturer:   decoded.Manufacturer,
		SerialNumber:   decoded.SerialNumber,
		Version:        tr.address[6],
		DeviceType:     decoded.DeviceType,
		CI:             tr.ci,
		AccessNumber:   tr.accessNumber,
		Status:         tr.status,
		EncryptionMode: tr.mode,
		DataRecords:    decoded.DataRecords,
	}, nil
}

// Wired long frame offsets, from the start byte to the first data record.
const (
	wiredIdentOffset  = 7
	wiredRecordOffset = 19
	wiredTrailerLen   = 2 // checksum and stop byte
)

// decodeApplication decodes the application payload by handing it to the wired
// package. The wireless application layer is the wired one: identical DIF/VIF
// data records. So instead of a second walker that would drift from the first,
// the payload is wrapped in a synthetic variable data response carrying the
// wireless address, and gombus.LongFrame.Decode does the work. That also gives
// the manufacturer string and the device type name for free.
func decodeApplication(tr *transport, payload []byte) (*gombus.DecodedFrame, error) {
	frame := make(gombus.LongFrame, wiredRecordOffset+len(payload)+wiredTrailerLen)
	frame[0] = 0x68
	frame[3] = 0x68
	frame[4] = 0x08 // RSP_UD
	frame[5] = 0x00 // no primary address on the wireless side
	frame[6] = 0x72

	// The wired header wants the identification number, manufacturer, version
	// and device type in that order; tr.address holds them in wireless order.
	// A non-BCD identification number would fail the whole decode, and the
	// data records are worth more than the identity, so it is zeroed and the
	// serial number reads as zero.
	copy(frame[wiredIdentOffset:wiredIdentOffset+4], tr.address[2:6])
	if _, err := identSerial([4]byte(tr.address[2:6])); err != nil {
		copy(frame[wiredIdentOffset:wiredIdentOffset+4], make([]byte, 4))
	}
	copy(frame[11:13], tr.address[0:2])
	frame[13] = tr.address[6]
	frame[14] = tr.address[7]
	frame[15] = tr.accessNumber
	frame[16] = tr.status
	binary.LittleEndian.PutUint16(frame[17:19], 0)

	copy(frame[wiredRecordOffset:], payload)
	frame[len(frame)-1] = 0x16
	frame.SetLength()
	frame.SetChecksum()

	decoded, err := frame.Decode()
	if err != nil {
		// The wired decoder has its own sentinels, and its record level ones
		// are unexported. Wrapping keeps one contract for callers of this
		// package: anything structurally broken is ErrInvalidFrame.
		return nil, fmt.Errorf("%w: decoding application data: %w", ErrInvalidFrame, err)
	}
	return decoded, nil
}
