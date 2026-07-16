package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/yottabytesolutions/gombus"
)

var (
	primaryID = flag.Int("id", 1, "primaryID to fetch data from")
	device    = flag.String("device", "192.168.13.42:10001", "tcp address of the mbus gateway")
)

func main() {
	flag.Parse()

	// Ctrl-C cancels the exchange in flight rather than waiting out its timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	conn, err := gombus.DialTCP(*device)
	if err != nil {
		slog.Error("dial failed", "err", err)
		return
	}

	// One Client for the whole session. It owns the framing state, so reusing it
	// is what keeps consecutive frames aligned on the stream.
	client := gombus.NewClient(conn)
	defer func() {
		if err := client.Close(); err != nil {
			slog.Error("close failed", "err", err)
		}
	}()

	frames, err := client.ReadAllFrames(ctx, uint8(*primaryID))
	if err != nil {
		slog.Error("read all frames failed", "err", err)
		return
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	for _, frame := range frames {
		if err := enc.Encode(frame); err != nil {
			slog.Error("encode failed", "err", err)
			return
		}
		slog.Info("read values", "count", len(frame.DataRecords))
	}
	fmt.Printf("read %d frame(s)\n", len(frames))
}
