package gombus

import (
	"fmt"
	"time"

	"go.bug.st/serial"
)

// SerialConfig is the on-wire configuration of a serial M-Bus port. Zero is
// not accepted by DialSerialConfig: callers must opt into every non-default
// setting explicitly, avoiding a platform-specific serial default.
type SerialConfig struct {
	BaudRate int
	DataBits int
	Parity   serial.Parity
	StopBits serial.StopBits
}

// DefaultSerialConfig is the conventional wired M-Bus setting, 2400 8E1.
var DefaultSerialConfig = SerialConfig{BaudRate: 2400, DataBits: 8, Parity: serial.EvenParity, StopBits: serial.OneStopBit}

// DialSerial opens a serial port configured for M-Bus communication
// (2400 baud, 8 data bits, even parity, 1 stop bit).
func DialSerial(device string) (Conn, error) {
	return DialSerialConfig(device, DefaultSerialConfig)
}

// DialSerialConfig opens device using config. Values are validated before the
// port is opened so malformed configuration never reaches a driver.
func DialSerialConfig(device string, config SerialConfig) (Conn, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	port, err := serial.Open(device, &serial.Mode{BaudRate: config.BaudRate, DataBits: config.DataBits, Parity: config.Parity, StopBits: config.StopBits})
	if err != nil {
		return nil, err
	}
	return &serialConn{port: port}, nil
}

// Validate reports an unsafe or unsupported serial configuration.
func (c SerialConfig) Validate() error {
	if c.BaudRate <= 0 || c.BaudRate > 4_000_000 {
		return fmt.Errorf("invalid serial baud rate %d", c.BaudRate)
	}
	if c.DataBits < 5 || c.DataBits > 8 {
		return fmt.Errorf("invalid serial data bits %d: want 5..8", c.DataBits)
	}
	if c.Parity < serial.NoParity || c.Parity > serial.SpaceParity {
		return fmt.Errorf("invalid serial parity %d", c.Parity)
	}
	if c.StopBits < serial.OneStopBit || c.StopBits > serial.TwoStopBits {
		return fmt.Errorf("invalid serial stop bits %d", c.StopBits)
	}
	return nil
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
