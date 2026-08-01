package wmbus

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// requireTestRecords asserts the two records of testRecords: 87654321 and
// 10000 litres, both VIF 0x13 so the unit scales to cubic metres.
func requireTestRecords(t *testing.T, telegram *Telegram) {
	t.Helper()
	require.Len(t, telegram.DataRecords, 2)
	require.InDelta(t, 87654.321, telegram.DataRecords[0].Value, 0.0005)
	require.InDelta(t, 10.0, telegram.DataRecords[1].Value, 0.0005)
	for _, record := range telegram.DataRecords {
		require.NoError(t, record.ValueErr)
		require.Equal(t, "Instantaneous value", record.Function)
	}
}

func TestDecodePlaintext(t *testing.T) {
	tests := []struct {
		name      string
		transport []byte
		wantCI    byte
		wantACC   byte
	}{
		{
			name:      "short header",
			transport: append(shortHeader(0x2A, 0x04, 0x0000), testRecords...),
			wantCI:    CIShortHeader,
			wantACC:   0x2A,
		},
		{
			name: "long header",
			transport: append(
				[]byte{
					CILongHeader,
					0x12, 0x34, 0x56, 0x78, // identification number
					0x2D, 0x2C, // manufacturer KAM
					0x1B, 0x07, // version, device type
					0x2A, 0x04, // access number, status
					0x00, 0x00, // configuration word
				}, testRecords...,
			),
			wantCI:  CILongHeader,
			wantACC: 0x2A,
		},
		{
			name:      "no header",
			transport: append([]byte{CINoHeader}, testRecords...),
			wantCI:    CINoHeader,
			wantACC:   0x00,
		},
		{
			name: "extended link layer then short header",
			transport: append(
				[]byte{CIExtendedLinkI, 0x00, 0x2A},
				append(shortHeader(0x2A, 0x04, 0x0000), testRecords...)...,
			),
			wantCI:  CIShortHeader,
			wantACC: 0x2A,
		},
	}

	for _, tt := range tests {
		t.Run(
			tt.name, func(t *testing.T) {
				telegram, err := Decode(buildFormatA(testAddress, tt.transport), nil)
				require.NoError(t, err)

				require.Equal(t, "KAM", telegram.Manufacturer)
				require.Equal(t, 78563412, telegram.SerialNumber)
				require.Equal(t, byte(0x1B), telegram.Version)
				require.Equal(t, "Water", telegram.DeviceType)
				require.Equal(t, tt.wantCI, telegram.CI)
				require.Equal(t, tt.wantACC, telegram.AccessNumber)
				require.Equal(t, ModeNone, telegram.EncryptionMode)
				requireTestRecords(t, telegram)
			},
		)
	}
}

func TestDecodeStatus(t *testing.T) {
	transport := append(shortHeader(0x2A, 0x04, 0x0000), testRecords...)
	telegram, err := Decode(buildFormatA(testAddress, transport), nil)
	require.NoError(t, err)
	require.Equal(t, byte(0x04), telegram.Status)
}

func TestDecodeModeFive(t *testing.T) {
	transport := modeFiveTelegram(t, testAddress, testKey, testRecords)
	telegram, err := Decode(buildFormatA(testAddress, transport), testKey)
	require.NoError(t, err)

	require.Equal(t, ModeAESCBCIV, telegram.EncryptionMode)
	require.Equal(t, byte(0x2A), telegram.AccessNumber)
	require.Equal(t, 78563412, telegram.SerialNumber)
	requireTestRecords(t, telegram)
}

func TestDecodeModeSeven(t *testing.T) {
	transport := modeSevenTelegram(t, testAddress, 0x11223344, testKey, testRecords)
	telegram, err := Decode(buildFormatA(testAddress, transport), testKey)
	require.NoError(t, err)

	require.Equal(t, ModeAESCBCDerived, telegram.EncryptionMode)
	require.Equal(t, 78563412, telegram.SerialNumber)
	requireTestRecords(t, telegram)
}

// TestDecodePlaintextTail covers a telegram whose configuration word encrypts
// only the first blocks, leaving later records in the clear.
func TestDecodeModeFivePlaintextTail(t *testing.T) {
	tail := []byte{0x04, 0x13, 0xE8, 0x03, 0x00, 0x00} // binary, 1000 litres
	transport := append(modeFiveTelegram(t, testAddress, testKey, testRecords), tail...)

	telegram, err := Decode(buildFormatA(testAddress, transport), testKey)
	require.NoError(t, err)
	require.Len(t, telegram.DataRecords, 3)
	require.InDelta(t, 1.0, telegram.DataRecords[2].Value, 0.0005)
}

func TestDecodeKeyErrors(t *testing.T) {
	modeFive := buildFormatA(testAddress, modeFiveTelegram(t, testAddress, testKey, testRecords))

	wrongKey := append([]byte(nil), testKey...)
	wrongKey[0] ^= 0xFF

	tests := []struct {
		name string
		raw  []byte
		key  []byte
		want error
	}{
		{name: "no key", raw: modeFive, key: nil, want: ErrKeyRequired},
		{name: "wrong key", raw: modeFive, key: wrongKey, want: ErrDecrypt},
		{name: "short key", raw: modeFive, key: testKey[:8], want: ErrInvalidKey},
		{
			name: "wrong key mode seven",
			raw:  buildFormatA(testAddress, modeSevenTelegram(t, testAddress, 7, testKey, testRecords)),
			key:  wrongKey,
			want: ErrDecrypt,
		},
	}

	for _, tt := range tests {
		t.Run(
			tt.name, func(t *testing.T) {
				telegram, err := Decode(tt.raw, tt.key)
				require.Nil(t, telegram)
				require.ErrorIs(t, err, tt.want)
			},
		)
	}
}

func TestDecodeKeyRing(t *testing.T) {
	raw := buildFormatA(testAddress, modeFiveTelegram(t, testAddress, testKey, testRecords))

	telegram, err := DecodeWithKeyRing(raw, KeyRing{78563412: testKey})
	require.NoError(t, err)
	requireTestRecords(t, telegram)

	_, err = DecodeWithKeyRing(raw, KeyRing{1: testKey})
	require.ErrorIs(t, err, ErrKeyRequired)
}

func TestDecodeTransportErrors(t *testing.T) {
	withCI := func(transport []byte) []byte {
		return buildFormatA(testAddress, transport)
	}

	modeThirteenConfig := uint16(13)<<8 | 1<<4

	tests := []struct {
		name string
		raw  []byte
		want error
	}{
		{
			name: "unsupported CI",
			raw:  withCI([]byte{0x51, 0x00}),
			want: ErrUnsupportedCI,
		},
		{
			name: "extended link layer two",
			raw:  withCI([]byte{CIExtendedLinkII, 0x00, 0x2A, 0, 0, 0, 0}),
			want: ErrUnsupportedMode,
		},
		{
			name: "unsupported security mode",
			raw:  withCI(append(shortHeader(0x2A, 0x00, modeThirteenConfig), make([]byte, 16)...)),
			want: ErrUnsupportedMode,
		},
		{
			name: "empty transport layer",
			raw:  withCI(nil),
			want: ErrInvalidFrame,
		},
		{
			name: "short header truncated",
			raw:  withCI([]byte{CIShortHeader, 0x2A, 0x00}),
			want: ErrInvalidFrame,
		},
		{
			name: "long header truncated",
			raw:  withCI([]byte{CILongHeader, 0x12, 0x34}),
			want: ErrInvalidFrame,
		},
		{
			name: "extended link layer truncated",
			raw:  withCI([]byte{CIExtendedLinkI, 0x00}),
			want: ErrInvalidFrame,
		},
		{
			name: "encrypted blocks beyond payload",
			raw:  withCI(append(shortHeader(0x2A, 0x00, uint16(ModeAESCBCIV)<<8|4<<4), make([]byte, 16)...)),
			want: ErrInvalidFrame,
		},
		{
			name: "mode seven without message counter",
			raw:  withCI(append(shortHeader(0x2A, 0x00, uint16(ModeAESCBCDerived)<<8|1<<4), make([]byte, 17)...)),
			want: ErrInvalidFrame,
		},
		{
			name: "authentication layer truncated",
			raw:  withCI([]byte{CIAuthFragmentation, 0x08, 0x00, 0x10}),
			want: ErrInvalidFrame,
		},
		{
			name: "record runs past the payload",
			raw:  withCI(append(shortHeader(0x2A, 0x00, 0x0000), 0x0C, 0x13, 0x01)),
			want: ErrInvalidFrame,
		},
	}

	for _, tt := range tests {
		t.Run(
			tt.name, func(t *testing.T) {
				telegram, err := Decode(tt.raw, testKey)
				require.Nil(t, telegram)
				require.ErrorIs(t, err, tt.want)
			},
		)
	}
}

// TestDecodeNonBCDIdent keeps the records decodable when the meter fills the
// identification number with bytes that are not BCD.
func TestDecodeNonBCDIdent(t *testing.T) {
	addr := testAddress
	addr.ident = [4]byte{0xAB, 0xCD, 0xEF, 0x01}
	transport := append(shortHeader(0x2A, 0x00, 0x0000), testRecords...)

	telegram, err := Decode(buildFormatA(addr, transport), nil)
	require.NoError(t, err)
	require.Equal(t, 0, telegram.SerialNumber)
	requireTestRecords(t, telegram)
}
