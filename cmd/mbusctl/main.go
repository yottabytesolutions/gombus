package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/yottabytesolutions/gombus"
)

var device = flag.String("device", "192.168.13.42:10001", "address. ether tcp ex 192.168.1.10:10000 or /dev/tty*")

var mode = flag.String("mode", "scan", "valid modes are: scan,set-primary,read-single,read-multi")

// set-primary
var (
	newPrimary     = flag.Int("new-primary", -1, "new primary address")
	currentPrimary = flag.Int("current-primary", -1, "current primary address")
)

// scan
var (
	addr     = flag.Int("addr", 1, "primary address start number. use 254 if only one meter connected to bus")
	addrStop = flag.Int("addr-stop", 250, "primary address stop number")
	logLevel = flag.String("loglevel", "info", "available levels are: "+strings.Join(getLevels(), ","))
)

// A meter can hold 1..250 as its own address. 0 marks an unconfigured slave and
// 251..255 are reserved.
const (
	minPrimaryAddr = 1
	maxPrimaryAddr = 250
	// addrSecondarySelect (0xFD) is where a meter answers after a secondary
	// selection. addrBroadcastReply (0xFE) is broadcast-with-reply, the
	// documented way to reach the only meter on a bus when its address is
	// unknown. Both are valid places to SEND a frame.
	addrSecondarySelect = 253
	addrBroadcastReply  = 254
)

// parseDestinationAddr range-checks an address a frame will be SENT TO, so it
// also accepts 0xFD and 0xFE. Range-checking happens before the narrowing
// conversion: converting first wraps silently, turning 300 into meter 44.
func parseDestinationAddr(flagName string, value int) (uint8, error) {
	valid := (value >= minPrimaryAddr && value <= maxPrimaryAddr) ||
		value == addrSecondarySelect || value == addrBroadcastReply
	if !valid {
		return 0, fmt.Errorf(
			"invalid -%s %d: address to read from must be %d..%d, %d (secondary select) or %d (broadcast with reply)",
			flagName, value, minPrimaryAddr, maxPrimaryAddr, addrSecondarySelect, addrBroadcastReply,
		)
	}
	return uint8(value), nil
}

// parseAssignableAddr range-checks an address that will be WRITTEN INTO a meter
// as its own. Stricter than parseDestinationAddr on purpose: 0xFE is a fine
// destination but writing it into a meter makes that meter answer every
// broadcast, which cannot be undone over the bus.
func parseAssignableAddr(flagName string, value int) (uint8, error) {
	if value < minPrimaryAddr || value > maxPrimaryAddr {
		return 0, fmt.Errorf(
			"invalid -%s %d: address written into a meter must be %d..%d",
			flagName, value, minPrimaryAddr, maxPrimaryAddr,
		)
	}
	return uint8(value), nil
}

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

func getLevels() []string {
	lvls := make([]string, 0, len(logLevels))
	for k := range logLevels {
		lvls = append(lvls, k)
	}
	return lvls
}

func main() {
	flag.Parse()
	lvl, ok := logLevels[strings.ToLower(*logLevel)]
	if !ok {
		log.Fatalf("invalid log level: %s", *logLevel)
	}
	if lvl != slog.LevelInfo {
		_, _ = fmt.Fprintf(os.Stderr, "using loglevel: %s\n", lvl.String())
	}
	slog.SetLogLoggerLevel(lvl)

	primaryAddr, err := parseDestinationAddr("addr", *addr)
	if err != nil {
		log.Fatal(err)
	}
	primaryAddrStop, err := parseDestinationAddr("addr-stop", *addrStop)
	if err != nil {
		log.Fatal(err)
	}

	// Validated before dial so bad input never reaches the bus. Only
	// set-primary reads these, and their -1 defaults are not valid addresses.
	var currentAddr, newAddr uint8
	if *mode == "set-primary" {
		if currentAddr, err = parseDestinationAddr("current-primary", *currentPrimary); err != nil {
			log.Fatal(err)
		}
		if newAddr, err = parseAssignableAddr("new-primary", *newPrimary); err != nil {
			log.Fatal(err)
		}
	}

	log.Printf("connecting to: %s\n", *device)
	conn, err := dial(*device)
	if err != nil {
		log.Println(fmt.Errorf("error connecting to mbus: %w", err))
		return
	}
	// One Client for the whole session: it owns the framing state, so building
	// a fresh one per read would drop bytes that arrived after a frame's stop
	// byte and desynchronise the stream.
	client := gombus.NewClient(conn)
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("error closing connection: %v", err)
		}
	}()

	// Ctrl-C cancels whatever exchange is in flight instead of waiting out its
	// timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch *mode {
	case "scan":
		// i is uint8, so this loop terminates only because
		// parseDestinationAddr rejects 255: i++ on 254 exits, i++ on 255 wraps
		// to 0 and runs forever. Widening the accepted range means fixing this.
		for i := primaryAddr; i <= primaryAddrStop; i++ {
			log.Println("checking address:", i)
			frame, err := readPrimaryAddress(ctx, client, i)
			if err != nil {
				log.Println("error checking address:", i, err)
				continue
			}
			log.Println(
				"Found device:",
				frame.SerialNumber,
				frame.Manufacturer,
				frame.Version,
				frame.DeviceType,
				frame.SecondaryAddressString(),
			)
		}
	case "set-primary":
		log.Printf("change primary address from %d to %d\n", currentAddr, newAddr)
		if err := setPrimary(ctx, client, currentAddr, newAddr); err != nil {
			log.Println(err)
			return
		}
	case "read-single":
		frame, err := readPrimaryAddress(ctx, client, primaryAddr)
		if err != nil {
			log.Println(err)
			return
		}
		b, err := json.MarshalIndent(frame, "", "\t")
		if err != nil {
			log.Println(err)
			return
		}
		fmt.Println(string(b))
	case "read-multi":
		frames, err := client.ReadAllFrames(ctx, primaryAddr)
		if err != nil {
			log.Println(err)
			return
		}
		b, err := json.MarshalIndent(frames, "", "\t")
		if err != nil {
			log.Println(err)
			return
		}
		fmt.Println(string(b))
	default:
		log.Printf("unknown mode: %s\n", *mode)
	}
}

func dial(device string) (gombus.Conn, error) {
	_, _, err := net.SplitHostPort(device)
	if err != nil {
		log.Printf("device %s does not contain a port, assuming its a serial device\n", device)
		return gombus.DialSerial(device)
	}

	conn, err := gombus.DialTCP(device)
	if err != nil {
		return nil, fmt.Errorf("error connecting to mbus: %w", err)
	}

	return conn, nil
}

func readPrimaryAddress(ctx context.Context, client *gombus.Client, primaryAddr uint8) (*gombus.DecodedFrame, error) {
	// A scan asks addresses that mostly hold no meter, so bound the ack to 1s
	// instead of waiting out the full frame timeout on every empty address.
	ackCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	slog.Debug("writing NKE")
	if err := client.WriteFrame(ackCtx, gombus.SndNKE(primaryAddr)); err != nil {
		return nil, err
	}
	slog.Debug("writing NKE done")

	slog.Debug("ReadSingleCharFrame")
	if _, err := client.ReadSingleCharFrame(ackCtx); err != nil {
		return nil, err
	}
	slog.Debug("ReadSingleCharFrame done")

	slog.Debug("client.ReadSingleFrame")
	frame, err := client.ReadSingleFrame(ctx, primaryAddr)
	if err != nil {
		return nil, err
	}
	slog.Debug("client.ReadSingleFrame done")

	return frame, nil
}

func setPrimary(ctx context.Context, client *gombus.Client, primaryAddr, newPrimary uint8) error {
	// Writing an address is not a scan: the meter is known to be there, so allow
	// it longer to answer than readPrimaryAddress does.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	slog.Debug("writing NKE")
	if err := client.WriteFrame(ctx, gombus.SndNKE(primaryAddr)); err != nil {
		return err
	}
	slog.Debug("writing NKE done")

	slog.Debug("ReadSingleCharFrame")
	if _, err := client.ReadSingleCharFrame(ctx); err != nil {
		return err
	}
	slog.Debug("ReadSingleCharFrame done")

	frame, err := gombus.SetPrimaryUsingPrimary(primaryAddr, newPrimary)
	if err != nil {
		return fmt.Errorf("building set-primary frame: %w", err)
	}
	if err := client.WriteFrame(ctx, frame); err != nil {
		return err
	}
	if _, err := client.ReadSingleCharFrame(ctx); err != nil {
		return fmt.Errorf("timeout waiting for answer after setting address: %w", err)
	}

	return nil
}
