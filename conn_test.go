package gombus

import (
	"bytes"
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// The owner's requirement is "support any medium, not just serial or TCP". That
// promise is only worth anything if the type system enforces it, so these are
// compile-time assertions: a doc comment cannot fail, but a build can.
//
// Adding a method to Conn breaks this line. That is the point. Conn must stay
// net.Conn's shape so every net.Conn is usable with no adapter.
var _ Conn = (net.Conn)(nil)

// The mediums that guarantee buys us, spelled out so a reader does not have to
// take net.Conn's word for it.
var (
	_ Conn = (*net.TCPConn)(nil)  // TCP, including a concentrator
	_ Conn = (*tls.Conn)(nil)     // TLS, e.g. a gateway over the public internet
	_ Conn = (*net.UnixConn)(nil) // unix socket, e.g. a local daemon
	_ Conn = (*net.UDPConn)(nil)  // UDP datagrams
	_ Conn = (*net.IPConn)(nil)   // raw IP
)

// And the transports this package ships itself.
var (
	_ Conn = (*conn)(nil)       // DialTCP
	_ Conn = (*serialConn)(nil) // DialSerial
)

// TestRawNetConnNeedsNoAdapter is the runtime half of the assertions above: a
// net.Conn straight from the standard library, with no wrapper of ours around
// it, reads a frame. If this ever needs an adapter, the "any medium" promise is
// broken no matter what the doc says.
func TestRawNetConnNeedsNoAdapter(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })

	frame := validLongFrame()
	go func() {
		_, err := server.Write(frame)
		if err != nil {
			t.Errorf("writing frame: %v", err)
		}
	}()

	// client is a net.Conn. It is passed to NewClient unwrapped.
	got, err := NewClient(client).ReadLongFrame(t.Context())
	if err != nil {
		t.Fatalf("reading over a raw net.Conn: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("got %x, want %x", got, frame)
	}
}

// TestConnDeadlineContract pins the two ways a transport may report "no data
// yet", because Conn's doc promises both are accepted and an implementer will
// pick one. A net.Conn reports a timeout error; go.bug.st/serial reports
// (0, nil). Neither may surface as a failure before the frame deadline.
func TestConnDeadlineContract(t *testing.T) {
	cases := []struct {
		name string
		conn func() silentTransport
	}{
		{
			name: "timeout reported as an error",
			conn: func() silentTransport { return &deadlineErrConn{} },
		},
		{
			name: "timeout reported as (0, nil)",
			conn: func() silentTransport { return &silentConn{blockUntilDeadline: true} },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := tc.conn()
			assertSilentReadTimesOut(t, conn, func() error {
				_, err := NewClient(conn).ReadLongFrame(t.Context())
				return err
			})
		})
	}
}

// deadlineErrConn reports a read timeout the way net.Conn does: an error for
// which net.Error.Timeout reports true.
type deadlineErrConn struct {
	deadline time.Time
	reads    atomic.Int64
}

func (d *deadlineErrConn) readCount() int64 { return d.reads.Load() }

func (d *deadlineErrConn) Read(_ []byte) (int, error) {
	d.reads.Add(1)
	if !d.deadline.IsZero() {
		if wait := time.Until(d.deadline); wait > 0 {
			time.Sleep(wait)
		}
	}
	return 0, &net.OpError{Op: "read", Err: timeoutError{}}
}

func (d *deadlineErrConn) Write(b []byte) (int, error) { return len(b), nil }

func (d *deadlineErrConn) SetReadDeadline(t time.Time) error { d.deadline = t; return nil }

func (*deadlineErrConn) SetWriteDeadline(time.Time) error { return nil }

func (*deadlineErrConn) Close() error { return nil }

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
