package wmbus_test

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"

	"github.com/yottabytesolutions/gombus/wmbus"
)

// waterMeterTelegram is a frame format A telegram from a Kamstrup water meter,
// encrypted with security mode 5. It is the raw radio frame with the L field
// first, which is how a receiver dongle hands it over.
const waterMeterTelegram = "1e442d2c123456781b0780d4" +
	"7a2a00100515fdc2e7f9093e898010b6b52433e80f6fca5647"

// meterKey is the meter's 16 byte AES key, as read off the meter or its
// delivery note.
const meterKey = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"

func mustHex(s string) []byte {
	raw, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return raw
}

// Decode does the whole job: link layer CRCs, transport header, decryption and
// the application records. Records are the wired package's own type, so every
// gombus matcher and query works on them.
func ExampleDecode() {
	telegram, err := wmbus.Decode(mustHex(waterMeterTelegram), mustHex(meterKey))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s %d (%s), security mode %d\n",
		telegram.Manufacturer, telegram.SerialNumber,
		telegram.DeviceType, telegram.EncryptionMode)

	for _, r := range telegram.DataRecords {
		fmt.Printf("%.3f %s\n", r.Value, r.Unit.Unit)
	}

	// Output:
	// KAM 78563412 (Water), security mode 5
	// 87654.321 m^3
	// 10.000 m^3
}

// With several meters in range, keep the keys in a KeyRing and let the
// telegram's identification number pick one.
func ExampleDecodeWithKeyRing() {
	keys := wmbus.KeyRing{
		78563412: mustHex(meterKey),
	}

	telegram, err := wmbus.DecodeWithKeyRing(mustHex(waterMeterTelegram), keys)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(telegram.SerialNumber, telegram.DataRecords[0].Value)

	// A meter the ring has no key for stops at ErrKeyRequired rather than
	// producing records from garbage.
	_, err = wmbus.DecodeWithKeyRing(mustHex(waterMeterTelegram), wmbus.KeyRing{})
	fmt.Println(errors.Is(err, wmbus.ErrKeyRequired))

	// Output:
	// 78563412 87654.321
	// true
}

// Parse stops at the link layer, before any key is needed. On a busy radio
// channel that is what filters the telegrams worth decrypting.
func ExampleParse() {
	frame, err := wmbus.Parse(mustHex(waterMeterTelegram))
	if err != nil {
		log.Fatal(err)
	}

	serial, err := frame.SerialNumber()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s %d\n", frame.Format, frame.Manufacturer(), serial)

	if serial != 78563412 {
		return
	}

	telegram, err := frame.Decode(mustHex(meterKey))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%.3f %s\n", telegram.DataRecords[0].Value, telegram.DataRecords[0].Unit.Unit)

	// Output:
	// A KAM 78563412
	// 87654.321 m^3
}

// A wrong key surfaces as ErrDecrypt, because the decrypted payload has to
// start with the two 0x2F filler bytes. A receiver loop can tell that apart
// from a corrupted telegram, which gives ErrCRC.
func ExampleDecode_wrongKey() {
	wrongKey := mustHex(meterKey)
	wrongKey[0] ^= 0xFF

	_, err := wmbus.Decode(mustHex(waterMeterTelegram), wrongKey)
	fmt.Println(errors.Is(err, wmbus.ErrDecrypt))

	corrupted := mustHex(waterMeterTelegram)
	corrupted[len(corrupted)-1] ^= 0xFF

	_, err = wmbus.Decode(corrupted, mustHex(meterKey))
	fmt.Println(errors.Is(err, wmbus.ErrCRC))

	// Output:
	// true
	// true
}
