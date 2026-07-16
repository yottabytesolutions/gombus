package gombus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// validLongFrame returns a small well-formed long frame: 0x68 L L 0x68, five
// data bytes, checksum, stop.
func validLongFrame() []byte {
	return []byte{0x68, 0x05, 0x05, 0x68, 0x01, 0x02, 0x03, 0x04, 0x05, 0x0F, 0x16}
}

// chunkConn hands out exactly the chunks it is given, one per Read, so a test
// can put a frame boundary anywhere inside a Read. This is the transport
// behaviour a TCP concentrator produces and serial at 2400 baud does not.
type chunkConn struct {
	chunks [][]byte
	reads  atomic.Int64
}

func (c *chunkConn) Read(b []byte) (int, error) {
	c.reads.Add(1)
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(b, c.chunks[0])
	if n < len(c.chunks[0]) {
		c.chunks[0] = c.chunks[0][n:]
	} else {
		c.chunks = c.chunks[1:]
	}
	return n, nil
}

func (c *chunkConn) Write(b []byte) (int, error)    { return len(b), nil }
func (*chunkConn) SetReadDeadline(time.Time) error  { return nil }
func (*chunkConn) SetWriteDeadline(time.Time) error { return nil }
func (*chunkConn) Close() error                     { return nil }

// TestReadLongFramePipelined pins the framing-desync fix. Two frames delivered
// in ONE transport Read must both come back. Before the fix the reader returned
// from inside its byte loop and dropped everything after the first stop byte,
// so the second frame vanished.
func TestReadLongFramePipelined(t *testing.T) {
	frame := validLongFrame()

	cases := []struct {
		name   string
		chunks [][]byte
	}{
		{
			name:   "two frames in one read",
			chunks: [][]byte{append(append([]byte{}, frame...), frame...)},
		},
		{
			// The nastier half: a Read that ends mid-frame. The next
			// ReadLongFrame must resume, not restart on a half frame.
			name: "read straddles the frame boundary",
			chunks: [][]byte{
				append(append([]byte{}, frame...), frame[:4]...),
				frame[4:],
			},
		},
		{
			name: "every byte in its own read",
			chunks: func() [][]byte {
				stream := append(append([]byte{}, frame...), frame...)
				out := make([][]byte, 0, len(stream))
				for _, b := range stream {
					out = append(out, []byte{b})
				}
				return out
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(&chunkConn{chunks: tc.chunks})

			first, err := client.ReadLongFrame(t.Context())
			if err != nil {
				t.Fatalf("first frame: %v", err)
			}
			if !bytes.Equal(first, frame) {
				t.Fatalf("first frame: got %x, want %x", first, frame)
			}

			second, err := client.ReadLongFrame(t.Context())
			if err != nil {
				t.Fatalf("second frame lost: %v", err)
			}
			if !bytes.Equal(second, frame) {
				t.Fatalf("second frame: got %x, want %x", second, frame)
			}
		})
	}
}

// TestReadLongFrameNoAliasing pins that a returned frame does not share memory
// with the Client's buffer. It does if the reader hands back a subslice, and
// the corruption only shows up once a later frame overwrites it.
func TestReadLongFrameNoAliasing(t *testing.T) {
	frame := validLongFrame()
	client := NewClient(&chunkConn{chunks: [][]byte{append(append([]byte{}, frame...), frame...)}})

	first, err := client.ReadLongFrame(t.Context())
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if _, err = client.ReadLongFrame(t.Context()); err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if !bytes.Equal(first, frame) {
		t.Fatalf("first frame was corrupted by the second read: got %x, want %x", first, frame)
	}
}

func TestReadLongFrameContext(t *testing.T) {
	t.Run("cancel returns promptly, not after the frame timeout", func(t *testing.T) {
		client := NewClient(&silentConn{blockUntilDeadline: true})
		ctx, cancel := context.WithCancel(context.Background())

		start := time.Now()
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		_, err := client.ReadLongFrame(ctx)
		elapsed := time.Since(start)

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected errors.Is(err, context.Canceled), got %v", err)
		}
		// Generous bound: the point is that it does not wait out frameReadTimeout.
		if elapsed > frameReadTimeout/2 {
			t.Fatalf("cancel took %v, want well under the %v frame timeout", elapsed, frameReadTimeout)
		}
	})

	t.Run("earlier ctx deadline wins", func(t *testing.T) {
		client := NewClient(&silentConn{blockUntilDeadline: true})
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := client.ReadLongFrame(ctx)
		elapsed := time.Since(start)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected errors.Is(err, context.DeadlineExceeded), got %v", err)
		}
		if elapsed > frameReadTimeout/2 {
			t.Fatalf("read took %v, want ~200ms: the ctx deadline must win", elapsed)
		}
	})

	t.Run("later ctx deadline does not extend the frame timeout", func(t *testing.T) {
		client := NewClient(&silentConn{blockUntilDeadline: true})
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()

		start := time.Now()
		_, err := client.ReadLongFrame(ctx)
		elapsed := time.Since(start)

		if !errors.Is(err, ErrReadTimeout) {
			t.Fatalf("expected errors.Is(err, ErrReadTimeout), got %v", err)
		}
		if elapsed > 2*frameReadTimeout {
			t.Fatalf("read took %v, want ~%v: a distant ctx deadline must not extend it", elapsed, frameReadTimeout)
		}
	})

	t.Run("already cancelled ctx never touches the bus", func(t *testing.T) {
		conn := &silentConn{}
		client := NewClient(conn)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := client.ReadLongFrame(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected errors.Is(err, context.Canceled), got %v", err)
		}
		if n := conn.readCount(); n != 0 {
			t.Fatalf("read the transport %d time(s) on a cancelled context, want 0", n)
		}
	})
}

func TestReadLongFrameValidation(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		err    error // expected wrapped error (nil = success)
		length int   // expected frame length on success
	}{
		{
			name:   "valid frame",
			data:   []byte{0x68, 0x05, 0x05, 0x68, 0x01, 0x02, 0x03, 0x04, 0x05, 0x0F, 0x16},
			length: 11,
		},
		{
			name: "invalid start sequence",
			data: []byte{0x69, 0x05, 0x05, 0x68, 0x01, 0x02, 0x03, 0x04, 0x05, 0x0F, 0x16},
			err:  ErrNoLongFrameFound,
		},
		{
			name: "mismatched length bytes",
			data: []byte{0x68, 0x05, 0x06, 0x68, 0x01, 0x02, 0x03, 0x04, 0x05, 0x0F, 0x16},
			err:  ErrNoLongFrameFound,
		},
		{
			name: "no end byte",
			data: []byte{0x68, 0x05, 0x05, 0x68, 0x01, 0x02, 0x03, 0x04, 0x05, 0x0F},
			err:  ErrNoLongFrameFound,
		},
		{
			name: "implausibly large length truncated stream",
			data: []byte{0x68, 0xFF, 0xFF, 0x68, 0x01, 0x02, 0x03},
			err:  ErrNoLongFrameFound,
		},
		{
			// Checksum byte happens to equal 0x16. The strict reader must
			// not stop one byte early on it.
			name: "checksum byte equals stop sentinel",
			data: []byte{
				0x68, 0x05, 0x05, 0x68,
				0x01, 0x02, 0x03, 0x04, 0x0C, // sum = 0x16
				0x16, // checksum
				0x16, // stop
			},
			length: 11,
		},
		{
			name: "bad checksum",
			data: []byte{
				0x68, 0x05, 0x05, 0x68,
				0x01, 0x02, 0x03, 0x04, 0x05,
				0x00, // checksum should be 0x0F
				0x16,
			},
			err: ErrChecksumMismatch,
		},
		{
			name: "stop byte missing at expected position",
			data: []byte{
				0x68, 0x05, 0x05, 0x68,
				0x01, 0x02, 0x03, 0x04, 0x05,
				0x0F, // checksum
				0x00, // not 0x16
			},
			err: ErrInvalidFrame,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &mockConn{readData: tc.data}
			frame, err := NewClient(conn).ReadLongFrame(t.Context())
			if tc.err != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.err)
				}
				if !errors.Is(err, tc.err) {
					t.Fatalf("expected errors.Is(err, %v), got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(frame) != tc.length {
				t.Fatalf("expected length %d, got %d", tc.length, len(frame))
			}
		})
	}
}

func TestReadSingleCharFrame(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		err  bool
	}{
		{"valid 0xE5 byte", []byte{0xE5}, false},
		{"data ending in 0xE5", []byte{0x01, 0x02, 0xE5}, false},
		{"no 0xE5", []byte{0x01, 0x02, 0x03}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := NewClient(&mockConn{readData: tc.data}).ReadSingleCharFrame(t.Context())
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(frame) == 0 || frame[len(frame)-1] != SingleCharacterFrame {
				t.Fatalf("expected frame ending in 0xE5, got %x", frame)
			}
		})
	}
}

// TestReadSingleCharFrameSilentMeter pins the same (0, nil) spin one function
// over from ReadLongFrame. Before the fix a bufio reader absorbed up to 100
// consecutive empty reads, each re-arming a fresh relative timeout on serial:
// ~200s against a 2s budget, surfacing as io.ErrNoProgress rather than a
// timeout.
func TestReadSingleCharFrameSilentMeter(t *testing.T) {
	for _, tc := range silentConns() {
		t.Run(tc.name, func(t *testing.T) {
			conn := tc.conn()
			assertSilentReadTimesOut(t, conn, func() error {
				_, err := NewClient(conn).ReadSingleCharFrame(t.Context())
				return err
			})
		})
	}
}

// TestReadSingleCharFrameKeepsTrailingFrame pins the ack-plus-frame case: a
// slave's E5h ack and the long frame that follows commonly land in one Read.
// The frame must survive for the next read on the same Client.
func TestReadSingleCharFrameKeepsTrailingFrame(t *testing.T) {
	frame := validLongFrame()
	conn := &mockConn{readData: append([]byte{SingleCharacterFrame}, frame...)}
	client := NewClient(conn)

	ack, err := client.ReadSingleCharFrame(t.Context())
	if err != nil {
		t.Fatalf("reading ack: %v", err)
	}
	if len(ack) != 1 || ack[0] != SingleCharacterFrame {
		t.Fatalf("expected the E5h byte, got %x", ack)
	}

	// The conn is drained: everything is buffered inside the Client now, which
	// is exactly why the Client and not the Conn has to own it.
	got, err := client.ReadLongFrame(t.Context())
	if err != nil {
		t.Fatalf("frame after the ack was lost: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("got frame %x, want %x", got, frame)
	}
}

func TestReadAnyAndPrintTimeout(t *testing.T) {
	conn := &mockConn{readData: []byte{0x01, 0x02, 0x03}, timeout: true}
	if err := readAnyAndPrint(conn); err == nil {
		t.Fatal("expected error from forced timeout")
	}
}

// silentTransport is a Conn that never answers, in one of the shapes a real
// silent meter presents. readCount reports how hard a caller spun on it.
type silentTransport interface {
	Conn
	readCount() int64
}

// silentConns enumerates the ways a silent meter fails. Every read path must
// time out against all of them.
func silentConns() []struct {
	name string
	conn func() silentTransport
} {
	return []struct {
		name string
		conn func() silentTransport
	}{
		{
			name: "transport honours the deadline",
			conn: func() silentTransport { return &silentConn{blockUntilDeadline: true} },
		},
		{
			name: "transport ignores the deadline",
			conn: func() silentTransport { return &silentConn{} },
		},
		{
			name: "transport re-arms a relative timeout per read",
			conn: func() silentTransport { return &serialSilentConn{} },
		},
	}
}

// silentConn models a transport that reports a read timeout the way
// go.bug.st/serial does: (0, nil), never an error. blockUntilDeadline mimics a
// port that respects the absolute deadline it was handed; without it the conn
// returns instantly, as a wedged transport that ignores deadlines would.
type silentConn struct {
	blockUntilDeadline bool
	deadline           time.Time
	armed              []time.Time // non-zero deadlines seen, in order
	reads              atomic.Int64
}

func (s *silentConn) readCount() int64 { return s.reads.Load() }

func (s *silentConn) Read(_ []byte) (int, error) {
	s.reads.Add(1)
	if s.blockUntilDeadline && !s.deadline.IsZero() {
		if d := time.Until(s.deadline); d > 0 {
			time.Sleep(d)
		}
	}
	return 0, nil
}

func (s *silentConn) Write(b []byte) (int, error) { return len(b), nil }

func (s *silentConn) SetReadDeadline(t time.Time) error {
	s.deadline = t
	if !t.IsZero() {
		s.armed = append(s.armed, t)
	}
	return nil
}

func (*silentConn) SetWriteDeadline(time.Time) error { return nil }

func (*silentConn) Close() error { return nil }

// serialSilentConn models go.bug.st/serial exactly: SetReadTimeout takes a
// RELATIVE duration that the port re-arms on every Read, and a timeout reports
// as (0, nil). This is the shape that made the old bufio-based
// ReadSingleCharFrame burn ~200s against a 2s budget, because bufio absorbs 100
// consecutive empty reads and each one got a fresh full timeout.
type serialSilentConn struct {
	timeout atomic.Int64 // relative read timeout, re-armed on every Read
	reads   atomic.Int64
}

func (s *serialSilentConn) readCount() int64 { return s.reads.Load() }

func (s *serialSilentConn) Read(_ []byte) (int, error) {
	s.reads.Add(1)
	if d := time.Duration(s.timeout.Load()); d > 0 {
		time.Sleep(d)
	}
	return 0, nil
}

func (s *serialSilentConn) Write(b []byte) (int, error) { return len(b), nil }

// SetReadDeadline mirrors serialConn: an absolute deadline collapses to a
// relative duration at the moment it is set, and the zero time clears it.
func (s *serialSilentConn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		s.timeout.Store(0)
		return nil
	}
	d := time.Until(t)
	if d < 0 {
		d = 0
	}
	s.timeout.Store(int64(d))
	return nil
}

func (*serialSilentConn) SetWriteDeadline(time.Time) error { return nil }

func (*serialSilentConn) Close() error { return nil }

// TestReadLongFrameSilentMeter pins the fix for the (0, nil) spin: a meter that
// never answers must make ReadLongFrame return a timeout, not loop forever.
// Before the fix this test hangs until the bounded deadline below fires.
func TestReadLongFrameSilentMeter(t *testing.T) {
	for _, tc := range silentConns() {
		t.Run(tc.name, func(t *testing.T) {
			conn := tc.conn()
			assertSilentReadTimesOut(t, conn, func() error {
				_, err := NewClient(conn).ReadLongFrame(t.Context())
				return err
			})
		})
	}
}

// assertSilentReadTimesOut runs read against a silent transport and requires it
// to report ErrReadTimeout inside a bounded budget. The bound is what stops a
// regression from hanging CI: the failure mode under test is an unbounded loop.
func assertSilentReadTimesOut(t *testing.T, conn silentTransport, read func() error) {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- read() }()

	select {
	case err := <-done:
		if errors.Is(err, io.ErrNoProgress) {
			t.Fatalf("a silent meter must read as a timeout, not as %v", err)
		}
		if !errors.Is(err, ErrReadTimeout) {
			t.Fatalf("expected errors.Is(err, ErrReadTimeout), got %v", err)
		}
	case <-time.After(frameReadTimeout * 5):
		t.Fatalf("read did not return within %v; spun through %d reads", frameReadTimeout*5, conn.readCount())
	}
}

// TestReadLongFrameDeadlineIsPerFrame pins the per-read vs per-frame mismatch:
// the budget belongs to the frame, not to each Read within it.
//
// Reads are armed one poll interval at a time so cancellation is noticed, so
// the arms move forward. What must never happen is an arm reaching past the
// frame's own deadline: that is a transport being handed a fresh full timeout,
// which is the bug. Assert the bound, not the mechanism.
func TestReadLongFrameDeadlineIsPerFrame(t *testing.T) {
	conn := &silentConn{}
	start := time.Now()

	if _, err := NewClient(conn).ReadLongFrame(t.Context()); !errors.Is(err, ErrReadTimeout) {
		t.Fatalf("expected errors.Is(err, ErrReadTimeout), got %v", err)
	}

	if len(conn.armed) < 2 {
		t.Fatalf("expected the read deadline to be armed on every read, got %d arms", len(conn.armed))
	}
	// Slack absorbs the gap between start and the deadline being computed.
	budgetEnds := start.Add(frameReadTimeout + 50*time.Millisecond)
	for i, armed := range conn.armed {
		if armed.After(budgetEnds) {
			t.Fatalf("arm %d reaches %v past the frame budget: the frame timeout is being re-armed per read",
				i, armed.Sub(budgetEnds))
		}
	}
	if elapsed := time.Since(start); elapsed > 2*frameReadTimeout {
		t.Fatalf("frame read took %v, want at most %v", elapsed, 2*frameReadTimeout)
	}
}
