// Command mbusctl talks to wired M-Bus meters over TCP or serial: it scans a
// bus, reads meters by primary or secondary address, and assigns primary
// addresses. Human readable output by default, JSON with -json.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/yottabytesolutions/gombus"
)

// Exit codes. 2 is reserved for bad invocation so a script can tell a typo from
// a bus that did not answer.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// usageError marks an error caused by the command line, not by the bus.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }

func (e *usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error {
	return &usageError{err: fmt.Errorf(format, args...)}
}

func main() {
	os.Exit(mainCode())
}

// mainCode is main with a return value, so the signal handler's cleanup runs
// before the process exits.
func mainCode() int {
	// Ctrl-C cancels the exchange in flight instead of waiting out its timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := run(ctx, os.Args[1:], os.Stdout)
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		return exitUsage
	default:
		writeText(os.Stderr, fmt.Sprintf("mbusctl: %v\n", err))
		var ue *usageError
		if errors.As(err, &ue) {
			writeText(os.Stderr, "run 'mbusctl help' for usage\n")
			return exitUsage
		}
		return exitError
	}
}

// run dispatches the subcommand. Data goes to out, diagnostics to stderr.
func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return usagef("no command given")
	}

	switch args[0] {
	case "scan":
		return runScan(ctx, args[1:], out)
	case "read":
		return runRead(ctx, args[1:], out)
	case "set-address":
		return runSetAddress(ctx, args[1:], out)
	case "help", "-h", "-help", "--help":
		printUsage(out)
		return nil
	default:
		printUsage(os.Stderr)
		return usagef("unknown command %q", args[0])
	}
}

// writeText prints help, usage and diagnostics. It is the one place a write
// error is dropped: these go to stderr, so there is nothing left to report a
// failed write with.
func writeText(w io.Writer, text string) {
	_, _ = io.WriteString(w, text)
}

func printUsage(w io.Writer) {
	writeText(w, `mbusctl reads wired M-Bus meters over TCP or serial.

usage: mbusctl <command> [flags] [arguments]

commands:
  scan         find meters by primary or secondary address
  read         read one meter by primary or secondary address
  set-address  write a new primary address into a meter
  help         show this text

Flags come after the command: mbusctl read -device /dev/ttyUSB0 1
Run 'mbusctl <command> -h' for the flags of one command.
`)
}

// printer writes text output and keeps the first write error. A closed pipe
// then surfaces once, as the command's error, instead of after every line.
type printer struct {
	w   io.Writer
	err error
}

func newPrinter(w io.Writer) *printer { return &printer{w: w} }

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

func (p *printer) Err() error { return p.err }

// busFlags are the flags every subcommand shares.
type busFlags struct {
	device  string
	timeout time.Duration
	json    bool
}

// addBusFlags registers the shared flags. timeout is the budget for the whole
// command, so a scan gets a longer default than a single read.
func addBusFlags(fs *flag.FlagSet, timeout time.Duration) *busFlags {
	bf := &busFlags{}
	fs.StringVar(&bf.device, "device", "", "meter transport: host:port for TCP, /dev/tty* for serial (required)")
	fs.DurationVar(&bf.timeout, "timeout", timeout, "time budget for the whole command")
	fs.BoolVar(&bf.json, "json", false, "print JSON instead of text")
	return bf
}

func (bf *busFlags) validate() error {
	if bf.device == "" {
		return usagef("missing -device")
	}
	if bf.timeout <= 0 {
		return usagef("invalid -timeout %s: must be positive", bf.timeout)
	}
	return nil
}

// withClient dials the device, runs fn with a deadline, and closes the
// transport. One Client serves the whole command: it owns the framing state, so
// a fresh one per exchange would drop bytes that arrived after a stop byte and
// desynchronise the stream.
func (bf *busFlags) withClient(ctx context.Context, fn func(context.Context, *gombus.Client) error) error {
	conn, err := dial(bf.device)
	if err != nil {
		return err
	}
	client := gombus.NewClient(conn)

	ctx, cancel := context.WithTimeout(ctx, bf.timeout)
	defer cancel()

	err = fn(ctx, client)
	if closeErr := client.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("closing transport: %w", closeErr))
	}
	return err
}

// dial picks the transport from the device string: a path is serial, host:port
// is TCP.
func dial(device string) (gombus.Conn, error) {
	if strings.Contains(device, "/") {
		conn, err := gombus.DialSerial(device)
		if err != nil {
			return nil, fmt.Errorf("opening serial device %s: %w", device, err)
		}
		return conn, nil
	}
	if !strings.Contains(device, ":") {
		return nil, usagef("invalid -device %q: want host:port for TCP or a /dev path for serial", device)
	}
	conn, err := gombus.DialTCP(device)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", device, err)
	}
	return conn, nil
}

// parseFlags parses fs and turns a bad command line into a usage error.
//
// The flag package prints the error and the whole usage text itself. That is
// silenced here so a bad flag is reported once, by main. -h still prints the
// subcommand's flags, and is not a failure of the bus.
func parseFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(io.Discard)
	err := fs.Parse(args)
	fs.SetOutput(os.Stderr)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		fs.Usage()
		return err
	default:
		return &usageError{err: err}
	}
}
