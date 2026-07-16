package gombus

import (
	"context"
	"fmt"
	"net"
	"time"
)

type conn struct {
	conn net.Conn
}

func DialTCP(addr string) (Conn, error) {
	var dialer net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	c, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}
	return &conn{conn: c}, nil
}

func (c *conn) Read(b []byte) (n int, err error)  { return c.conn.Read(b) }
func (c *conn) Write(b []byte) (n int, err error) { return c.conn.Write(b) }
func (c *conn) Close() error                      { return c.conn.Close() }
func (c *conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
