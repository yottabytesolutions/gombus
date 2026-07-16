package gombus

import "time"

// Conn is the transport seam: the bytes an M-Bus master sends and receives,
// and nothing else. It is deliberately the same shape as [net.Conn]'s read,
// write, deadline and close methods, so every net.Conn already satisfies it
// with no adapter: TCP, TLS, unix sockets, UDP.
//
//	c, err := net.Dial("tcp", "192.168.1.10:10001")
//	client := gombus.NewClient(c)
//
// Anything else that can move bytes with a deadline is equally welcome: a radio
// gateway, a USB dongle, a replay file, a test fake. [DialTCP] and [DialSerial]
// are conveniences, not the contract.
//
// This interface stays this small on purpose. Nothing transport-specific
// belongs on it, and neither does context.Context: the library derives read and
// write deadlines from the caller's context and applies them through
// SetReadDeadline and SetWriteDeadline. That is what lets a transport be
// implemented without knowing anything about this package.
//
// Implementations must honour the deadline contract:
//   - SetReadDeadline(t) bounds subsequent reads; the zero time means no bound.
//   - A read that reaches its deadline must report it, either as an error for
//     which net.Error.Timeout or os.ErrDeadlineExceeded reports true, or as
//     (0, nil). The library treats both as "no data yet" and never as failure.
type Conn interface {
	Read(b []byte) (n int, err error)
	Write(b []byte) (n int, err error)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}
