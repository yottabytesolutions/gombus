package gombus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	os.Exit(m.Run())
}

// TestAddressValidators pins the two rules against each other. A destination may
// be 0xFD or 0xFE; an address written into a meter may not, because writing 0xFE
// makes the meter answer every broadcast and cannot be undone over the bus. The
// shared table makes a future merge of the two validators fail loudly.
func TestAddressValidators(t *testing.T) {
	cases := []struct {
		name          string
		addr          uint8
		wantDestErr   bool
		wantAssignErr bool
	}{
		{name: "zero is unconfigured", addr: 0, wantDestErr: true, wantAssignErr: true},
		{name: "lowest meter address", addr: 1},
		{name: "typical", addr: 42},
		{name: "highest meter address", addr: 250},
		{name: "251 reserved", addr: 251, wantDestErr: true, wantAssignErr: true},
		{name: "252 reserved", addr: 252, wantDestErr: true, wantAssignErr: true},
		{
			name:          "253 is a legal destination but must never be written",
			addr:          253,
			wantAssignErr: true,
		},
		{
			name:          "254 broadcast-with-reply reads fine but bricks if written",
			addr:          254,
			wantAssignErr: true,
		},
		{
			name:          "255 broadcast without reply can never answer",
			addr:          255,
			wantDestErr:   true,
			wantAssignErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertAddrErr(t, "validateDestinationAddr", validateDestinationAddr(tc.addr), tc.wantDestErr, tc.addr)
			assertAddrErr(t, "validateAssignableAddr", validateAssignableAddr(tc.addr), tc.wantAssignErr, tc.addr)
		})
	}
}

func assertAddrErr(t *testing.T, fn string, err error, wantErr bool, addr uint8) {
	t.Helper()
	if wantErr {
		if !errors.Is(err, ErrInvalidPrimaryID) {
			t.Fatalf("%s(%d): expected errors.Is(err, ErrInvalidPrimaryID), got %v", fn, addr, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s(%d): unexpected error: %v", fn, addr, err)
	}
}

// TestReadFramesRejectInvalidDestination checks that both entry points refuse an
// unusable destination before touching the bus. Previously primaryID was an int
// truncated to uint8 at the call to RequestUD2, so 300 silently addressed 44.
func TestReadFramesRejectInvalidDestination(t *testing.T) {
	ctx := t.Context()
	reads := []struct {
		name string
		read func(Conn, uint8) error
	}{
		{
			name: "ReadAllFrames",
			read: func(c Conn, a uint8) error { _, err := NewClient(c).ReadAllFrames(ctx, a); return err },
		},
		{
			name: "ReadSingleFrame",
			read: func(c Conn, a uint8) error { _, err := NewClient(c).ReadSingleFrame(ctx, a); return err },
		},
	}
	addrs := []struct {
		name string
		addr uint8
	}{
		{name: "zero", addr: 0},
		{name: "reserved 251", addr: 251},
		{name: "broadcast without reply", addr: 255},
	}

	for _, r := range reads {
		for _, a := range addrs {
			t.Run(r.name+"/"+a.name, func(t *testing.T) {
				conn := &countingWriteConn{}
				if err := r.read(conn, a.addr); !errors.Is(err, ErrInvalidPrimaryID) {
					t.Fatalf("expected errors.Is(err, ErrInvalidPrimaryID), got %v", err)
				}
				if conn.writes != 0 {
					t.Fatalf("wrote %d frame(s) to the bus for address %d, want 0", conn.writes, a.addr)
				}
			})
		}
	}
}

// TestReadFramesAcceptBroadcastReply pins the workflow mbusctl documents at the
// -addr flag: reading from 0xFE on a single-meter bus. A validator that treats
// the destination and the written address as one rule breaks this.
func TestReadFramesAcceptBroadcastReply(t *testing.T) {
	for _, addr := range []uint8{addrSecondarySelect, addrBroadcastReply} {
		t.Run(fmt.Sprintf("addr %d", addr), func(t *testing.T) {
			conn := &countingWriteConn{}
			// The conn never answers, so this must fail at the read, never at
			// validation. Reaching a timeout proves the address was accepted.
			err := func() error { _, err := NewClient(conn).ReadSingleFrame(t.Context(), addr); return err }()
			if errors.Is(err, ErrInvalidPrimaryID) {
				t.Fatalf("address %d must be a valid destination, got %v", addr, err)
			}
			if conn.writes == 0 {
				t.Fatalf("expected a REQ_UD2 to be sent to %d", addr)
			}
		})
	}
}

// countingWriteConn records writes and never answers, so a rejected address is
// visible as zero bus traffic.
type countingWriteConn struct {
	mockConn
	writes int
}

func (c *countingWriteConn) Write(b []byte) (int, error) {
	c.writes++
	return len(b), nil
}

// TestReadAllFramesSilentMeter covers the user-facing path: ReadAllFrames waits
// for the SND_NKE ack via ReadSingleCharFrame, so a meter that never answers
// must surface as a timeout there rather than hanging the caller.
func TestReadAllFramesSilentMeter(t *testing.T) {
	for _, tc := range silentConns() {
		t.Run(tc.name, func(t *testing.T) {
			conn := tc.conn()
			assertSilentReadTimesOut(t, conn, func() error {
				_, err := NewClient(conn).ReadAllFrames(t.Context(), 1)
				return err
			})
		})
	}
}

// chattyConn answers every REQ_UD2 with the same frame, which carries the 0x1F
// "more records follow" sentinel. It models a slave whose FCB walk never ends.
type chattyConn struct {
	frame   []byte
	pending []byte
	writes  int
}

func (c *chattyConn) Read(b []byte) (int, error) {
	if len(c.pending) == 0 {
		return 0, nil
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *chattyConn) Write(b []byte) (int, error) {
	c.writes++
	if c.writes == 1 {
		c.pending = append(c.pending, SingleCharacterFrame) // ack to SND_NKE
	} else {
		c.pending = append(c.pending, c.frame...)
	}
	return len(b), nil
}

func (*chattyConn) SetReadDeadline(time.Time) error  { return nil }
func (*chattyConn) SetWriteDeadline(time.Time) error { return nil }
func (*chattyConn) Close() error                     { return nil }

// TestReadAllFramesBoundsFCBWalk pins the fix for the unbounded walk. A slave
// that always sets the more-records sentinel must produce an error, not grow
// the frame slice until the process dies.
func TestReadAllFramesBoundsFCBWalk(t *testing.T) {
	// Real frame ending in the 0x1F sentinel, so HasMoreRecords stays true.
	conn := &chattyConn{
		frame: hexToBytes(`
			68 78 78 68 08 01 72 14 21 07 90 36 1c c7 02 25 00 00 00
			84 40 2a a0 09 00 00
			84 80 40 2a ba 00 00 00
			84 c0 40 2a 00 00 00 00
			84 40 fb 97 72 fb fe ff ff
			84 80 40 fb 97 72 4b 00 00 00
			84 c0 40 fb 97 72 00 00 00 00
			84 40 fb b7 72 ae 09 00 00
			84 80 40 fb b7 72 c8 00 00 00
			84 c0 40 fb b7 72 00 00 00 00
			82 40 fd ba 73 e2 03
			82 80 40 fd ba 73 9f 03
			82 c0 40 fd ba 73 00 00 1f
			ef 16`),
	}

	type result struct {
		frames []*DecodedFrame
		err    error
	}
	done := make(chan result, 1)
	go func() {
		frames, err := NewClient(conn).ReadAllFrames(context.Background(), 1)
		done <- result{frames, err}
	}()

	select {
	case got := <-done:
		if !errors.Is(got.err, ErrTooManyFrames) {
			t.Fatalf("expected errors.Is(err, ErrTooManyFrames), got %v after %d frames", got.err, len(got.frames))
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("ReadAllFrames did not return; walked at least %d frames", conn.writes)
	}
}
