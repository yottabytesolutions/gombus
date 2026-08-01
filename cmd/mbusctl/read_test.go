package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
)

// fakeMeter answers one session on a loopback socket: E5h to anything short,
// and the canned frame to a REQ_UD2. It exists to prove the CLI drives the
// exchange in the right order, which no unit test of the parsers can show.
func fakeMeter(t *testing.T, frame []byte) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		buf := make([]byte, 512)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			reply := []byte{0xE5}
			// REQ_UD2 is a short frame whose C field is 5Bh or 7Bh, the FCB
			// being the only difference.
			if req := buf[:n]; len(req) >= 2 && req[0] == 0x10 && req[1]&0x0F == 0x0B {
				reply = frame
			}
			if _, err := conn.Write(reply); err != nil {
				return
			}
		}
	}()

	return ln.Addr().String()
}

func loadFrame(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	frame, err := hex.DecodeString(strings.Join(strings.Fields(string(raw)), ""))
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return frame
}

// TestReadOverLoopback drives the read subcommand end to end against a fake
// meter, in both output modes.
func TestReadOverLoopback(t *testing.T) {
	frame := loadFrame(t, "../../testdata/frames/EDC.hex")

	t.Run("text", func(t *testing.T) {
		var out bytes.Buffer
		args := []string{"read", "-device", fakeMeter(t, frame), "1"}
		if err := run(context.Background(), args, &out); err != nil {
			t.Fatalf("run: %v", err)
		}
		if !strings.HasPrefix(out.String(), "meter ") {
			t.Errorf("text output must start with the meter line, got:\n%s", out.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		var out bytes.Buffer
		args := []string{"read", "-json", "-device", fakeMeter(t, frame), "1"}
		if err := run(context.Background(), args, &out); err != nil {
			t.Fatalf("run: %v", err)
		}
		var got jsonFrame
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not a JSON object: %v\n%s", err, out.String())
		}
		if got.Manufacturer == "" || len(got.Records) == 0 {
			t.Errorf("decoded frame looks empty: %+v", got)
		}
	})
}
