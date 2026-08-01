# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0] - 2026-08-01

### Added

- Bus scanning. `Client.ScanPrimary` sweeps a range of primary addresses.
  `Client.ScanSecondary` finds meters by secondary address with a wildcard
  search that fixes the identification number one BCD digit at a time.
- Secondary addressing. `Client.SelectSecondary` points address 0xFD at one
  meter and `Client.ReadBySecondary` reads it, for buses where primary
  addresses were never assigned. `SecondaryAddress` carries the identification
  number, manufacturer, version and medium.
- Fixed data responses (CI=0x73). They decode into the same `DecodedFrame`
  with one record per counter, so every matcher and query works on them
  unchanged.
- `wmbus` package: decode-only wireless M-Bus per EN 13757-4 and OMS. Frame
  format A and B with CRC verification, transport headers with CI 0x72, 0x7A
  and 0x78, and security modes 0, 5 and 7. Application records are decoded by
  the wired package, so wired and wireless records are the same type.
- `mbusctl` CLI with `scan`, `read` and `set-address` subcommands over TCP and
  serial, and a `-json` output mode carrying record type, function, storage
  number, tariff, device, value, unit and timestamps.
- Runnable godoc examples for decoding, the record query API, scanning,
  secondary reads and wireless decoding.
- `CHANGELOG.md`, and a README covering the new surface.

### Fixed

- VIF extension types no longer collide with primary VIF types in `Unit.Type`.
  An error-flags record (VIF 0xFD, VIFE 0x17) used to match
  `MatchType(VIFVolume)`. Extension types now carry a 0x100/0x200 offset,
  named by the new `VIFExt` constants.

## [1.0.0] - 2026-07-31

First stable release of the fork.

### Added

- Exported VIF type codes (`VIFEnergyWh`, `VIFVolume`, `VIFPowerW` and the
  rest of the primary VIF table) and record function names
  (`FunctionInstantaneous`, `FunctionMaximum` and friends), so records can be
  selected by constant rather than by string.
- Record query API on `DecodedFrame`: `Find`, `FindAll`, `Record`, `Records`,
  `Value` and `Timestamp`, with the `MatchType`, `MatchFunction`,
  `MatchStorage`, `MatchTariff`, `MatchDevice`, `MatchCurrent` and
  `MatchTimestamp` matchers.
- `Conn` interface so transports are pluggable, with TCP and serial behind it.
- Read and write deadlines, and error sentinels for malformed frames.
- Test corpus of captured frames from the libmbus and Meterlogger fixtures,
  plus the libmbus error-frame corpus.

### Fixed

- Silent value corruption. A record whose value could not be decoded now
  reports it in `ValueErr` instead of reading as zero, and `Value` returns an
  error rather than a plausible number.

### Changed

- Module path is `github.com/yottabytesolutions/gombus`.
- Frame handling and read paths reworked.
- Dependencies updated.

[Unreleased]: https://github.com/yottabytesolutions/gombus/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/yottabytesolutions/gombus/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/yottabytesolutions/gombus/releases/tag/v1.0.0
