package gombus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Bus discovery: finding the slaves on a segment by primary address and by
// secondary address, per EN 13757-2 (link layer) and EN 13757-3 (selection).

const (
	// secondaryDigits is the number of BCD digits in an identification number.
	// It is fixed by the 4-byte ID field, so the wildcard search can never
	// recurse deeper than this.
	secondaryDigits = 8
	// maskWildcard is the nibble value that matches any digit in a selection
	// frame. It is per nibble, so a mask may fix some digits and wildcard the
	// rest, which is what makes the digit-by-digit search possible.
	maskWildcard = 'F'
	// ciSelectSlave (0x52) is the CI of the selection frame that points the
	// 0xFD address at one slave.
	ciSelectSlave = 0x52
	// cSndUD is SND_UD with FCV set and FCB clear. A selection carries no state
	// across frames, so the master does not have to alternate the FCB.
	cSndUD = 0x53
)

var (
	// ErrInvalidScanRange reports a primary scan range whose bounds are
	// reversed.
	ErrInvalidScanRange = errors.New("invalid scan range")
	// ErrInvalidSecondaryMask reports a selection mask that is not 8 digits of
	// 0..9 or the wildcard F.
	ErrInvalidSecondaryMask = errors.New("invalid secondary address mask")
	// ErrInvalidManufacturer reports a manufacturer code that is not three
	// letters A..Z.
	ErrInvalidManufacturer = errors.New("invalid manufacturer code")
	// ErrSelectNoAnswer reports that no slave answered a secondary selection.
	ErrSelectNoAnswer = errors.New("no slave answered the secondary selection")
	// ErrSelectCollision reports that more than one slave answered a secondary
	// selection, so the reply bytes collided and no single slave is selected.
	ErrSelectCollision = errors.New("more than one slave answered the secondary selection")
)

// SecondaryAddress is the EN 13757-3 secondary address of a slave: the 8-digit
// BCD identification number plus the manufacturer, version and medium that make
// it unique across manufacturers.
type SecondaryAddress struct {
	// ID is the identification number, at most 8 decimal digits.
	ID uint64
	// Manufacturer is the 3-letter EN 61107 code, as decoded by
	// LongFrame.DecodeManufacturer.
	Manufacturer string
	// Version is the manufacturer-specific generation of the device.
	Version byte
	// Medium is the raw medium code. Use DeviceTypeLookup for its name.
	Medium byte
}

// Mask returns the identification number as the 8-digit string used in
// selection frames.
func (s SecondaryAddress) Mask() string {
	return fmt.Sprintf("%08d", s.ID)
}

// ScanPrimary probes every primary address from..to and returns those that
// answer, in ascending order.
//
// Each address gets a link reset (SND_NKE) followed by a REQ_UD2, and either an
// E5h acknowledgement or a parseable long frame counts as an answer. Silence and
// garbled replies are ordinary during a scan and are not errors: only a
// cancelled context or a broken transport ends the sweep early.
//
// Probing 250 addresses takes 250 answer windows on a bus with no slaves, so
// scan the narrowest range that can hold the meters you expect.
func (c *Client) ScanPrimary(ctx context.Context, from, to uint8) ([]uint8, error) {
	if err := validateAssignableAddr(from); err != nil {
		return nil, fmt.Errorf("scan start: %w", err)
	}
	if err := validateAssignableAddr(to); err != nil {
		return nil, fmt.Errorf("scan end: %w", err)
	}
	if from > to {
		return nil, fmt.Errorf("%w: %d..%d", ErrInvalidScanRange, from, to)
	}

	var found []uint8
	for addr := from; ; addr++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		present, err := c.probePrimary(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("probing address %d: %w", addr, err)
		}
		if present {
			found = append(found, addr)
		}
		// to is a valid assignable address, so it is at most 250 and addr++
		// cannot wrap. The exit lives here so from == to still probes once.
		if addr == to {
			return found, nil
		}
	}
}

// probePrimary reports whether a slave answers at addr. A silent or garbled
// address is reported as absent, not as an error.
func (c *Client) probePrimary(ctx context.Context, addr uint8) (bool, error) {
	if err := c.WriteFrame(ctx, SndNKE(addr)); err != nil {
		return false, err
	}
	// The link reset ack is read only to keep the byte stream aligned. A slave
	// that skips it still counts if it answers the REQ_UD2 below.
	if _, err := c.probeResponse(ctx, c.probeTimeout); err != nil && !isBusNoise(err) {
		return false, err
	}

	if err := c.WriteFrame(ctx, RequestUD2(addr)); err != nil {
		return false, err
	}
	if _, err := c.probeResponse(ctx, c.probeTimeout); err != nil {
		if isBusNoise(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ScanSecondary finds every slave on the bus by secondary address and returns
// them in the order the search reached them.
//
// The search fixes the identification number one BCD digit at a time. For each
// mask it selects and looks at what comes back: one acknowledgement means a
// single slave matches and its full address is read from its own reply, a
// collision means several slaves match and the search fixes one more digit,
// silence prunes the branch. That bounds the work at 10 selections per digit
// level instead of the 100 million a brute-force sweep of the number space
// would need.
//
// Slaves whose identification numbers are equal but whose manufacturer, version
// or medium differ cannot be told apart this way. They collide at full depth and
// are skipped.
func (c *Client) ScanSecondary(ctx context.Context) ([]SecondaryAddress, error) {
	mask := []byte(strings.Repeat("F", secondaryDigits))
	var found []SecondaryAddress
	if err := c.scanSecondaryDigit(ctx, mask, 0, &found); err != nil {
		return nil, err
	}
	return found, nil
}

// scanSecondaryDigit walks digits 0..9 at position pos, recursing into the next
// position wherever several slaves still match.
func (c *Client) scanSecondaryDigit(ctx context.Context, mask []byte, pos int, found *[]SecondaryAddress) error {
	for digit := byte('0'); digit <= '9'; digit++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		mask[pos] = digit

		responded, collision, err := c.selectMask(ctx, string(mask))
		if err != nil {
			return err
		}
		switch {
		case collision:
			if err := c.narrowSecondary(ctx, mask, pos, found); err != nil {
				return err
			}
		case responded:
			addr, err := c.readSecondaryAddress(ctx)
			if err != nil {
				if !isBusNoise(err) {
					return err
				}
				// The selection looked unique but the data reply did not
				// parse, so more than one slave was in fact selected.
				if err := c.narrowSecondary(ctx, mask, pos, found); err != nil {
					return err
				}
				continue
			}
			*found = append(*found, addr)
		}
	}
	// Leave the position as the caller found it so sibling branches keep their
	// wildcards.
	mask[pos] = maskWildcard
	return nil
}

// narrowSecondary recurses one digit deeper into a colliding branch. At full
// depth there is nothing left to fix, so the branch is abandoned.
func (c *Client) narrowSecondary(ctx context.Context, mask []byte, pos int, found *[]SecondaryAddress) error {
	if pos+1 >= secondaryDigits {
		return nil
	}
	return c.scanSecondaryDigit(ctx, mask, pos+1, found)
}

// SelectSecondary points the 0xFD address at one slave. Frames sent to 0xFD
// afterwards reach that slave until the next selection frame replaces it.
//
// The identification number and, when set, the manufacturer are matched exactly.
// Version and medium are wildcarded: a slave is identified by its number within
// a manufacturer, and matching a stale version would silently select nothing.
//
// It returns ErrSelectNoAnswer when nothing matches and ErrSelectCollision when
// several slaves do.
func (c *Client) SelectSecondary(ctx context.Context, sec SecondaryAddress) error {
	filter, err := newSecondaryFilter(sec)
	if err != nil {
		return err
	}
	responded, collision, err := c.sendSelect(ctx, filter)
	switch {
	case err != nil:
		return err
	case collision:
		return fmt.Errorf("%w: id %s", ErrSelectCollision, sec.Mask())
	case !responded:
		return fmt.Errorf("%w: id %s", ErrSelectNoAnswer, sec.Mask())
	}
	return nil
}

// ReadBySecondary selects sec and reads one frame from it. It is the read path
// for a bus where primary addresses are unassigned or unknown.
func (c *Client) ReadBySecondary(ctx context.Context, sec SecondaryAddress) (*DecodedFrame, error) {
	if err := c.SelectSecondary(ctx, sec); err != nil {
		return nil, err
	}
	return c.ReadSingleFrame(ctx, addrSecondarySelect)
}

// selectMask sends a selection whose identification number is the given 8-digit
// mask, with F wildcarding a digit, and reports what the bus did. Manufacturer,
// version and medium are wildcarded.
//
// responded means exactly one slave acknowledged. collision means several
// answered at once and their bytes overlapped. Both false means nothing matched.
func (c *Client) selectMask(ctx context.Context, mask string) (responded, collision bool, err error) {
	id, err := maskToBCD(mask)
	if err != nil {
		return false, false, err
	}
	return c.sendSelect(ctx, secondaryFilter{id: id, manufacturer: [2]byte{0xFF, 0xFF}, version: 0xFF, medium: 0xFF})
}

// sendSelect writes one selection frame and classifies the answer.
func (c *Client) sendSelect(ctx context.Context, filter secondaryFilter) (responded, collision bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if err := c.WriteFrame(ctx, filter.frame()); err != nil {
		return false, false, err
	}

	resp, err := c.probeResponse(ctx, c.probeTimeout)
	switch {
	case err == nil:
	case errors.Is(err, ErrReadTimeout):
		return false, false, nil
	case isBusNoise(err):
		// Slaves answering together garble each other's bytes, so a reply that
		// does not parse is the normal shape of a collision.
		c.discardBuffered()
		return false, true, nil
	default:
		return false, false, err
	}

	if len(resp) != 1 || resp[0] != SingleCharacterFrame || len(c.buf) > 0 {
		c.discardBuffered()
		return false, true, nil
	}
	return true, false, nil
}

// readSecondaryAddress reads the currently selected slave's own reply and takes
// its full secondary address from the fixed data header.
func (c *Client) readSecondaryAddress(ctx context.Context) (SecondaryAddress, error) {
	if err := c.WriteFrame(ctx, RequestUD2(addrSecondarySelect)); err != nil {
		return SecondaryAddress{}, err
	}
	frame, err := c.probeResponse(ctx, c.probeTimeout)
	if err != nil {
		return SecondaryAddress{}, err
	}
	return secondaryFromHeader(frame)
}

// secondaryFromHeader reads the identification block of a variable data
// response: ID(4) manufacturer(2) version(1) medium(1) at offset 7.
func secondaryFromHeader(frame LongFrame) (SecondaryAddress, error) {
	const headerEnd = 15
	if len(frame) < headerEnd {
		return SecondaryAddress{}, fmt.Errorf(
			"%w: need %d bytes for the identification header, have %d",
			ErrInvalidFrame, headerEnd, len(frame),
		)
	}
	manufacturer, err := frame.DecodeManufacturer()
	if err != nil {
		return SecondaryAddress{}, err
	}
	id, err := bcdMagnitude(frame[7:11])
	if err != nil {
		return SecondaryAddress{}, fmt.Errorf("%w: invalid identification number: %w", ErrInvalidFrame, err)
	}
	return SecondaryAddress{
		ID:           id,
		Manufacturer: manufacturer,
		Version:      frame[13],
		Medium:       frame[14],
	}, nil
}

// secondaryFilter is the 8-byte selection payload: which slaves a selection
// frame matches. 0xFF in any field, or 0xF in any ID nibble, is a wildcard.
type secondaryFilter struct {
	id           [4]byte
	manufacturer [2]byte
	version      byte
	medium       byte
}

// newSecondaryFilter builds an exact filter for sec. Version and medium stay
// wildcarded; see SelectSecondary.
func newSecondaryFilter(sec SecondaryAddress) (secondaryFilter, error) {
	id, err := maskToBCD(sec.Mask())
	if err != nil {
		return secondaryFilter{}, err
	}
	manufacturer := [2]byte{0xFF, 0xFF}
	if sec.Manufacturer != "" {
		manufacturer, err = encodeManufacturer(sec.Manufacturer)
		if err != nil {
			return secondaryFilter{}, err
		}
	}
	return secondaryFilter{id: id, manufacturer: manufacturer, version: 0xFF, medium: 0xFF}, nil
}

// frame builds the SND_UD selection frame (CI=0x52) addressed to 0xFD.
func (f secondaryFilter) frame() LongFrame {
	data := LongFrame{
		0x68, 0x00, 0x00, 0x68,
		cSndUD, addrSecondarySelect, ciSelectSlave,
		f.id[0], f.id[1], f.id[2], f.id[3],
		f.manufacturer[0], f.manufacturer[1],
		f.version,
		f.medium,
		0x00, 0x16,
	}
	data.SetLength()
	data.SetChecksum()
	return data
}

// maskToBCD packs an 8-digit selection mask into the 4 little-endian BCD bytes
// of the ID field. The mask reads most significant digit first, as printed, so
// "12345678" becomes 78 56 34 12. F is the per-nibble wildcard.
func maskToBCD(mask string) ([4]byte, error) {
	var id [4]byte
	if len(mask) != secondaryDigits {
		return id, fmt.Errorf("%w: %q needs %d digits", ErrInvalidSecondaryMask, mask, secondaryDigits)
	}
	for i := 0; i < len(id); i++ {
		hi, err := maskNibble(mask[2*i])
		if err != nil {
			return [4]byte{}, err
		}
		lo, err := maskNibble(mask[2*i+1])
		if err != nil {
			return [4]byte{}, err
		}
		id[len(id)-1-i] = hi<<4 | lo
	}
	return id, nil
}

func maskNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c == maskWildcard || c == 'f':
		return 0x0F, nil
	default:
		return 0, fmt.Errorf("%w: %q is not a digit or the wildcard F", ErrInvalidSecondaryMask, string(c))
	}
}

// encodeManufacturer packs a 3-letter code into the two little-endian bytes of
// the EN 61107 manufacturer field. It is the inverse of
// LongFrame.DecodeManufacturer.
func encodeManufacturer(code string) ([2]byte, error) {
	var out [2]byte
	if len(code) != 3 {
		return out, fmt.Errorf("%w: %q is not 3 letters", ErrInvalidManufacturer, code)
	}
	var id uint16
	for i := range len(code) {
		ch := code[i]
		if ch < 'A' || ch > 'Z' {
			return [2]byte{}, fmt.Errorf("%w: %q must be A..Z", ErrInvalidManufacturer, code)
		}
		id = id<<5 | uint16(ch-64)
	}
	binary.LittleEndian.PutUint16(out[:], id)
	return out, nil
}

// probeResponse reads whatever a probe gets back: an E5h acknowledgement or a
// long frame. wait bounds only the silence before the first byte, because a
// slave that has started talking must be given time to finish its frame.
//
// The read deadline is cleared on return so a later read on the same Conn is not
// bound by this probe's short window.
func (c *Client) probeResponse(ctx context.Context, wait time.Duration) (LongFrame, error) {
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()

	idle := time.Now().Add(wait)
	if frame := frameDeadline(ctx); frame.Before(idle) {
		idle = frame
	}
	for len(c.buf) == 0 {
		if err := c.fill(ctx, idle); err != nil {
			return nil, c.wrapFillErr(err)
		}
	}

	deadline := frameDeadline(ctx)
	for {
		frame, err := c.takeBufferedFrame()
		if err == nil {
			return frame, nil
		}
		if !errors.Is(err, errNeedMore) {
			// Resynchronise: the bytes ahead are not a frame, so keeping them
			// would corrupt the next probe as well.
			c.discardBuffered()
			return nil, err
		}
		if err := c.fill(ctx, deadline); err != nil {
			return nil, c.wrapFillErr(err)
		}
	}
}

// takeBufferedFrame consumes the frame at the head of the buffer, accepting
// either form a slave may send. It returns errNeedMore while the buffer holds
// the start of a long frame but not all of it.
func (c *Client) takeBufferedFrame() (LongFrame, error) {
	if len(c.buf) == 0 {
		return nil, errNeedMore
	}
	if c.buf[0] == SingleCharacterFrame {
		c.consume(1)
		return LongFrame{SingleCharacterFrame}, nil
	}
	frame, n, err := parseLongFrame(c.buf)
	if err != nil {
		return nil, err
	}
	c.consume(n)
	return frame, nil
}

// discardBuffered drops everything buffered. Used after a garbled reply, where
// the remaining bytes belong to no frame.
func (c *Client) discardBuffered() { c.consume(len(c.buf)) }

// isBusNoise reports whether err means "this probe produced nothing usable"
// rather than "the transport is broken". Silence and garbled bytes are ordinary
// on a bus being scanned; a closed socket or a cancelled context is not, and
// must end the scan instead of being counted as an absent slave.
func isBusNoise(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF):
		return false
	case errors.Is(err, ErrReadTimeout),
		errors.Is(err, ErrNoLongFrameFound),
		errors.Is(err, ErrInvalidFrame),
		errors.Is(err, ErrChecksumMismatch),
		errors.Is(err, ErrUnsupportedCI):
		return true
	}
	return false
}
