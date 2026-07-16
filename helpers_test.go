package gombus

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockConn is a minimal in-memory Conn used by the read.go and gombus.go
// tests. readData is replayed byte-by-byte; once exhausted the connection
// reports io.EOF (matching real socket semantics for graceful close), unless
// timeout is set in which case Read returns a timeout-shaped error.
type mockConn struct {
	readData         []byte
	readIndex        int
	readErr          error
	timeout          bool
	writeDeadlineSet bool
	readDeadlineSet  bool
}

func (m *mockConn) Read(b []byte) (int, error) {
	if m.timeout {
		return 0, errors.New("timeout")
	}
	if m.readErr != nil {
		return 0, m.readErr
	}
	if m.readIndex >= len(m.readData) {
		if len(m.readData) > 0 {
			return 0, io.EOF
		}
		return 0, errors.New("timeout")
	}
	n := copy(b, m.readData[m.readIndex:])
	m.readIndex += n
	return n, nil
}

func (m *mockConn) Write(b []byte) (int, error) {
	return len(b), nil
}

func (m *mockConn) SetReadDeadline(_ time.Time) error  { m.readDeadlineSet = true; return nil }
func (m *mockConn) SetWriteDeadline(_ time.Time) error { m.writeDeadlineSet = true; return nil }
func (m *mockConn) Close() error                       { return nil }

// hexToBytes is a tolerant hex decoder used by tests that paste captured
// frames as multi-line strings (whitespace and tabs ignored).
func hexToBytes(s string) []byte {
	cleaned := strings.Map(
		func(r rune) rune {
			switch r {
			case ' ', '\n', '\r', '\t':
				return -1
			}
			return r
		}, s,
	)
	out := make([]byte, len(cleaned)/2)
	for i := 0; i < len(out); i++ {
		hi, lo := hexNib(cleaned[2*i]), hexNib(cleaned[2*i+1])
		out[i] = hi<<4 | lo
	}
	return out
}

func hexNib(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

// loadHexFixture reads a whitespace-tolerant hex file from disk and returns
// the raw bytes. Fails the test on any I/O error.
func loadHexFixture(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading fixture %s", path)
	return hexToBytes(string(raw))
}

// forEachHexFixture walks dir and invokes fn for every *.hex file as a
// subtest. fn runs inside a panic-recovery defer so a parser regression
// surfaces as a test failure instead of crashing the test binary.
func forEachHexFixture(t *testing.T, dir string, fn func(t *testing.T, name string, data []byte)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".hex") {
			continue
		}
		name := e.Name()
		t.Run(
			name, func(t *testing.T) {
				data := loadHexFixture(t, filepath.Join(dir, name))
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic decoding %s: %v", name, r)
					}
				}()
				fn(t, name, data)
			},
		)
	}
}
