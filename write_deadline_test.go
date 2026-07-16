package gombus

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingWriteConn captures whether SetWriteDeadline was called and rejects
// every Write so we can observe the deadline being applied.
type blockingWriteConn struct {
	mockConn
}

func (b *blockingWriteConn) Write(_ []byte) (int, error) {
	if !b.writeDeadlineSet {
		return 0, errors.New("write happened without SetWriteDeadline")
	}
	return 0, errors.New("simulated write failure")
}

func TestReadSingleFrameSetsWriteDeadline(t *testing.T) {
	c := &blockingWriteConn{}
	_, err := NewClient(c).ReadSingleFrame(t.Context(), 1)
	require.Error(t, err)
	assert.True(t, c.writeDeadlineSet, "ReadSingleFrame must call SetWriteDeadline before Write")
}

func TestReadAllFramesSetsWriteDeadline(t *testing.T) {
	c := &blockingWriteConn{}
	_, err := NewClient(c).ReadAllFrames(t.Context(), 1)
	require.Error(t, err)
	assert.True(t, c.writeDeadlineSet, "ReadAllFrames must call SetWriteDeadline before Write")
}

// scriptedConn pairs Reads to Writes: each Write enqueues the next response
// from the script. Reads only see bytes that a prior Write has produced.
// This prevents bufio's read-ahead in ReadSingleCharFrame from gobbling
// later frames that should be visible to subsequent ReadLongFrame calls.
type scriptedConn struct {
	responses [][]byte
	writeIdx  int
	pending   []byte
}

func (s *scriptedConn) Read(b []byte) (int, error) {
	if len(s.pending) == 0 {
		return 0, io.EOF
	}
	n := copy(b, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *scriptedConn) Write(b []byte) (int, error) {
	if s.writeIdx >= len(s.responses) {
		return 0, errors.New("scriptedConn: more writes than scripted responses")
	}
	s.pending = append(s.pending, s.responses[s.writeIdx]...)
	s.writeIdx++
	return len(b), nil
}

func (*scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (*scriptedConn) SetWriteDeadline(time.Time) error { return nil }
func (*scriptedConn) Close() error                     { return nil }

// TestReadAllFramesMultiFrame exercises the FCB-walk loop with two real
// captured frames: the first one ends with the 0x1F "more records follow"
// sentinel, the second does not. ReadAllFrames must return both in order
// and stop after the second.
func TestReadAllFramesMultiFrame(t *testing.T) {
	frame1 := hexToBytes(`
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
		ef 16`)

	frame2 := hexToBytes(`
		68 56 56 68 08 02 72 36 46 00 19 77 04 14 07 9d 10 00 00 0c 78 36 46 00
		19 0d 7c 08 44 49 20 2e 74 73 75 63 0a 20 20 20 20 20 20 20 20 20 20 04
		6d 35 14 d3 26 02 7c 09 65 6d 69 74 20 2e 74 61 62 97 10 04 13 01 6e 03
		00 04 93 7f 00 00 00 00 44 13 27 51 03 00 0f 00 00 1f 96 16`)

	c := &scriptedConn{
		responses: [][]byte{
			{SingleCharacterFrame}, // ack to SND_NKE
			frame1,                 // first REQ_UD2 response (HasMoreRecords=true)
			frame2,                 // second REQ_UD2 response (terminal)
		},
	}

	frames, err := NewClient(c).ReadAllFrames(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, frames, 2, "expected two frames before HasMoreRecords cleared")

	assert.True(t, frames[0].HasMoreRecords(), "frame[0] must signal more records")
	assert.False(t, frames[1].HasMoreRecords(), "frame[1] is terminal")

	// Cross-check: the two real frames belong to different meters, so the
	// loop is plumbing both decoded responses through (not duplicating one).
	assert.Equal(t, 90072114, frames[0].SerialNumber)
	assert.Equal(t, 19004636, frames[1].SerialNumber)

	// All scripted responses consumed: 1 SND_NKE + 2 REQ_UD2.
	assert.Equal(t, 3, c.writeIdx, "ReadAllFrames should issue exactly one SND_NKE and two REQ_UD2")
}

// TestReadAllFramesStopsOnFirstTerminal confirms that when the very first
// response carries no HasMoreRecords sentinel, the loop exits after one
// iteration without issuing a second REQ_UD2.
func TestReadAllFramesStopsOnFirstTerminal(t *testing.T) {
	terminal := hexToBytes(`
		68 56 56 68 08 02 72 36 46 00 19 77 04 14 07 9d 10 00 00 0c 78 36 46 00
		19 0d 7c 08 44 49 20 2e 74 73 75 63 0a 20 20 20 20 20 20 20 20 20 20 04
		6d 35 14 d3 26 02 7c 09 65 6d 69 74 20 2e 74 61 62 97 10 04 13 01 6e 03
		00 04 93 7f 00 00 00 00 44 13 27 51 03 00 0f 00 00 1f 96 16`)

	c := &scriptedConn{
		responses: [][]byte{
			{SingleCharacterFrame},
			terminal,
		},
	}

	frames, err := NewClient(c).ReadAllFrames(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.False(t, frames[0].HasMoreRecords())
	assert.Equal(t, 2, c.writeIdx, "must not issue extra REQ_UD2 once HasMoreRecords clears")
}
