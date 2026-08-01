package wmbus

import "errors"

// ErrInvalidFrame reports a structurally broken telegram: too short for the
// fields it claims to carry, or a length field that cannot be satisfied.
var ErrInvalidFrame = errors.New("invalid wM-Bus frame")

// ErrCRC reports that a link layer block failed its CRC check.
var ErrCRC = errors.New("wM-Bus block CRC mismatch")

// ErrUnsupportedCI reports a CI field this package does not interpret.
var ErrUnsupportedCI = errors.New("unsupported wM-Bus CI field")

// ErrUnsupportedMode reports a security mode outside the supported set
// (0 none, 5 and 7 AES-128-CBC). Mode 13 telegrams, signalled by the
// extended link layer CI 0x8D, land here as well.
var ErrUnsupportedMode = errors.New("unsupported wM-Bus security mode")

// ErrDecrypt reports that decryption produced something that is not a valid
// application payload. In practice that means the wrong key: the plaintext
// must start with the two 0x2F filler bytes and a wrong key makes that check
// fail with overwhelming probability.
var ErrDecrypt = errors.New("wM-Bus decryption failed")

// ErrKeyRequired reports that the telegram is encrypted but no key was given.
var ErrKeyRequired = errors.New("wM-Bus telegram is encrypted but no key was supplied")

// ErrInvalidKey reports a key that is not the 16 bytes AES-128 needs.
var ErrInvalidKey = errors.New("wM-Bus key must be 16 bytes")
