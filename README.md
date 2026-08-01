# gombus

[![CI](https://github.com/yottabytesolutions/gombus/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yottabytesolutions/gombus/actions/workflows/ci.yml)
[![CodeQL](https://github.com/yottabytesolutions/gombus/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/yottabytesolutions/gombus/security/code-scanning)
[![Go Reference](https://pkg.go.dev/badge/github.com/yottabytesolutions/gombus.svg)](https://pkg.go.dev/github.com/yottabytesolutions/gombus)
[![Release](https://img.shields.io/github/v/release/yottabytesolutions/gombus)](https://github.com/yottabytesolutions/gombus/releases)
[![License](https://img.shields.io/github/license/yottabytesolutions/gombus)](LICENSE)

M-Bus (Meter-Bus) in pure Go. Read wired meters over TCP or RS-485, decode
wireless M-Bus telegrams, and do it from a single static binary.

gombus is a pure-Go alternative to [libmbus](https://github.com/rscada/libmbus).
There is no cgo and no C toolchain in the build, so it cross-compiles to every
target Go supports. A router, an ARM gateway or a Windows box each get the same
binary out of `GOOS=... GOARCH=... go build`.

This is a fork of [jonaz/gombus](https://github.com/jonaz/gombus) by Jonas
Falck, who created the original library. Upstream has been inactive since
2024. This fork continues maintenance. See [Credits](#credits).

## Features

- Wired master over TCP and RS-485 serial, and over anything else that moves
  bytes: the `Conn` interface has `net.Conn`'s shape.
- Variable data responses (CI=0x72) and fixed data responses (CI=0x73),
  decoded into the same record model.
- Bus discovery: primary address sweep, and secondary address wildcard search
  that fixes the identification number one BCD digit at a time.
- Read and address meters by secondary address, for buses where primary
  addresses were never assigned.
- Record query API: pick readings by VIF type, function, storage number,
  tariff and sub-device instead of walking a slice.
- Wireless M-Bus decoding: frame format A and B, security modes 0, 5 and 7.
- `mbusctl`, a CLI for scanning, reading and readdressing, with JSON output.
- Errors instead of silent zeros. A reading that did not decode says so.

## Comparison

|                | gombus | libmbus | jonaz/gombus |
| -------------- | ------ | ------- | ------------ |
| Language       | Go, no cgo | C | Go, no cgo |
| Wired master   | yes | yes | yes |
| Bus scanning   | primary and secondary, in the library | primary and secondary | primary, in the CLI only |
| Fixed data response | yes | yes | no |
| Wireless M-Bus | decode only | no | no |
| CLI output     | JSON, per record | XML | JSON, whole frame |
| Maintained     | yes | low activity | inactive since 2024 |

## Install

```
go get github.com/yottabytesolutions/gombus
```

The CLI:

```
go install github.com/yottabytesolutions/gombus/cmd/mbusctl@latest
```

## Usage

Read one meter over a TCP to M-Bus gateway:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/yottabytesolutions/gombus"
)

func main() {
	conn, err := gombus.DialTCP("192.168.1.10:10001")
	if err != nil {
		log.Fatal(err)
	}

	client := gombus.NewClient(conn)
	defer client.Close()

	frame, err := client.ReadSingleFrame(context.Background(), 1)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s #%d (%s)\n",
		frame.Manufacturer, frame.SerialNumber, frame.DeviceType)
	for _, r := range frame.DataRecords {
		fmt.Printf("  %s\t%v %s\n", r.Function, r.Value, r.Unit.Unit)
	}
}
```

For serial:

```go
conn, err := gombus.DialSerial("/dev/ttyUSB0")
```

Keep one `Client` per transport. It owns the bytes that arrive after a frame's
stop byte, which is what keeps consecutive frames aligned on the stream.

For multi-frame meters use `Client.ReadAllFrames`, which walks the FCB bit
until the slave reports no more records.

### Picking readings

```go
// Current instantaneous power, with real error reporting (no silent zeros).
power, err := frame.Value(gombus.VIFPowerW, gombus.FunctionInstantaneous)

// The meter's own clock.
ts, ok := frame.Timestamp()

// Arbitrary queries: historic values, tariff registers, sub-devices.
lastPeriod, ok := frame.Find(
	gombus.MatchType(gombus.VIFEnergyWh),
	gombus.MatchStorage(1))
tariffs := frame.FindAll(
	gombus.MatchType(gombus.VIFEnergyWh),
	gombus.MatchFunction(gombus.FunctionInstantaneous))
```

A matcher is an ordinary predicate, so a closure covers whatever the provided
matchers do not.

### Finding meters

```go
// Which primary addresses answer.
addresses, err := client.ScanPrimary(ctx, 1, 20)

// Full secondary addresses, found by wildcard search.
slaves, err := client.ScanSecondary(ctx)
for _, sec := range slaves {
	fmt.Println(sec.Manufacturer, sec.Mask(), sec.Version, sec.Medium)
}
```

Silence at an address is ordinary during a scan and is not an error. Only a
cancelled context or a broken transport ends a sweep early.

### Reading by secondary address

```go
frame, err := client.ReadBySecondary(ctx, gombus.SecondaryAddress{
	ID:           12345678,
	Manufacturer: "KAM",
})
```

The identification number and manufacturer are matched exactly. Version and
medium are wildcarded, so a stale version byte cannot silently select nothing.

### Wireless M-Bus

The `wmbus` package decodes what a receiver picked up off the air. It does not
transmit.

```go
import "github.com/yottabytesolutions/gombus/wmbus"

telegram, err := wmbus.Decode(raw, key)
if err != nil {
	log.Fatal(err)
}

fmt.Println(telegram.Manufacturer, telegram.SerialNumber, telegram.DeviceType)
for _, r := range telegram.DataRecords {
	fmt.Printf("%v %s\n", r.Value, r.Unit.Unit)
}
```

Input is the raw telegram with the L field first, which is how receiver dongles
hand it over. Frame format A and B are auto-detected by `Parse`, and
`ParseWithoutCRC` covers receivers that verify and strip the CRCs themselves.

Security modes 0 (plaintext), 5 and 7 are supported. With several meters in
range, put the keys in a `KeyRing` and let each telegram's identification
number pick one. Records are the wired package's own type, so the same
matchers and queries apply.

## mbusctl

```
mbusctl scan  -device /dev/ttyUSB0 -from 1 -to 20
mbusctl scan  -device /dev/ttyUSB0 -secondary -json
mbusctl read  -device 192.168.1.10:10001 1
mbusctl read  -device /dev/ttyUSB0 -secondary 12345678 -json
mbusctl read  -device /dev/ttyUSB0 -all 1
mbusctl set-address -device /dev/ttyUSB0 1 5
```

`-device` takes `host:port` for TCP or a `/dev/tty*` path for serial.
`-timeout` bounds the whole command. `-json` prints records with their type,
function, storage number, tariff, device, value, unit and timestamps, which is
the shape a collector or a scrape job wants.

## Tested with

Decoding runs against a corpus of 21 captured frames from real meters across
the major manufacturers, drawn from the libmbus and Meterlogger fixtures, plus
the libmbus error-frame corpus for malformed-input safety.

- Itron M-Bus Cyble v2.0
- Garo GNM3D-MBUS / Carlo Gavazzi EM340-M1
- Kamstrup MULTICAL 403 / 603 / 803
- 17+ other meters via the libmbus test corpus (Engelmann, Elster, Sensus,
  Saia-Burgess, EMU, Elvaco, ZRM Minol, and others)

## Notes

Manufacturer-specific data after a 0x0F or 0x1F DIF is recognised but not
interpreted. Any CI other than 0x72 and 0x73 returns `ErrUnsupportedCI`.

On serial transports `SetWriteDeadline` is a no-op, because `go.bug.st/serial`
exposes no write timeout. A stuck serial port can still block on `Write`. TCP
transports honour the deadline normally.

## Layout

```
constants.go    EN 13757-3 protocol constants (DIF / VIF / medium / control)
vif.go          Exported VIF type codes and record function names
frame.go        ShortFrame / LongFrame types and bit accessors
commands.go     Frame builders (REQ_UD2, SND_NKE, set primary, and others)
encoding.go     BCD, int24/48/64, float32, ASCII, checksum helpers
datetime.go     Type F / G / I date and date-time fields
field.go        DIB/VIB decoders (function, medium, unit, storage, tariff, device)
decode.go       Variable data response decoder (CI=0x72)
decode_fixed.go Fixed data response decoder (CI=0x73)
records.go      Record query API (Find, FindAll, Value, Timestamp, matchers)
unit.go         VIF unit lookup tables
read.go         Frame reading (timeout, checksum verify, error sentinels)
gombus.go       Client and the high-level read flows
scan.go         Primary sweep, secondary wildcard search, selection
conn.go         Conn interface
tcp.go          TCP transport
serial.go       go.bug.st/serial transport
wmbus/          Wireless M-Bus decoding (link layer, transport layer, crypto)
cmd/mbusctl/    CLI
testdata/       Real-meter frame fixtures (libmbus corpus + local capture)
```

## Spec

- [EN 13757-3 §6 Application Layer](https://m-bus.com/documentation-wired/06-application-layer)
- EN 13757-4 for the wireless link layer, and the OMS Technical Report volume 2
  for the wireless transport header, security modes and key derivation
- `docs/Kamstrup_M-Bus_and_wM-Bus_Protocol.pdf` (vendor logger profiles,
  included in the repo)

## Credits

The original gombus was written by [Jonas Falck](https://github.com/jonaz)
and published at [jonaz/gombus](https://github.com/jonaz/gombus) under the
Apache License 2.0. The protocol decoding core, frame handling, and VIF unit
tables originate from that work.

This fork is not endorsed by or affiliated with the original author.

### Changes in this fork

- Module path changed to `github.com/yottabytesolutions/gombus`.
- Reworked frame handling and read paths.
- `Conn` interface extracted so transports are pluggable; TCP and serial
  moved behind it.
- Read and write deadline support; error sentinels for malformed frames.
- Bus scanning, secondary addressing and fixed data responses added.
- Wireless M-Bus decoding added in the `wmbus` package.
- Test corpus extended with the libmbus and Meterlogger frame fixtures.
- Dependencies updated.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
