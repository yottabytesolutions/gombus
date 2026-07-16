package gombus

import (
	"time"

	"go.bug.st/serial"
)

// DialSerial opens a serial port configured for M-Bus communication
// (2400 baud, 8 data bits, even parity, 1 stop bit).
func DialSerial(device string) (Conn, error) {
	port, err := serial.Open(device, &serial.Mode{
		BaudRate: 2400,
		DataBits: 8,
		Parity:   serial.EvenParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return nil, err
	}
	return &serialConn{port: port}, nil
}

// serialConn adapts a go.bug.st/serial Port to the Conn interface. The port is
// a named field rather than embedded: embedding promotes SetReadTimeout onto
// the public type, which lets a caller arm serial.NoTimeout and defeat every
// read deadline this package sets.
type serialConn struct {
	port serial.Port
}

func (s *serialConn) Read(b []byte) (int, error) { return s.port.Read(b) }

func (s *serialConn) Write(b []byte) (int, error) { return s.port.Write(b) }

func (s *serialConn) Close() error { return s.port.Close() }

// SetReadDeadline maps an absolute deadline onto the port's relative read
// timeout, which the library re-arms on every Read. The zero time clears the
// deadline, matching net.Conn. A deadline that has already passed arms a zero
// timeout so the next Read polls once and returns: the raw time.Until value is
// negative, which the library rejects, and clamping it to -1 would mean
// serial.NoTimeout and block forever.
func (s *serialConn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		return s.port.SetReadTimeout(serial.NoTimeout)
	}
	timeout := time.Until(t)
	if timeout < 0 {
		timeout = 0
	}
	return s.port.SetReadTimeout(timeout)
}

// SetWriteDeadline is a no-op. go.bug.st/serial v1.8.0 writes synchronously and
// exposes no write timeout.
func (*serialConn) SetWriteDeadline(time.Time) error { return nil }
