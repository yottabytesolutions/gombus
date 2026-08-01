package wmbus

import (
	"encoding/binary"
	"fmt"
)

// Transport layer CI fields this package understands.
const (
	// CILongHeader is a variable data response with a long transport header:
	// the header repeats the full address, so the meter it names can differ
	// from the link layer address of the device that sent the telegram.
	CILongHeader = 0x72

	// CIShortHeader is a variable data response with a short transport
	// header: access number, status and configuration word only.
	CIShortHeader = 0x7A

	// CINoHeader is a variable data response without a transport header. It
	// carries no configuration word, so it is never encrypted.
	CINoHeader = 0x78

	// CIExtendedLinkI is the basic extended link layer: communication control
	// and access number, followed by the next CI field.
	CIExtendedLinkI = 0x8C

	// CIExtendedLinkII adds a session number and drives security mode 13,
	// which this package does not decrypt.
	CIExtendedLinkII = 0x8D

	// CIAuthFragmentation is the authentication and fragmentation layer. Its
	// message counter feeds the security mode 7 key derivation.
	CIAuthFragmentation = 0x90
)

// Security modes of the transport layer configuration word.
const (
	// ModeNone is plaintext.
	ModeNone = 0

	// ModeAESCBCIV is security mode 5: AES-128-CBC with an initialisation
	// vector built from the address and the access number.
	ModeAESCBCIV = 5

	// ModeAESCBCDerived is security mode 7: AES-128-CBC with a zero
	// initialisation vector and a key derived from the master key.
	ModeAESCBCDerived = 7

	// modeAESCBCEphemeral is security mode 13, which needs the extended link
	// layer session key exchange. It is reported as unsupported.
	modeAESCBCEphemeral = 13
)

// encryptionBlockLen is the AES block size the configuration word counts in.
const encryptionBlockLen = 16

// transport is the parsed transport layer of one telegram: the header fields,
// the security parameters and the application data that follows the header.
type transport struct {
	ci             byte
	accessNumber   byte
	status         byte
	mode           int
	encryptedBytes int

	// address is the eight IV bytes in transmission order: manufacturer (2),
	// identification number (4), version, device type. It comes from the long
	// transport header when there is one, and from the link layer otherwise.
	address [8]byte

	// counter is the message counter of the authentication and fragmentation
	// layer, in transmission order. Security mode 7 derives its key from it.
	counter    [4]byte
	hasCounter bool

	// data is the application layer: encrypted prefix first, plaintext tail
	// after it.
	data []byte
}

// parseTransport walks the layers between the link layer and the application
// data. Extended link and authentication layers are stepped over; the CI that
// ends the walk is the transport header CI.
func parseTransport(frame *Frame) (*transport, error) {
	tr := &transport{}
	binary.LittleEndian.PutUint16(tr.address[0:2], frame.ManufacturerCode)
	copy(tr.address[2:6], frame.Ident[:])
	tr.address[6] = frame.Version
	tr.address[7] = frame.DeviceType

	rest := frame.Payload
	for {
		if len(rest) < 1 {
			return nil, fmt.Errorf("%w: no CI field", ErrInvalidFrame)
		}
		ci := rest[0]
		switch ci {
		case CIExtendedLinkI:
			// Communication control and access number, then the next CI.
			if len(rest) < 3 {
				return nil, fmt.Errorf("%w: extended link layer truncated", ErrInvalidFrame)
			}
			rest = rest[3:]
		case CIExtendedLinkII:
			return nil, fmt.Errorf("%w: %d (extended link layer session key)", ErrUnsupportedMode, modeAESCBCEphemeral)
		case CIAuthFragmentation:
			next, err := parseAuthLayer(tr, rest)
			if err != nil {
				return nil, err
			}
			rest = next
		case CILongHeader, CIShortHeader, CINoHeader:
			if err := tr.parseHeader(ci, rest[1:]); err != nil {
				return nil, err
			}
			return tr, nil
		default:
			return nil, fmt.Errorf("%w: 0x%02X", ErrUnsupportedCI, ci)
		}
	}
}

// parseHeader reads the transport header that follows CI and leaves the
// application data in tr.data.
func (tr *transport) parseHeader(ci byte, rest []byte) error {
	tr.ci = ci
	if ci == CINoHeader {
		tr.data = rest
		return nil
	}

	// The long header prefixes the short one with the meter address, in the
	// wired package's order: identification number, manufacturer, version,
	// device type.
	if ci == CILongHeader {
		const longPrefix = 8
		if len(rest) < longPrefix {
			return fmt.Errorf("%w: long transport header truncated", ErrInvalidFrame)
		}
		copy(tr.address[0:2], rest[4:6])
		copy(tr.address[2:6], rest[0:4])
		tr.address[6] = rest[6]
		tr.address[7] = rest[7]
		rest = rest[longPrefix:]
	}

	const shortHeaderLen = 4 // access number, status, configuration word
	if len(rest) < shortHeaderLen {
		return fmt.Errorf("%w: transport header truncated", ErrInvalidFrame)
	}
	tr.accessNumber = rest[0]
	tr.status = rest[1]
	config := binary.LittleEndian.Uint16(rest[2:4])
	rest = rest[shortHeaderLen:]

	// Configuration word: bits 8 to 12 hold the security mode, bits 4 to 7 the
	// number of encrypted 16 byte blocks.
	tr.mode = int(config>>8) & 0x1F
	tr.encryptedBytes = (int(config>>4) & 0x0F) * encryptionBlockLen

	switch tr.mode {
	case ModeNone, ModeAESCBCIV:
	case ModeAESCBCDerived:
		// Modes 7 and 13 add a configuration field extension byte that selects
		// the key derivation and the key. Only the default derivation is
		// implemented, so the byte is stepped over.
		if len(rest) < 1 {
			return fmt.Errorf("%w: configuration field extension missing", ErrInvalidFrame)
		}
		rest = rest[1:]
	default:
		return fmt.Errorf("%w: %d", ErrUnsupportedMode, tr.mode)
	}

	if tr.encryptedBytes > len(rest) {
		return fmt.Errorf(
			"%w: configuration word claims %d encrypted bytes, %d remain",
			ErrInvalidFrame, tr.encryptedBytes, len(rest),
		)
	}
	tr.data = rest
	return nil
}

// Authentication and fragmentation layer control bits. Only the message
// counter is used; the other fields are skipped by way of the length field.
const (
	aflMessageControlPresent = 0x2000
	aflMessageCounterPresent = 0x1000
	aflKeyInfoPresent        = 0x0100
)

// parseAuthLayer reads the authentication and fragmentation layer at the start
// of rest and returns everything after it. The layer's own length field is
// what advances the walk, so a misread optional field cannot desynchronise the
// layers that follow.
func parseAuthLayer(tr *transport, rest []byte) ([]byte, error) {
	// CI, AFLL, then AFLL further bytes.
	if len(rest) < 2 {
		return nil, fmt.Errorf("%w: authentication layer truncated", ErrInvalidFrame)
	}
	length := int(rest[1])
	end := 2 + length
	if len(rest) < end {
		return nil, fmt.Errorf(
			"%w: authentication layer claims %d bytes, %d remain",
			ErrInvalidFrame, length, len(rest)-2,
		)
	}
	fields := rest[2:end]

	if len(fields) < 2 {
		return nil, fmt.Errorf("%w: authentication layer has no fragmentation control field", ErrInvalidFrame)
	}
	control := binary.LittleEndian.Uint16(fields[0:2])
	fields = fields[2:]

	// Field order after the control field: message control, key information,
	// message counter, MAC, message length.
	skip := func(n int) bool {
		if len(fields) < n {
			return false
		}
		fields = fields[n:]
		return true
	}
	if control&aflMessageControlPresent != 0 && !skip(1) {
		return nil, fmt.Errorf("%w: authentication layer message control missing", ErrInvalidFrame)
	}
	if control&aflKeyInfoPresent != 0 && !skip(2) {
		return nil, fmt.Errorf("%w: authentication layer key information missing", ErrInvalidFrame)
	}
	if control&aflMessageCounterPresent != 0 {
		if len(fields) < 4 {
			return nil, fmt.Errorf("%w: authentication layer message counter missing", ErrInvalidFrame)
		}
		copy(tr.counter[:], fields[0:4])
		tr.hasCounter = true
	}
	// The MAC and message length fields that may follow are not verified here
	// and need no walking: the length field already marked the layer's end.
	return rest[end:], nil
}
