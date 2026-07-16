package gombus

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTCPConnReadLongFrame replays a known-good Itron water-meter frame over
// a net.Pipe(), exercising the real *conn implementation including its
// timeout handling and Read/Write/Close path.
func TestTCPConnReadLongFrame(t *testing.T) {
	frameHex := `68 56 56 68 08 02 72 36 46 00 19 77 04 14 07 40
		10 00 00 0c 78 36 46 00 19 0d 7c 08 44 49 20 2e
		74 73 75 63 0a 20 20 20 20 20 20 20 20 20 20 04
		6d 32 16 d0 26 02 7c 09 65 6d 69 74 20 2e 74 61
		62 9a 10 04 13 75 68 03 00 04 93 7f 00 00 00 00
		44 13 27 51 03 00 0f 00 00 1f a6 16`
	data := hexToBytes(frameHex)

	server, client := net.Pipe()
	c := &conn{conn: client}
	go func() {
		for _, b := range data {
			_, err := server.Write([]byte{b})
			assert.NoError(t, err)
		}
		assert.NoError(t, server.Close())
	}()

	frame, err := NewClient(c).ReadLongFrame(t.Context())
	require.NoError(t, err)
	assert.Len(t, frame, 92)

	df, err := frame.Decode()
	require.NoError(t, err)
	assert.InDelta(t, 217.383, df.DataRecords[6].Value, 1e-6)
}

// TestTCPConnWriteAndClose round-trips Write through the *conn wrapper and
// verifies SetWriteDeadline + Close don't error on a live socket.
func TestTCPConnWriteAndClose(t *testing.T) {
	server, client := net.Pipe()
	c := &conn{conn: client}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 5)
		n, err := server.Read(buf)
		assert.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, []byte("hello"), buf[:n])
	}()

	require.NoError(t, c.SetWriteDeadline(time.Now().Add(time.Second)))
	n, err := c.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	<-done
	require.NoError(t, c.Close())
}

// TestTCPDialTCPRefused exercises the error path of DialTCP. It targets a
// locally-bound, immediately-closed port to provoke a connection-refused
// from the kernel without depending on a remote service.
func TestTCPDialTCPRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	_, err = DialTCP(addr)
	assert.Error(t, err)
}
