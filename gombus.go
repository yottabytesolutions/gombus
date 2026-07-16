package gombus

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// writeTimeout bounds a single conn.Write so a stuck transport cannot block
// forever.
const writeTimeout = 2 * time.Second

// Client is an M-Bus master session over one transport.
//
// It owns the bytes that have arrived from the Conn but not yet been consumed
// by a frame. That state is why a Client must be kept and reused for every
// exchange on the same Conn: M-Bus is a byte stream, so one transport Read can
// carry the tail of one frame and the head of the next. Building a fresh Client
// per read throws the remainder away and desynchronises the stream, which is
// the bug this type exists to prevent.
//
// A Client is not safe for concurrent use. A bus exchange is a request followed
// by its answer, so concurrent readers would interleave and corrupt each
// other's frames. Use one Client per transport, from one goroutine.
type Client struct {
	conn Conn
	buf  []byte // read from conn, not yet consumed by a frame
	tmp  []byte // scratch for a single transport Read
}

// NewClient returns a Client that reads and writes M-Bus frames over conn.
// Any Conn works: see [Conn].
func NewClient(conn Conn) *Client {
	return &Client{
		conn: conn,
		tmp:  make([]byte, readChunkSize),
	}
}

// Close closes the underlying transport. Buffered bytes are discarded.
func (c *Client) Close() error {
	c.buf = nil
	return c.conn.Close()
}

const (
	// minPrimaryID and maxPrimaryID bound the addresses a slave can hold as its
	// own. 0 marks an unconfigured slave and 251..255 are reserved.
	minPrimaryID = 1
	maxPrimaryID = 250
	// addrSecondarySelect (0xFD) is the destination used for secondary
	// addressing: after a selection the slave answers here.
	addrSecondarySelect = 253
	// addrBroadcastReply (0xFE) is broadcast-with-reply. Every slave answers, so
	// it is usable only on a single-slave bus, where it is the documented way to
	// reach a slave whose address is unknown.
	addrBroadcastReply = 254
	// addrBroadcastNoReply (0xFF) is broadcast without reply. No slave ever
	// answers it.
	addrBroadcastNoReply = 255
	// maxFramesPerRead bounds the FCB walk. A slave that always sets the "more
	// records follow" sentinel would otherwise loop until it exhausts memory.
	maxFramesPerRead = 64
)

var (
	ErrInvalidPrimaryID = errors.New("primary address out of range")
	ErrTooManyFrames    = errors.New("too many frames")
)

// validateAssignableAddr accepts only the addresses a slave may hold as its own,
// so it is the rule for an address being WRITTEN INTO a slave. Strict on
// purpose: writing 0xFD or 0xFE into a slave makes it answer secondary
// selections or every broadcast, which is not recoverable over the bus.
//
// It is deliberately narrower than validateDestinationAddr. Do not merge them.
func validateAssignableAddr(addr uint8) error {
	if addr < minPrimaryID || addr > maxPrimaryID {
		return fmt.Errorf("%w: %d, want %d..%d", ErrInvalidPrimaryID, addr, minPrimaryID, maxPrimaryID)
	}
	return nil
}

// validateDestinationAddr accepts the addresses a frame may be SENT TO, which
// is wider than the set a slave may hold. 0xFD is where a slave answers after a
// secondary selection, and 0xFE is broadcast-with-reply, the documented way to
// reach a slave of unknown address on a single-slave bus. Both are legal
// destinations and neither may ever be written into a slave as its own address,
// which is why this is a separate rule from validateAssignableAddr.
//
// 0xFF is broadcast without reply, so a read addressed to it can never answer.
// It is rejected here rather than left to expire as a timeout.
func validateDestinationAddr(addr uint8) error {
	switch {
	case addr >= minPrimaryID && addr <= maxPrimaryID:
		return nil
	case addr == addrSecondarySelect, addr == addrBroadcastReply:
		return nil
	case addr == addrBroadcastNoReply:
		return fmt.Errorf(
			"%w: %d is broadcast without reply, no slave answers it",
			ErrInvalidPrimaryID, addr,
		)
	default:
		return fmt.Errorf(
			"%w: %d, want %d..%d, %d or %d",
			ErrInvalidPrimaryID, addr, minPrimaryID, maxPrimaryID, addrSecondarySelect, addrBroadcastReply,
		)
	}
}

// WriteFrame sends a frame as-is, bounded by the earlier of writeTimeout and
// ctx's deadline. Use it with the frame builders ([SndNKE], [RequestUD2],
// [SetPrimaryUsingPrimary] and friends) to drive an exchange this package does
// not wrap.
func (c *Client) WriteFrame(ctx context.Context, frame []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	deadline := time.Now().Add(writeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}

	_, err := c.conn.Write(frame)
	return err
}

// ReadAllFrames reads every frame the slave at primaryID has, walking the
// FCB bit to advance through multi-frame responses. It reads at most
// maxFramesPerRead frames and errors if the slave still reports more.
//
// ctx bounds the whole walk, not each frame within it: every frame is
// additionally bounded by frameReadTimeout.
//
// primaryID is a destination, so 0xFD and 0xFE are accepted. See
// validateDestinationAddr.
func (c *Client) ReadAllFrames(ctx context.Context, primaryID uint8) ([]*DecodedFrame, error) {
	if err := validateDestinationAddr(primaryID); err != nil {
		return nil, err
	}
	if err := c.WriteFrame(ctx, SndNKE(primaryID)); err != nil {
		return nil, err
	}
	if _, err := c.ReadSingleCharFrame(ctx); err != nil {
		return nil, err
	}

	var frames []*DecodedFrame
	respFrame := &DecodedFrame{}
	lastFCB := true
	for frameCnt := 0; respFrame.HasMoreRecords() || frameCnt == 0; frameCnt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if frameCnt >= maxFramesPerRead {
			return nil, fmt.Errorf(
				"%w: slave %d still reports more records after %d frames",
				ErrTooManyFrames, primaryID, maxFramesPerRead,
			)
		}

		frame := RequestUD2(primaryID)
		if !lastFCB {
			frame.SetFCB()
			frame.SetChecksum()
		}
		lastFCB = frame.C().FCB()

		if err := c.WriteFrame(ctx, frame); err != nil {
			return nil, err
		}
		resp, err := c.ReadLongFrame(ctx)
		if err != nil {
			return nil, err
		}
		respFrame, err = resp.Decode()
		if err != nil {
			return nil, err
		}
		frames = append(frames, respFrame)
	}
	return frames, nil
}

// ReadSingleFrame reads exactly one frame from the slave at primaryID. Does
// not link-reset the slave first.
//
// primaryID is a destination, so 0xFD and 0xFE are accepted. See
// validateDestinationAddr.
func (c *Client) ReadSingleFrame(ctx context.Context, primaryID uint8) (*DecodedFrame, error) {
	if err := validateDestinationAddr(primaryID); err != nil {
		return nil, err
	}
	if err := c.WriteFrame(ctx, RequestUD2(primaryID)); err != nil {
		return nil, err
	}
	resp, err := c.ReadLongFrame(ctx)
	if err != nil {
		return nil, err
	}
	return resp.Decode()
}
