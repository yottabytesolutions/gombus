package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/yottabytesolutions/gombus"
)

// readTimeout is the default budget for one read. A meter that is there answers
// in well under a second; the rest of the budget covers a slow serial gateway.
const readTimeout = 30 * time.Second

// ackTimeout bounds the link reset acknowledgement. A meter either answers it at
// once or is not there, so waiting the full budget adds nothing.
const ackTimeout = 2 * time.Second

func runRead(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	bf := addBusFlags(fs, readTimeout)
	all := fs.Bool("all", false, "read every frame the meter has, not just the first")
	secondary := fs.Bool("secondary", false, "treat the address as a secondary id, whatever its length")
	fs.Usage = func() {
		writeText(fs.Output(), "usage: mbusctl read [flags] <address>\n\n"+
			"address is a primary address (1..250) or an 8-digit secondary id.\n\n")
		fs.PrintDefaults()
	}

	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usagef("read takes exactly one address")
	}
	tgt, err := parseTarget(fs.Arg(0), *secondary)
	if err != nil {
		return err
	}
	if err := bf.validate(); err != nil {
		return err
	}

	return bf.withClient(ctx, func(ctx context.Context, c *gombus.Client) error {
		frames, err := readFrames(ctx, c, tgt, *all)
		if err != nil {
			return fmt.Errorf("reading %s: %w", tgt, err)
		}
		return printFrames(out, frames, bf.json, *all)
	})
}

// readFrames fetches one frame, or every frame with all set. A secondary target
// is selected first, after which 0xFD reaches it like any primary address.
func readFrames(ctx context.Context, c *gombus.Client, tgt target, all bool) ([]*gombus.DecodedFrame, error) {
	addr := tgt.primary
	if tgt.secondary {
		sec := gombus.SecondaryAddress{ID: tgt.id}
		if !all {
			frame, err := c.ReadBySecondary(ctx, sec)
			if err != nil {
				return nil, err
			}
			return []*gombus.DecodedFrame{frame}, nil
		}
		if err := c.SelectSecondary(ctx, sec); err != nil {
			return nil, err
		}
		addr = addrSecondarySelect
	}

	if all {
		return c.ReadAllFrames(ctx, addr)
	}
	frame, err := readOnce(ctx, c, addr)
	if err != nil {
		return nil, err
	}
	return []*gombus.DecodedFrame{frame}, nil
}

// readOnce link-resets the meter and reads one frame. The reset puts the meter
// in a known state, so the answer is its first frame rather than wherever a
// previous session left the FCB bit.
func readOnce(ctx context.Context, c *gombus.Client, addr uint8) (*gombus.DecodedFrame, error) {
	if err := linkReset(ctx, c, addr); err != nil {
		return nil, err
	}
	return c.ReadSingleFrame(ctx, addr)
}

func printFrames(out io.Writer, frames []*gombus.DecodedFrame, asJSON, all bool) error {
	if asJSON {
		// With -all the shape is an array even for a single frame, so a
		// consumer does not have to branch on the flag it passed.
		if all {
			return writeJSON(out, newJSONFrames(frames))
		}
		return writeJSON(out, newJSONFrame(frames[0]))
	}

	p := newPrinter(out)
	for i, f := range frames {
		if i > 0 {
			p.printf("\n")
		}
		printFrame(p, f)
	}
	return p.Err()
}

func printFrame(p *printer, f *gombus.DecodedFrame) {
	p.printf("meter %s %s version %d %s\n",
		f.SecondaryAddressString(), f.Manufacturer, f.Version, f.DeviceType)
	if f.ProductName != "" {
		p.printf("product %s\n", f.ProductName)
	}
	p.printf("access %d status 0x%02x %s\n", f.AccessNumber, f.Status, f.ReadableStatus())

	for _, r := range f.DataRecords {
		p.printf("  %-28s %s%s\n", r.Function, recordValue(r), recordQualifiers(r))
	}
}

// recordQualifiers names storage, tariff and sub-device only when they are not
// the current-value defaults, so a plain reading stays on one short line.
func recordQualifiers(r gombus.DecodedDataRecord) string {
	if r.StorageNumber == 0 && r.Tariff == 0 && r.Device == 0 {
		return ""
	}
	return fmt.Sprintf(" [storage %d tariff %d device %d]", r.StorageNumber, r.Tariff, r.Device)
}

// recordValue renders one record's value: the error when it did not decode, the
// timestamp for a date field, otherwise the scaled number and its unit.
func recordValue(r gombus.DecodedDataRecord) string {
	switch {
	case r.ValueErr != nil:
		return "undecodable: " + r.ValueErr.Error()
	case !r.Time.IsZero():
		return r.Time.Format(time.RFC3339)
	case r.ValueString != "":
		return r.ValueString
	case r.Unit.Unit == "":
		return formatValue(r.Value)
	default:
		return formatValue(r.Value) + " " + r.Unit.Unit
	}
}

// formatValue rounds for reading. The full precision the meter sent stays in
// the JSON output, which is what a consumer should parse.
func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}
