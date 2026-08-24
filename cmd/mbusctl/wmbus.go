package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yottabytesolutions/gombus/wmbus"
)

const maxTelegramInput = 4096

// runWMBusDecode decodes one receiver telegram from a hex file or stdin. Keys
// live only in a file: accepting them on argv leaks them to process listings.
func runWMBusDecode(_ context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("wmbus-decode", flag.ContinueOnError)
	input := fs.String("input", "", "hex telegram file (default: stdin)")
	keyFile := fs.String("key-file", "", "JSON object mapping 8-digit serial numbers to 32-hex-digit AES keys")
	format := fs.String("format", "auto", "input format: auto, a, b, or stripped")
	asJSON := fs.Bool("json", false, "print JSON instead of text")
	fs.Usage = func() {
		writeText(fs.Output(), "usage: mbusctl wmbus-decode [flags]\n\nDecodes one whitespace-separated hexadecimal wireless M-Bus telegram.\n\n")
		fs.PrintDefaults()
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usagef("wmbus-decode takes no arguments")
	}
	raw, err := readHexTelegram(*input)
	if err != nil {
		return err
	}
	keys, err := readKeyRing(*keyFile)
	if err != nil {
		return err
	}
	frame, err := parseWMBus(*format, raw)
	if err != nil {
		return fmt.Errorf("decoding wireless telegram: %w", err)
	}
	t, err := frame.DecodeWithKeyRing(keys)
	if err != nil {
		return fmt.Errorf("decoding wireless telegram: %w", err)
	}
	if *asJSON {
		return writeJSON(out, newJSONWMBus(t))
	}
	p := newPrinter(out)
	p.printf("meter %s %08d %s format %s control 0x%02x\n", t.Manufacturer, t.SerialNumber, t.DeviceType, t.Format, t.LinkControl)
	for _, r := range t.DataRecords {
		p.printf("  %-28s %s%s\n", r.Function, recordValue(r), recordQualifiers(r))
	}
	return p.Err()
}

func readHexTelegram(path string) ([]byte, error) {
	var r io.Reader = os.Stdin
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening telegram input: %w", err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxTelegramInput*3+1))
	if err != nil {
		return nil, fmt.Errorf("reading telegram input: %w", err)
	}
	s := strings.Join(strings.Fields(string(b)), "")
	if len(s) == 0 || len(s) > maxTelegramInput*2 || len(s)%2 != 0 {
		return nil, usagef("invalid telegram hex length")
	}
	raw := make([]byte, len(s)/2)
	if _, err := hex.Decode(raw, []byte(s)); err != nil {
		return nil, usagef("invalid telegram hex: %v", err)
	}
	return raw, nil
}

func readKeyRing(path string) (wmbus.KeyRing, error) {
	keys := wmbus.KeyRing{}
	if path == "" {
		return keys, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	if len(b) > 1<<20 {
		return nil, usagef("key file exceeds 1 MiB")
	}
	var values map[string]string
	if err := json.Unmarshal(b, &values); err != nil {
		return nil, usagef("invalid key file JSON: %v", err)
	}
	for serial, value := range values {
		if len(serial) != 8 || !isDigits(serial) {
			return nil, usagef("invalid key serial %q", serial)
		}
		key, err := hex.DecodeString(value)
		if err != nil || len(key) != 16 {
			return nil, usagef("key for %s must be exactly 32 hexadecimal characters", serial)
		}
		var id uint64
		for _, ch := range serial {
			id = id*10 + uint64(ch-'0')
		}
		keys[id] = key
	}
	return keys, nil
}

func parseWMBus(format string, raw []byte) (*wmbus.Frame, error) {
	switch strings.ToLower(format) {
	case "auto":
		return wmbus.Parse(raw)
	case "a":
		return wmbus.ParseFormatA(raw)
	case "b":
		return wmbus.ParseFormatB(raw)
	case "stripped":
		return wmbus.ParseWithoutCRC(raw)
	default:
		return nil, usagef("invalid -format %q: want auto, a, b or stripped", format)
	}
}

type jsonWMBus struct {
	Format         string       `json:"format"`
	LinkControl    int          `json:"link_control"`
	SerialNumber   int          `json:"serial_number"`
	Manufacturer   string       `json:"manufacturer"`
	Version        int          `json:"version"`
	DeviceType     string       `json:"device_type"`
	CI             int          `json:"ci"`
	AccessNumber   int          `json:"access_number"`
	Status         int          `json:"status"`
	EncryptionMode int          `json:"encryption_mode"`
	Records        []jsonRecord `json:"records"`
}

func newJSONWMBus(t *wmbus.Telegram) jsonWMBus {
	records := make([]jsonRecord, 0, len(t.DataRecords))
	for _, r := range t.DataRecords {
		records = append(records, newJSONRecord(r))
	}
	return jsonWMBus{Format: t.Format.String(), LinkControl: int(t.LinkControl), SerialNumber: t.SerialNumber, Manufacturer: t.Manufacturer, Version: int(t.Version), DeviceType: t.DeviceType, CI: int(t.CI), AccessNumber: int(t.AccessNumber), Status: int(t.Status), EncryptionMode: t.EncryptionMode, Records: records}
}
