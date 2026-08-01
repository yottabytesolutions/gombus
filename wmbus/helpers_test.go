package wmbus

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// testKey is an arbitrary AES-128 key used by every crypto test.
var testKey = []byte{
	0x0f, 0x1e, 0x2d, 0x3c, 0x4b, 0x5a, 0x69, 0x78,
	0x87, 0x96, 0xa5, 0xb4, 0xc3, 0xd2, 0xe1, 0xf0,
}

// testAddress is the link layer address the fixtures use: manufacturer KAM,
// identification number 78563412, version 0x1B, device type 0x07 (water).
var testAddress = address{
	manufacturer: 0x2C2D, // KAM, transmitted as 2D 2C
	ident:        [4]byte{0x12, 0x34, 0x56, 0x78},
	version:      0x1B,
	deviceType:   0x07,
}

// address holds the link layer identity fields of a synthetic telegram.
type address struct {
	manufacturer uint16
	ident        [4]byte
	version      byte
	deviceType   byte
}

// testC is the SND-NR control field of an unsolicited meter transmission.
const testC = 0x44

// testAccessNumber is the access number every fixture transmits.
const testAccessNumber = 0x2A

// header builds the 10 byte link layer header for a given L field.
func (a address) header(l byte) []byte {
	head := make([]byte, dllHeaderLen)
	head[0] = l
	head[1] = testC
	binary.LittleEndian.PutUint16(head[2:4], a.manufacturer)
	copy(head[4:8], a.ident[:])
	head[8] = a.version
	head[9] = a.deviceType
	return head
}

// appendCRC appends the two big endian CRC bytes of block.
func appendCRC(dst, block []byte) []byte {
	dst = append(dst, block...)
	return binary.BigEndian.AppendUint16(dst, crc16(block))
}

// buildFormatA wraps a transport payload in a frame format A telegram: first
// block with its CRC, then 16 byte blocks each with their own CRC.
func buildFormatA(a address, payload []byte) []byte {
	l := byte(dllHeaderLen - 1 + len(payload))
	raw := appendCRC(nil, a.header(l))
	for i := 0; i < len(payload); i += 16 {
		raw = appendCRC(raw, payload[i:min(i+16, len(payload))])
	}
	return raw
}

// buildFormatB wraps a transport payload in a frame format B telegram. It
// covers the single CRC block case only, which is every telegram up to 128
// bytes; the two block case is built explicitly by the test that needs it.
func buildFormatB(a address, payload []byte) []byte {
	total := dllHeaderLen + len(payload) + 2
	raw := append(a.header(byte(total-1)), payload...)
	return binary.BigEndian.AppendUint16(raw, crc16(raw))
}

// buildStripped wraps a transport payload the way a receiver that verified and
// removed the CRCs hands it over.
func buildStripped(a address, payload []byte) []byte {
	l := byte(dllHeaderLen - 1 + len(payload))
	return append(a.header(l), payload...)
}

// testRecords is a two record application payload: a BCD volume and a binary
// volume, both with VIF 0x13 (volume, 0.001 m3).
var testRecords = []byte{
	0x0C, 0x13, 0x21, 0x43, 0x65, 0x87, // BCD, 87654321 litres
	0x04, 0x13, 0x10, 0x27, 0x00, 0x00, // binary, 10000 litres
}

// shortHeader builds an unencrypted CI 0x7A transport header.
func shortHeader(accessNumber, status byte, config uint16) []byte {
	head := []byte{CIShortHeader, accessNumber, status, 0, 0}
	binary.LittleEndian.PutUint16(head[3:5], config)
	return head
}

// padPlaintext prefixes the 2F 2F marker and pads with 0x2F filler up to a
// whole number of AES blocks, which is what a meter encrypts.
func padPlaintext(records []byte) []byte {
	plain := append([]byte{fillerByte, fillerByte}, records...)
	for len(plain)%aes.BlockSize != 0 {
		plain = append(plain, fillerByte)
	}
	return plain
}

// encryptCBC is the test side counterpart of decryptCBC. It is written out
// here rather than reusing the package's own primitives so a round trip test
// exercises the documented construction and not just its inverse.
func encryptCBC(t *testing.T, key, iv, plain []byte) []byte {
	t.Helper()
	require.Zero(t, len(plain)%aes.BlockSize, "plaintext must be block aligned")
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	return out
}

// modeFiveTelegram builds a security mode 5 transport layer: short header,
// configuration word and ciphertext.
func modeFiveTelegram(t *testing.T, a address, key, records []byte) []byte {
	t.Helper()
	plain := padPlaintext(records)
	blocks := len(plain) / aes.BlockSize

	iv := make([]byte, aes.BlockSize)
	binary.LittleEndian.PutUint16(iv[0:2], a.manufacturer)
	copy(iv[2:6], a.ident[:])
	iv[6] = a.version
	iv[7] = a.deviceType
	for i := 8; i < aes.BlockSize; i++ {
		iv[i] = testAccessNumber
	}

	config := uint16(ModeAESCBCIV)<<8 | uint16(blocks)<<4
	return append(shortHeader(testAccessNumber, 0x00, config), encryptCBC(t, key, iv, plain)...)
}

// modeSevenTelegram builds a security mode 7 transport layer: an
// authentication layer carrying the message counter, then a short header with
// the configuration field extension and the ciphertext.
func modeSevenTelegram(t *testing.T, a address, counter uint32, masterKey, records []byte) []byte {
	t.Helper()
	plain := padPlaintext(records)
	blocks := len(plain) / aes.BlockSize

	mcr := make([]byte, 4)
	binary.LittleEndian.PutUint32(mcr, counter)

	// Key derivation input: derivation constant, message counter,
	// identification number, then 0x07 filler up to one AES block.
	input := append([]byte{keyDerivationEncrypt}, mcr...)
	input = append(input, a.ident[:]...)
	for len(input) < aes.BlockSize {
		input = append(input, 0x07)
	}
	messageKey, err := cmacAES(masterKey, input)
	require.NoError(t, err)

	config := uint16(ModeAESCBCDerived)<<8 | uint16(blocks)<<4

	out := make([]byte, 0, 16+len(plain))
	out = append(out, CIAuthFragmentation, 0x06, 0x00, aflMessageCounterPresent>>8)
	out = append(out, mcr...)
	out = append(out, shortHeader(testAccessNumber, 0x00, config)...)
	out = append(out, 0x00) // configuration field extension

	iv := make([]byte, aes.BlockSize)
	return append(out, encryptCBC(t, messageKey, iv, plain)...)
}
