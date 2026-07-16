package gombus

// Frame layouts and bit-level accessors for the M-Bus link layer.
//
// A short frame is 5 bytes (10h | C | A | CRC | 16h) and is used by the
// master to send fixed-size commands such as REQ_UD2 and SND_NKE.
//
// A long frame begins with 68h LL LL 68h, where LL is the user-data length
// repeated. After the C/A/CI fields the variable-data response carries the
// device identification block followed by data records. It ends with a
// one-byte arithmetic checksum and the stop byte 16h.

// ShortFrame is a 5-byte master->slave request frame.
type ShortFrame []byte

// NewShortFrame returns an unaddressed REQ_UD2-shaped short frame ready to
// have its address and C field filled in. SetChecksum must be called after
// any modification.
func NewShortFrame() ShortFrame {
	return ShortFrame{
		0x10, // start byte (short frame)
		0x7b, // C field (default REQ_UD2)
		0x00, // A field
		0x00, // checksum
		0x16, // stop byte
	}
}

// SetChecksum recomputes the checksum byte from the C and A fields.
func (sf ShortFrame) SetChecksum() {
	sf[len(sf)-2] = calcCheckSum(sf[1 : len(sf)-2])
}

// SetAddress sets the A field (primary address).
func (sf ShortFrame) SetAddress(primary uint8) { sf[2] = primary }

// SetC sets the C field.
func (sf ShortFrame) SetC(c uint8) { sf[1] = c }

// C returns the C (control) field.
func (sf ShortFrame) C() C { return C(sf[1]) }

// A returns the A (address) field.
func (sf ShortFrame) A() byte { return sf[2] }

// SetFCB sets the Frame Count Bit in the C field.
func (sf ShortFrame) SetFCB() { sf[1] |= ControlMaskFcb }

// SetFCV sets the Frame Count Valid bit in the C field.
func (sf ShortFrame) SetFCV() { sf[1] |= ControlMaskFcv }

// ClearFCB clears the Frame Count Bit in the C field.
func (sf ShortFrame) ClearFCB() { sf[1] &^= ControlMaskFcb }

// ClearFCV clears the Frame Count Valid bit in the C field.
func (sf ShortFrame) ClearFCV() { sf[1] &^= ControlMaskFcv }

// LongFrame is a variable-length frame used for both master->slave commands
// (CI=0x50/0x51/0x52) and slave->master variable-data responses (CI=0x72).
type LongFrame []byte

// SetChecksum recomputes the checksum byte over the user data area.
func (lf LongFrame) SetChecksum() {
	lf[len(lf)-2] = calcCheckSum(lf[4 : len(lf)-2])
}

// SetLength sets the two L bytes from the current frame length.
func (lf LongFrame) SetLength() {
	l := byte(len(lf) - 6)
	lf[1] = l
	lf[2] = l
}

// L returns the L field (user data length).
func (lf LongFrame) L() int { return int(lf[1]) }

// C returns the C (control) field.
func (lf LongFrame) C() C { return C(lf[4]) }

// A returns the A (address) field.
func (lf LongFrame) A() byte { return lf[5] }

// CI returns the CI (control information) field.
func (lf LongFrame) CI() byte { return lf[6] }

// SetFCB sets the Frame Count Bit in the C field.
func (lf LongFrame) SetFCB() { lf[4] |= ControlMaskFcb }

// SetFCV sets the Frame Count Valid bit in the C field.
func (lf LongFrame) SetFCV() { lf[4] |= ControlMaskFcv }

// ClearFCB clears the Frame Count Bit in the C field.
func (lf LongFrame) ClearFCB() { lf[4] &^= ControlMaskFcb }

// ClearFCV clears the Frame Count Valid bit in the C field.
func (lf LongFrame) ClearFCV() { lf[4] &^= ControlMaskFcv }

// C is a typed C-field byte exposing FCB/FCV bit accessors.
type C byte

// FCB reports whether the Frame Count Bit is set.
func (c C) FCB() bool { return c&ControlMaskFcb != 0 }

// FCV reports whether the Frame Count Valid bit is set.
func (c C) FCV() bool { return c&ControlMaskFcv != 0 }
