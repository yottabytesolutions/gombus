package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/yottabytesolutions/gombus"
)

func runSetAddress(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("set-address", flag.ContinueOnError)
	bf := addBusFlags(fs, readTimeout)
	secondary := fs.Bool("secondary", false, "treat the current address as a secondary id, whatever its length")
	fs.Usage = func() {
		writeText(fs.Output(), "usage: mbusctl set-address [flags] <current> <new>\n\n"+
			"current is a primary address (1..250, or 254 for the only meter on the bus)\n"+
			"or an 8-digit secondary id. new is the primary address to write, 1..250.\n\n")
		fs.PrintDefaults()
	}

	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return usagef("set-address takes a current and a new address")
	}
	tgt, err := parseTarget(fs.Arg(0), *secondary)
	if err != nil {
		return err
	}
	newAddr, err := parseNewPrimary(fs.Arg(1))
	if err != nil {
		return err
	}
	if err := bf.validate(); err != nil {
		return err
	}

	return bf.withClient(ctx, func(ctx context.Context, c *gombus.Client) error {
		if err := setAddress(ctx, c, tgt, newAddr); err != nil {
			return fmt.Errorf("setting %s to primary %d: %w", tgt, newAddr, err)
		}
		if bf.json {
			return writeJSON(out, jsonSetAddress{Primary: int(newAddr)})
		}
		p := newPrinter(out)
		p.printf("%s now answers at primary %d\n", tgt, newAddr)
		return p.Err()
	})
}

func parseNewPrimary(arg string) (uint8, error) {
	value, err := strconv.Atoi(arg)
	if err != nil {
		return 0, usagef("invalid new address %q: %v", arg, err)
	}
	addr, err := assignableAddr(value)
	if err != nil {
		return 0, usagef("invalid new address %q: %v", arg, err)
	}
	return addr, nil
}

// setAddress writes newAddr into the meter tgt names.
//
// A secondary target is selected first and then written through 0xFD. The
// one-frame variant that carries the id in the header exists too, but selecting
// first reuses the search path the scan already relies on and fails loudly when
// the id matches nothing or several meters.
func setAddress(ctx context.Context, c *gombus.Client, tgt target, newAddr uint8) error {
	addr := tgt.primary
	if tgt.secondary {
		if err := c.SelectSecondary(ctx, gombus.SecondaryAddress{ID: tgt.id}); err != nil {
			return err
		}
		addr = addrSecondarySelect
	} else if err := linkReset(ctx, c, addr); err != nil {
		return err
	}

	frame, err := gombus.SetPrimaryUsingPrimary(addr, newAddr)
	if err != nil {
		return fmt.Errorf("building set-address frame: %w", err)
	}
	if err := c.WriteFrame(ctx, frame); err != nil {
		return err
	}
	if _, err := c.ReadSingleCharFrame(ctx); err != nil {
		return fmt.Errorf("no acknowledgement after writing the address: %w", err)
	}
	return nil
}

// linkReset puts the meter in a known state before a write.
func linkReset(ctx context.Context, c *gombus.Client, addr uint8) error {
	ackCtx, cancel := context.WithTimeout(ctx, ackTimeout)
	defer cancel()

	if err := c.WriteFrame(ackCtx, gombus.SndNKE(addr)); err != nil {
		return err
	}
	if _, err := c.ReadSingleCharFrame(ackCtx); err != nil {
		return fmt.Errorf("no answer to link reset: %w", err)
	}
	return nil
}
