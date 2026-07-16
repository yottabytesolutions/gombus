package gombus

import "fmt"

// Frame builders for the master->slave commands the master sends on the bus.
//
// RequestUD2 and SndNKE validate nothing on purpose. They turn arguments into
// bytes; the bus operations that send them (ReadAllFrames, ReadSingleFrame)
// validate first. A blanket check here would also be wrong, because the legal
// destination set differs per command: SndNKE(255) is a valid broadcast link
// reset to every slave, while RequestUD2(255) is merely pointless, since a
// broadcast without reply never answers and the caller just times out.

// RequestUD2 builds a REQ_UD2 short frame requesting class-2 user data from
// the slave at primaryID.
func RequestUD2(primaryID uint8) ShortFrame {
	data := NewShortFrame()
	data[1] = 0x5b
	data[2] = primaryID
	data.SetChecksum()
	return data
}

// SndNKE builds an SND_NKE short frame (link reset). The slave acks with
// SingleCharacterFrame (0xE5).
func SndNKE(primaryID uint8) ShortFrame {
	data := NewShortFrame()
	data[1] = 0x40
	data[2] = primaryID
	data.SetChecksum()
	return data
}

// ApplicationReset builds an Application Reset long frame (CI=0x50) for the
// given primary address.
func ApplicationReset(primaryID uint8) LongFrame {
	data := LongFrame{
		0x68, 0x06, 0x06, 0x68, // start, L, L, start
		0x73,      // SND_UD
		primaryID, // A
		0x50,      // CI: data send (application reset)
		0x00,      // checksum
		0x16,      // stop
	}
	data.SetLength()
	data.SetChecksum()
	return data
}

// SendUD2 builds a slave-selection long frame (SND_UD, CI=0x52) addressed to
// 0xFD with every selection field wildcarded (FF FF FF FF FF FF FF FF).
// Wildcarding is per BCD nibble, so an all-F identification number matches
// every identification number.
//
// This selects EVERY slave on the bus, not one slave. On a bus with more than
// one meter all of them reply at once and the replies collide. Use it on a
// single-meter bus, or as the opening frame of a secondary-address scan that
// expects collisions and narrows the wildcard to find each meter. The
// selection stays in effect until the next selection frame, so a following
// frame addressed to 0xFD reaches whatever this selected.
//
// Selection is a two-frame exchange: this frame selects, and a second frame
// addressed to 0xFD acts on the selection. See SetPrimaryUsingSecondary, which
// merges both steps into one frame and is under review for that reason.
func SendUD2() LongFrame {
	data := LongFrame{
		0x68, 0x00, 0x00, 0x68, // start, L, L, start
		0x73, 0xFD, 0x52, // SND_UD, address 0xFD, CI: select slave
		0xFF, 0xFF, 0xFF, 0xFF, // ID wildcard (BCD)
		0xFF, 0xFF, // manufacturer wildcard
		0xFF,       // version wildcard
		0xFF,       // medium wildcard
		0x00, 0x16, // checksum, stop
	}
	data.SetLength()
	data.SetChecksum()
	return data
}

// SetPrimaryUsingSecondary builds a long frame that addresses the slave by
// secondary address (BCD identification number) and writes a new primary
// address (CI=0x51, DIF=0x01, VIF=0x7A).
//
// secondary must fit in 8 BCD digits and primary must be a valid primary
// address (1..250); both error rather than build a frame that writes to the
// wrong slave or leaves the slave unaddressable.
func SetPrimaryUsingSecondary(secondary uint64, primary uint8) (LongFrame, error) {
	if err := validateAssignableAddr(primary); err != nil {
		return nil, fmt.Errorf("new primary address: %w", err)
	}

	id, err := uintToBCD(secondary, 4)
	if err != nil {
		return nil, fmt.Errorf("secondary address %d: %w", secondary, err)
	}

	data := LongFrame{
		0x68, 0x00, 0x00, 0x68,
		0x73, 0xFD, 0x51,
		0x00, 0x00, 0x00, 0x00, // ID slot (will be overwritten)
		0xFF, 0xFF, // manufacturer wildcard
		0xFF,       // version wildcard
		0xFF,       // medium wildcard
		0x01,       // DIF
		0x7a,       // VIF
		primary,    // new primary
		0x00, 0x16, // checksum, stop
	}

	copy(data[7:11], id)

	data.SetLength()
	data.SetChecksum()
	return data, nil
}

// SetPrimaryUsingPrimary builds a long frame that addresses the slave by its
// current primary address and writes a new primary address.
//
// The two arguments follow different rules, so they take different validators.
// oldPrimary is where the frame is SENT, so 0xFE (broadcast with reply) is
// allowed and is the standard way to address a single new meter on its own
// bus. newPrimary is written INTO the slave, so 0xFE is rejected: it would
// make the slave answer every broadcast and leave it unaddressable. Same
// value, opposite answers, one call.
func SetPrimaryUsingPrimary(oldPrimary, newPrimary uint8) (LongFrame, error) {
	if err := validateDestinationAddr(oldPrimary); err != nil {
		return nil, fmt.Errorf("current primary address: %w", err)
	}
	if err := validateAssignableAddr(newPrimary); err != nil {
		return nil, fmt.Errorf("new primary address: %w", err)
	}

	data := LongFrame{
		0x68, 0x06, 0x06, 0x68,
		0x73, oldPrimary, 0x51,
		0x01, 0x7a, newPrimary,
		0x00, 0x16,
	}
	data.SetLength()
	data.SetChecksum()
	return data, nil
}
