# gombus

[![CI](https://github.com/yottabytesolutions/gombus/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yottabytesolutions/gombus/actions/workflows/ci.yml)

A pure-Go implementation of the wired M-Bus (Meter-Bus) protocol per
EN 13757-3, with helpers for both TCP and RS-485 serial transports.

This is a fork of [jonaz/gombus](https://github.com/jonaz/gombus) by Jonas
Falck, who created the original library. Upstream has been inactive since
2024. This fork continues maintenance. See [Credits](#credits).

## Status

Master-side only. Variable-data responses (CI=0x72) are decoded into
structured records; manufacturer-specific data after a 0x0F/0x1F DIF is
recognised but not interpreted.

Test corpus covers 21 captured frames from real meters across all major
manufacturers (libmbus + Meterlogger fixtures), plus the libmbus error-frame
corpus for malformed-input safety.

## Install

```
go get github.com/yottabytesolutions/gombus
```

## Usage

```go
package main

import (
	"fmt"
	"log"

	"github.com/yottabytesolutions/gombus"
)

func main() {
	conn, err := gombus.DialTCP("192.168.1.10:10001")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	frame, err := gombus.ReadSingleFrame(conn, 1)
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

To pick specific readings instead of scanning `DataRecords` by hand:

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

For multi-frame meters use `ReadAllFrames`, which walks the FCB bit until
the slave reports no more records.

For serial:

```go
conn, err := gombus.DialSerial("/dev/ttyUSB0")
```

Any type satisfying the `Conn` interface (`Read`, `Write`, `SetReadDeadline`,
`SetWriteDeadline`, `Close`) can be passed instead — useful for testing or
when wrapping a custom transport.

Note: on serial transports `SetWriteDeadline` is a no-op because
`go.bug.st/serial` does not expose a write timeout. A stuck serial port can
still block indefinitely on `Write`. TCP transports honour the deadline
normally.

## Tested with

- Itron M-Bus Cyble v2.0
- Garo GNM3D-MBUS / Carlo Gavazzi EM340-M1
- Kamstrup MULTICAL 403 / 603 / 803
- 17+ other meters via the libmbus test corpus (Engelmann, Elster, Sensus,
  Saia-Burgess, EMU, Elvaco, ZRM Minol, …)

## Layout

```
constants.go    EN 13757-3 protocol constants (DIF / VIF / medium / control)
frame.go        ShortFrame / LongFrame types and bit accessors
commands.go     Frame builders (REQ_UD2, SND_NKE, set primary, …)
encoding.go     BCD, int24/48/64, float32, ASCII, checksum helpers
field.go        DIB/VIB decoders (function, medium, unit, storage, tariff, device)
decode.go       Long-frame decoder (Decode, decodeData, decodeLVAR, ProductName)
unit.go         VIF unit lookup tables
read.go         Frame reading (timeout, checksum verify, error sentinels)
gombus.go       High-level read flows (ReadAllFrames, ReadSingleFrame)
conn.go         Conn interface
tcp.go          TCP transport
serial.go       go.bug.st/serial transport
testdata/       Real-meter frame fixtures (libmbus corpus + local capture)
```

## Spec

- [EN 13757-3 §6 Application Layer](https://m-bus.com/documentation-wired/06-application-layer)
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
- Test corpus extended with the libmbus and Meterlogger frame fixtures.
- Dependencies updated.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
