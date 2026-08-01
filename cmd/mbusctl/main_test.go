package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

// TestRunUsageErrors covers every way to get the command line wrong. Each must
// come back as a usage error, which is what makes the process exit 2 instead of
// looking like a bus that did not answer. None of these reach the transport.
func TestRunUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "no command", args: nil},
		{name: "unknown command", args: []string{"listen"}},
		{name: "unknown flag", args: []string{"read", "-nope", "1"}},
		{name: "read without device", args: []string{"read", "1"}},
		{name: "read without address", args: []string{"read", "-device", "127.0.0.1:1"}},
		{name: "read with two addresses", args: []string{"read", "-device", "127.0.0.1:1", "1", "2"}},
		{name: "read bad address", args: []string{"read", "-device", "127.0.0.1:1", "300"}},
		{name: "scan with an argument", args: []string{"scan", "-device", "127.0.0.1:1", "1"}},
		{name: "scan both modes", args: []string{"scan", "-device", "127.0.0.1:1", "-primary", "-secondary"}},
		{name: "scan reversed range", args: []string{"scan", "-device", "127.0.0.1:1", "-from", "10", "-to", "5"}},
		{name: "scan range outside the bus", args: []string{"scan", "-device", "127.0.0.1:1", "-to", "251"}},
		{name: "scan without device", args: []string{"scan"}},
		{name: "negative timeout", args: []string{"read", "-device", "127.0.0.1:1", "-timeout", "0s", "1"}},
		{name: "set-address without a new address", args: []string{"set-address", "-device", "127.0.0.1:1", "1"}},
		{name: "set-address to a reserved address", args: []string{"set-address", "-device", "127.0.0.1:1", "1", "254"}},
		{name: "set-address to zero", args: []string{"set-address", "-device", "127.0.0.1:1", "1", "0"}},
		{name: "device is neither host:port nor a path", args: []string{"read", "-device", "eth0", "1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args, io.Discard)
			if err == nil {
				t.Fatalf("run(%q): expected an error", tc.args)
			}
			var ue *usageError
			if !errors.As(err, &ue) {
				t.Fatalf("run(%q): want a usage error, got %T: %v", tc.args, err, err)
			}
		})
	}
}

// TestRunHelp pins that help succeeds and names every subcommand, since it is
// the only discovery path an operator has.
func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var buf bytes.Buffer
		if err := run(context.Background(), []string{arg}, &buf); err != nil {
			t.Fatalf("run(%q): %v", arg, err)
		}
		for _, want := range []string{"scan", "read", "set-address"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("help must name %q, got:\n%s", want, buf.String())
			}
		}
	}
}

// TestRunSubcommandHelp pins that -h on a subcommand returns flag.ErrHelp,
// which main turns into exit 2 without an error line of its own.
func TestRunSubcommandHelp(t *testing.T) {
	for _, cmd := range []string{"scan", "read", "set-address"} {
		err := run(context.Background(), []string{cmd, "-h"}, io.Discard)
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("run(%q -h): want flag.ErrHelp, got %v", cmd, err)
		}
	}
}

// TestDialRejectsUnusableDevice checks the transport split before anything is
// dialled: a path is serial, host:port is TCP, and anything else is a typo
// rather than a device.
func TestDialRejectsUnusableDevice(t *testing.T) {
	_, err := dial("ttyUSB0")
	if err == nil {
		t.Fatal("expected an error for a device that is neither a path nor host:port")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("want a usage error, got %T: %v", err, err)
	}
}
