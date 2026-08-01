package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/yottabytesolutions/gombus"
)

// scanTimeout is the default budget for a scan. A primary sweep spends one
// answer window per address, so the whole range takes minutes on a bus with few
// meters.
const scanTimeout = 10 * time.Minute

func runScan(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	bf := addBusFlags(fs, scanTimeout)
	primary := fs.Bool("primary", false, "scan primary addresses (default)")
	secondary := fs.Bool("secondary", false, "scan secondary addresses by wildcard search")
	from := fs.Int("from", minPrimaryAddr, "first primary address to probe")
	to := fs.Int("to", maxPrimaryAddr, "last primary address to probe")
	fs.Usage = func() {
		writeText(fs.Output(), "usage: mbusctl scan [-primary|-secondary] [flags]\n\n")
		fs.PrintDefaults()
	}

	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("scan takes no arguments, got %q", fs.Arg(0))
	}
	if *primary && *secondary {
		return usagef("choose either -primary or -secondary, not both")
	}
	if err := bf.validate(); err != nil {
		return err
	}

	if *secondary {
		return bf.withClient(ctx, func(ctx context.Context, c *gombus.Client) error {
			return scanSecondary(ctx, c, out, bf.json)
		})
	}

	start, err := parseAssignableAddr("from", *from)
	if err != nil {
		return &usageError{err: err}
	}
	stop, err := parseAssignableAddr("to", *to)
	if err != nil {
		return &usageError{err: err}
	}
	if start > stop {
		return usagef("invalid range: -from %d is above -to %d", start, stop)
	}
	return bf.withClient(ctx, func(ctx context.Context, c *gombus.Client) error {
		return scanPrimary(ctx, c, out, bf.json, start, stop)
	})
}

func scanPrimary(ctx context.Context, c *gombus.Client, out io.Writer, asJSON bool, from, to uint8) error {
	found, err := c.ScanPrimary(ctx, from, to)
	if err != nil {
		return fmt.Errorf("primary scan %d..%d: %w", from, to, err)
	}

	addrs := make([]int, 0, len(found))
	for _, a := range found {
		addrs = append(addrs, int(a))
	}
	if asJSON {
		return writeJSON(out, jsonPrimaryScan{Primary: addrs})
	}

	p := newPrinter(out)
	if len(addrs) == 0 {
		p.printf("no meter answered on %d..%d\n", from, to)
		return p.Err()
	}
	for _, a := range addrs {
		p.printf("primary %d\n", a)
	}
	return p.Err()
}

func scanSecondary(ctx context.Context, c *gombus.Client, out io.Writer, asJSON bool) error {
	found, err := c.ScanSecondary(ctx)
	if err != nil {
		return fmt.Errorf("secondary scan: %w", err)
	}

	if asJSON {
		list := make([]jsonSecondary, 0, len(found))
		for _, sec := range found {
			list = append(list, newJSONSecondary(sec))
		}
		return writeJSON(out, jsonSecondaryScan{Secondary: list})
	}

	p := newPrinter(out)
	if len(found) == 0 {
		p.printf("no meter answered the secondary search\n")
		return p.Err()
	}
	for _, sec := range found {
		p.printf("%s manufacturer %s version %d medium 0x%02x\n",
			sec.Mask(), sec.Manufacturer, sec.Version, sec.Medium)
	}
	return p.Err()
}
