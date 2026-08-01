package wmbus

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// plainTransport is an unencrypted short header transport layer, used by the
// link layer tests that only care about framing.
func plainTransport() []byte {
	return append(shortHeader(0x2A, 0x00, 0x0000), testRecords...)
}

func TestParseFormats(t *testing.T) {
	payload := plainTransport()

	tests := []struct {
		name  string
		raw   []byte
		parse func([]byte) (*Frame, error)
		want  Format
	}{
		{name: "format A", raw: buildFormatA(testAddress, payload), parse: ParseFormatA, want: FormatA},
		{name: "format B", raw: buildFormatB(testAddress, payload), parse: ParseFormatB, want: FormatB},
		{name: "stripped", raw: buildStripped(testAddress, payload), parse: ParseWithoutCRC, want: FormatStripped},
		{name: "auto detect A", raw: buildFormatA(testAddress, payload), parse: Parse, want: FormatA},
		{name: "auto detect B", raw: buildFormatB(testAddress, payload), parse: Parse, want: FormatB},
	}

	for _, tt := range tests {
		t.Run(
			tt.name, func(t *testing.T) {
				frame, err := tt.parse(tt.raw)
				require.NoError(t, err)
				require.Equal(t, tt.want, frame.Format)
				require.Equal(t, payload, frame.Payload)
				require.Equal(t, byte(0x44), frame.C)
				require.Equal(t, "KAM", frame.Manufacturer())
				require.Equal(t, testAddress.version, frame.Version)
				require.Equal(t, testAddress.deviceType, frame.DeviceType)

				serial, err := frame.SerialNumber()
				require.NoError(t, err)
				require.Equal(t, 78563412, serial)
			},
		)
	}
}

// TestParseFormatALongPayload covers a payload spanning several 16 byte
// blocks plus a short final block, which is where the block walk can drift.
func TestParseFormatALongPayload(t *testing.T) {
	payload := append(shortHeader(0x01, 0x00, 0x0000), make([]byte, 40)...)
	raw := buildFormatA(testAddress, payload)

	// 10 + 2 header, then blocks of 16+2, 16+2, 13+2.
	require.Len(t, raw, 12+18+18+15)

	frame, err := ParseFormatA(raw)
	require.NoError(t, err)
	require.Equal(t, payload, frame.Payload)
}

// TestParseFormatBTwoCRCBlocks covers a telegram past the 128 byte boundary,
// where a second CRC block appears.
func TestParseFormatBTwoCRCBlocks(t *testing.T) {
	payload := append(shortHeader(0x01, 0x00, 0x0000), make([]byte, 130)...)
	total := dllHeaderLen + len(payload) + 4
	require.Greater(t, total, formatBFirstCRCEnd)

	raw := append(testAddress.header(byte(total-1)), payload...)
	// Reopen the byte stream to insert the first CRC at its fixed offset.
	body := append([]byte(nil), raw[dllHeaderLen:]...)
	raw = raw[:dllHeaderLen]
	raw = append(raw, body[:formatBFirstCRCEnd-2-dllHeaderLen]...)
	raw = binary.BigEndian.AppendUint16(raw, crc16(raw))
	second := body[formatBFirstCRCEnd-2-dllHeaderLen:]
	raw = append(raw, second...)
	raw = binary.BigEndian.AppendUint16(raw, crc16(second))

	require.Len(t, raw, total)

	frame, err := ParseFormatB(raw)
	require.NoError(t, err)
	require.Equal(t, payload, frame.Payload)
}

func TestParseRejects(t *testing.T) {
	payload := plainTransport()

	corruptA := buildFormatA(testAddress, payload)
	corruptA[11] ^= 0xFF // first block CRC

	corruptABlock := buildFormatA(testAddress, payload)
	corruptABlock[len(corruptABlock)-1] ^= 0xFF // last block CRC

	corruptB := buildFormatB(testAddress, payload)
	corruptB[len(corruptB)-2] ^= 0xFF

	truncatedA := buildFormatA(testAddress, payload)
	truncatedA = truncatedA[:len(truncatedA)-4]

	longL := buildStripped(testAddress, payload)
	longL[0] = 0xFF

	tests := []struct {
		name  string
		raw   []byte
		parse func([]byte) (*Frame, error)
		want  error
	}{
		{name: "empty", raw: nil, parse: Parse, want: ErrInvalidFrame},
		{name: "header only", raw: make([]byte, 8), parse: Parse, want: ErrInvalidFrame},
		{name: "format A first block CRC", raw: corruptA, parse: ParseFormatA, want: ErrCRC},
		{name: "format A data block CRC", raw: corruptABlock, parse: ParseFormatA, want: ErrCRC},
		{name: "format B CRC", raw: corruptB, parse: ParseFormatB, want: ErrCRC},
		{name: "format A truncated", raw: truncatedA, parse: ParseFormatA, want: ErrInvalidFrame},
		{name: "L field beyond buffer", raw: longL, parse: ParseWithoutCRC, want: ErrInvalidFrame},
		{name: "L field below header", raw: []byte{0x02, 0x44, 0x2C, 0x2D, 0, 0, 0, 0, 0, 0}, parse: ParseWithoutCRC, want: ErrInvalidFrame},
		{name: "corrupt as either format", raw: corruptA, parse: Parse, want: ErrInvalidFrame},
	}

	for _, tt := range tests {
		t.Run(
			tt.name, func(t *testing.T) {
				frame, err := tt.parse(tt.raw)
				require.Nil(t, frame)
				require.ErrorIs(t, err, tt.want)
			},
		)
	}
}

// TestParseIgnoresTrailingBytes covers receivers that append a link quality
// byte after the telegram.
func TestParseIgnoresTrailingBytes(t *testing.T) {
	raw := append(buildFormatA(testAddress, plainTransport()), 0x9C)
	frame, err := ParseFormatA(raw)
	require.NoError(t, err)
	require.Equal(t, plainTransport(), frame.Payload)
}

func TestSerialNumberRejectsNonBCD(t *testing.T) {
	addr := testAddress
	addr.ident = [4]byte{0xAB, 0xCD, 0xEF, 0x00}
	frame, err := ParseWithoutCRC(buildStripped(addr, plainTransport()))
	require.NoError(t, err)

	_, err = frame.SerialNumber()
	require.ErrorIs(t, err, ErrInvalidFrame)
}

func TestFormatString(t *testing.T) {
	tests := []struct {
		format Format
		want   string
	}{
		{format: FormatA, want: "A"},
		{format: FormatB, want: "B"},
		{format: FormatStripped, want: "CRC-stripped"},
		{format: Format(0), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(
			tt.want, func(t *testing.T) {
				require.Equal(t, tt.want, tt.format.String())
			},
		)
	}
}
