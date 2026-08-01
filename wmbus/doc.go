// Package wmbus decodes wireless M-Bus telegrams per EN 13757-4 and the OMS
// specification. It is decode only: it parses what a receiver picked up off
// the air, it does not transmit.
//
// # Scope
//
// The package covers the three layers between the radio and the readings:
// the link layer with its CRCs, the transport layer with its headers and
// security parameters, and the application layer of DIF/VIF data records.
// Records are decoded by the wired gombus package, so a wireless record and a
// wired one are the same value with the same helpers.
//
// Security modes 0 (plaintext), 5 (AES-128-CBC with an address derived
// initialisation vector) and 7 (AES-128-CBC with a key derived from the
// master key) are supported. Mode 13, which needs the extended link layer
// session key exchange, returns ErrUnsupportedMode.
//
// # Input
//
// The input is the raw telegram with the L field as its first byte, which is
// how receiver dongles hand it over. What differs between dongles is the CRC:
//
//	Parse            frame format A or B, auto-detected
//	ParseFormatA     frame format A, per block CRCs
//	ParseFormatB     frame format B, one or two CRC blocks
//	ParseWithoutCRC  receivers that verify and strip the CRCs themselves
//
// Format A and B cannot be told apart from the bytes alone, so Parse tries A
// first and falls back to B. Prefer the explicit function when the receiver
// documents its format.
//
// # Worked example
//
// A water meter transmits an encrypted telegram. The receiver hands over the
// bytes, the caller holds the meter's key, and the reading comes out of the
// data records:
//
//	telegram, err := wmbus.Decode(raw, key)
//	if err != nil { /* ... */ }
//
//	fmt.Println(telegram.Manufacturer, telegram.SerialNumber, telegram.DeviceType)
//	for _, r := range telegram.DataRecords {
//	    fmt.Printf("%s\t%v %s\n", r.Function, r.Value, r.Unit.Unit)
//	}
//
// With more than one meter in range, keep the keys in a KeyRing and let the
// identification number of each telegram pick the key:
//
//	keys := wmbus.KeyRing{12345678: key}
//	telegram, err := wmbus.DecodeWithKeyRing(raw, keys)
//
// Splitting the two steps gives access to the link layer before any key is
// needed, which is what filtering a busy radio channel by meter wants:
//
//	frame, err := wmbus.Parse(raw)
//	if err != nil { /* ... */ }
//	serial, err := frame.SerialNumber()
//	if err != nil { /* ... */ }
//	if serial == wanted {
//	    telegram, err := frame.Decode(key)
//	    /* ... */
//	}
//
// # Errors
//
// Every failure mode has a sentinel, so a receiver loop can tell a corrupted
// telegram apart from one it simply has no key for: ErrInvalidFrame, ErrCRC,
// ErrUnsupportedCI, ErrUnsupportedMode, ErrDecrypt, ErrKeyRequired and
// ErrInvalidKey. A wrong key surfaces as ErrDecrypt, because the decrypted
// payload must start with the two 0x2F filler bytes.
//
// # Specifications
//
// EN 13757-4 for the radio link layer and the CRC, EN 13757-3 for the
// application layer, and the OMS Technical Report volume 2 for the transport
// header, the security modes and the key derivation.
package wmbus
