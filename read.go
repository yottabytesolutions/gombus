package gombus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

var (
	ErrNoLongFrameFound = errors.New("no long frame found")
	ErrInvalidFrame     = errors.New("invalid frame format")
	ErrChecksumMismatch = errors.New("frame checksum mismatch")
	ErrReadTimeout      = errors.New("read timed out")
)

// errNeedMore reports that a buffer does not hold a complete frame yet. It is
// internal: callers of the exported API never see it.
var errNeedMore = errors.New("incomplete frame")

const (
	// frameReadTimeout is the maximum time allowed to read a complete frame
	// when the caller's context does not impose something shorter.
	frameReadTimeout = 2 * time.Second
	// minFrameLength is the smallest legal L: C, A and CI. L is one byte, so
	// the upper bound is 255 and a whole frame never exceeds L + 6 bytes.
	minFrameLength = 3
	// readPollInterval bounds how long one transport Read may block. It is not
	// a timeout: the frame deadline still decides when to give up. It exists so
	// context cancellation is noticed on every transport, including those whose
	// Read cannot be interrupted from another goroutine.
	readPollInterval = 50 * time.Millisecond
	// silentReadBackoff paces the loop when a transport reports its read
	// timeout as (0, nil) and returns immediately instead of blocking.
	silentReadBackoff = 5 * time.Millisecond
	// readChunkSize is the scratch size for one transport Read.
	readChunkSize = 256
)

// parseLongFrame parses one long frame from the head of buf and reports how
// many bytes it consumed, leaving the caller to keep the remainder. That is the
// point: one transport Read can carry the tail of one frame and the head of the
// next, and a parser that cannot say where it stopped forces its caller to
// throw the remainder away.
//
// It returns errNeedMore when buf does not hold a complete frame yet. Pure
// function over bytes: no IO, no deadlines, no transport.
func parseLongFrame(buf []byte) (LongFrame, int, error) {
	// Start sequence: 0x68 LL LL 0x68.
	if len(buf) < 4 {
		return nil, 0, errNeedMore
	}
	if buf[0] != 0x68 || buf[3] != 0x68 || buf[1] != buf[2] {
		return nil, 0, ErrNoLongFrameFound
	}

	length := int(buf[1])
	if length < minFrameLength {
		return nil, 0, ErrInvalidFrame
	}

	// Total frame size is 4 (start) + L (data) + 2 (checksum + stop).
	total := length + 6
	if len(buf) < total {
		return nil, 0, errNeedMore
	}
	if buf[total-1] != 0x16 {
		return nil, 0, fmt.Errorf("%w: missing stop byte", ErrInvalidFrame)
	}
	if got, want := buf[length+4], calcCheckSum(buf[4:length+4]); got != want {
		return nil, 0, fmt.Errorf("%w: got 0x%02x, want 0x%02x", ErrChecksumMismatch, got, want)
	}

	// Copy: the caller compacts its buffer as soon as we return, so the frame
	// must not alias it.
	frame := make(LongFrame, total)
	copy(frame, buf[:total])
	return frame, total, nil
}

// ReadLongFrame reads one M-Bus long frame and verifies its structure and
// checksum. Bytes that arrive after the stop byte are kept for the next read on
// this Client, so frames pipelined into a single transport Read are not lost.
//
// The read ends at the earlier of frameReadTimeout and ctx's deadline. The read
// deadline is cleared on return so an unrelated read on the same Conn is not
// bound by this call's timeout.
func (c *Client) ReadLongFrame(ctx context.Context) (LongFrame, error) {
	deadline := frameDeadline(ctx)
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()

	for {
		frame, n, err := parseLongFrame(c.buf)
		if err == nil {
			c.consume(n)
			return frame, nil
		}
		if !errors.Is(err, errNeedMore) {
			return nil, err
		}

		if err := c.fill(ctx, deadline); err != nil {
			return nil, c.wrapFillErr(err)
		}
	}
}

// ReadSingleCharFrame reads a single character frame (E5h), discarding any bytes
// that precede it, and returns it as a LongFrame for consistency with the other
// read funcs. Bytes after the E5h are kept for the next read on this Client: a
// slave's ack and the frame that follows it often arrive together.
func (c *Client) ReadSingleCharFrame(ctx context.Context) (LongFrame, error) {
	deadline := frameDeadline(ctx)
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()

	for {
		if i := bytes.IndexByte(c.buf, SingleCharacterFrame); i >= 0 {
			c.consume(i + 1)
			return LongFrame{SingleCharacterFrame}, nil
		}
		// Nothing buffered is the ack we were told to expect, so it is noise
		// ahead of it. Dropping it keeps the stream aligned for the next frame.
		c.consume(len(c.buf))

		if err := c.fill(ctx, deadline); err != nil {
			return nil, fmt.Errorf("read single character frame: %w", err)
		}
	}
}

// wrapFillErr keeps the historical error shape: a transport failure part way
// through a frame reads as ErrNoLongFrameFound, while a timeout or a cancelled
// context reads as itself.
func (c *Client) wrapFillErr(err error) error {
	switch {
	case errors.Is(err, ErrReadTimeout),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return err
	case len(c.buf) == 0:
		return err
	default:
		return fmt.Errorf("%w: %w", ErrNoLongFrameFound, err)
	}
}

// fill blocks until at least one byte is buffered, ctx ends, or deadline passes.
//
// Each transport Read is armed for at most readPollInterval so a cancelled
// context is noticed on any transport, without ever calling SetReadDeadline
// while a Read is in flight. That would be the obvious way to interrupt a
// blocked read, but it races: go.bug.st/serial reads its timeout field inside
// Read without a lock.
func (c *Client) fill(ctx context.Context, deadline time.Time) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w with %d byte(s) buffered", ErrReadTimeout, len(c.buf))
		}

		until := time.Now().Add(readPollInterval)
		if until.After(deadline) {
			until = deadline
		}
		if err := c.conn.SetReadDeadline(until); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}

		n, err := c.conn.Read(c.tmp)
		if n > 0 {
			// Take the bytes first: an error alongside data recurs on the next
			// Read, but bytes dropped here are gone.
			c.buf = append(c.buf, c.tmp[:n]...)
			return nil
		}
		if err != nil {
			if !isTimeout(err) {
				return fmt.Errorf("read: %w", err)
			}
			continue
		}
		// (0, nil) is how go.bug.st/serial reports a timeout. A transport that
		// ignores the deadline returns it immediately, so pace the loop.
		time.Sleep(silentReadBackoff)
	}
}

// consume drops the first n buffered bytes, keeping the rest for the next read.
func (c *Client) consume(n int) {
	rest := copy(c.buf, c.buf[n:])
	c.buf = c.buf[:rest]
}

// frameDeadline returns the instant a frame read must give up: the earlier of
// ctx's deadline and frameReadTimeout from now. ctx can only tighten the bound,
// never extend it.
func frameDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(frameReadTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}

// isTimeout reports whether err is a transport read deadline expiring rather
// than a real failure. Under the poll loop that means "no data yet".
func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// readAnyAndPrint reads from r for up to 30s, printing whatever bytes arrive
// in hex. Package-internal: a library does not write to stdout.
func readAnyAndPrint(r io.Reader) error {
	tmp := make([]byte, readChunkSize)
	timeoutReader, hasDeadline := r.(interface{ SetReadDeadline(t time.Time) error })

	const maxDuration = 30 * time.Second
	startTime := time.Now()

	for {
		if time.Since(startTime) > maxDuration {
			return fmt.Errorf("debug session timeout after %v", maxDuration)
		}
		if hasDeadline {
			if err := timeoutReader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return fmt.Errorf("set read deadline: %w", err)
			}
		}
		n, err := r.Read(tmp)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}
		if n > 0 {
			fmt.Printf("% x\n", tmp[:n])
		}
	}
}
