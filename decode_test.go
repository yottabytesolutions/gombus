package gombus

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Bounds-safety / spec-conformance tests ----------------------------------

// TestDecodeShortFrameRejected verifies that Decode rejects frames too short
// to contain the variable-data header instead of panicking on the
// fixed-offset reads (lf[13..19]).
func TestDecodeShortFrameRejected(t *testing.T) {
	for _, n := range []int{0, 1, 6, 14, 19, 20} {
		t.Run("len="+strconv.Itoa(n), func(t *testing.T) {
			short := make(LongFrame, n)
			if n >= 7 {
				short[6] = 0x72
			}
			_, err := short.Decode()
			assert.ErrorIs(t, err, ErrInvalidFrame)
		})
	}
}

func TestDecodeUnsupportedCI(t *testing.T) {
	lf := LongFrame(make([]byte, 21))
	lf[6] = 0x71 // report of alarm status, neither a variable nor a fixed response
	_, err := lf.Decode()
	assert.ErrorIs(t, err, ErrUnsupportedCI)
}

func TestDecodeTruncatedDataRecord(t *testing.T) {
	cases := map[string]string{
		"64-bit truncated":          "07 00 01 02 03",
		"48-bit truncated":          "06 00 01 02",
		"variable string truncated": "0d 00 10 41 42 43 44",
		"custom unit truncated":     "00 7c 05 41 42",
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			lf := wrapAsLongFrame(hexToBytes(h))
			_, err := lf.Decode()
			assert.Error(t, err)
		})
	}
}

func TestDecodeOversizedLVARRejected(t *testing.T) {
	// LVAR=0xFF claims 255 bytes but none follow.
	lf := wrapAsLongFrame(hexToBytes("0d 00 ff"))
	_, err := lf.Decode()
	assert.Error(t, err)
}

// TestDecodeDIF1FStateMachine verifies that 0x1F closes parsing rather than
// being parsed as a regular DIF expecting VIF/data afterwards.
func TestDecodeDIF1FStateMachine(t *testing.T) {
	// Valid 2-digit BCD record + 0x1F sentinel + trailing junk.
	lf := wrapAsLongFrame(hexToBytes("09 00 12 1f aa"))
	dec, err := lf.Decode()
	require.NoError(t, err)
	assert.Len(t, dec.DataRecords, 2)
	assert.False(t, dec.DataRecords[0].HasMoreRecords)
	assert.True(t, dec.DataRecords[1].HasMoreRecords)
	assert.Equal(t, "More records follow", dec.DataRecords[1].Function)
	assert.True(t, dec.HasMoreRecords())
}

func TestDIFEVIFEOverflow(t *testing.T) {
	t.Run("too many DIFE", func(t *testing.T) {
		// DIF with extension bit + 11 DIFE bytes all with extension set.
		bytes := append([]byte{0x8F}, makeRepeated(0x80, 11)...)
		_, err := LongFrame{}.decodeData(bytes)
		assert.ErrorContains(t, err, "too many DIFE extensions")
	})
	t.Run("too many VIFE", func(t *testing.T) {
		// DIF=0x02 (16-bit, no ext), VIF=0xFB (with ext), 11 VIFE.
		bytes := append([]byte{0x02, 0xFB}, makeRepeated(0x80, 11)...)
		_, err := LongFrame{}.decodeData(bytes)
		assert.ErrorContains(t, err, "too many VIFE extensions")
	})
}

func TestSecondaryAddressStringShort(t *testing.T) {
	df := DecodedFrame{raw: []byte{0x68, 0x06, 0x06, 0x68}, SerialNumber: 42}
	assert.Equal(t, "42", df.SecondaryAddressString())
}

// --- Header field decoding ---------------------------------------------------

func TestDecodeFrameHeaderFields(t *testing.T) {
	// Hand-crafted minimal RSP_UD telegram:
	// 78 56 34 12 ID, 24 40 01 07 manufacturer/version/medium, 55/42/12 34 status block.
	frameData := []byte{
		0x68, 0x1F, 0x1F, 0x68, // start, L, L, start
		0x08, 0x02, 0x72, // C, A, CI
		0x78, 0x56, 0x34, 0x12, // ID
		0x24, 0x40, // manufacturer
		0x01,       // version
		0x07,       // medium
		0x55,       // AccessNumber
		0x42,       // status
		0x34, 0x12, // signature
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x16,
	}

	df, err := LongFrame(frameData).Decode()
	require.NoError(t, err)
	assert.Equal(t, 12345678, df.SerialNumber)
	assert.Equal(t, byte(0x55), df.AccessNumber)
	assert.Equal(t, byte(0x42), df.Status)
	assert.Equal(t, uint16(0x1234), df.Signature)
}

// --- decodeRecordFunction labels --------------------------------------------

// TestDecodeRecordFunctionSpecialDIFs verifies that the special-function DIF
// bytes (0x0F, 0x1F, 0x2F, 0x7F) are labelled correctly instead of being
// passed through the function-bit mask (which would mislabel them as
// instantaneous/maximum/minimum values per EN 13757-3 §6.2).
func TestDecodeRecordFunctionSpecialDIFs(t *testing.T) {
	cases := map[byte]string{
		0x0F: "Manufacturer specific",
		0x1F: "More records follow",
		0x2F: "Idle filler",
		0x7F: "Global readout request",
		0x00: "Instantaneous value",
		0x10: "Maximum value",
		0x20: "Minimum value",
		0x30: "Value during error state",
	}
	for dif, want := range cases {
		assert.Equal(t, want, decodeRecordFunction(dif), "decodeRecordFunction(0x%02x)", dif)
	}
}

// --- ReadableStatus ----------------------------------------------------------

func TestReadableStatus(t *testing.T) {
	cases := []struct {
		status byte
		want   string
	}{
		{0x00, "Normal operation"},
		{0x01, "Application busy"},
		{0x02, "Application error"},
		{0x04, "Power low"},
		{0x08, "Permanent error"},
		{0x10, "Temporary error"},
		{0x20, "Specific to manufacturer"},
		{0x40, "Specific to manufacturer"},
		{0x80, "Specific to manufacturer"},
		{0x03, "Application busy, Application error"},
		{0x15, "Application busy, Power low, Temporary error"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("Status_0x%02X", tc.status), func(t *testing.T) {
			df := DecodedFrame{Status: tc.status}
			assert.Equal(t, tc.want, df.ReadableStatus())
		})
	}
}

// --- ProductName extraction --------------------------------------------------

func TestExtractProductNameGenericHeuristic(t *testing.T) {
	records := []DecodedDataRecord{
		{Unit: Unit{Unit: "kWh"}, Value: 123},
		{Unit: Unit{Unit: "cust. ID"}, ValueString: "ABC123"},
	}
	assert.Equal(t, "ABC123", extractProductName("ITW", records))
}

func TestExtractProductNameKamstrupMulticalFallback(t *testing.T) {
	records := []DecodedDataRecord{
		{Unit: Unit{Unit: "none"}, Value: 2103601},
	}
	assert.Equal(t, "Multical 2103601", extractProductName("KAM", records))
}

func TestExtractProductNameNoMatch(t *testing.T) {
	records := []DecodedDataRecord{
		{Unit: Unit{Unit: "kWh"}, Value: 0},
		{Unit: Unit{Unit: "m^3"}, Value: 1},
	}
	assert.Empty(t, extractProductName("KAM", records))
	assert.Empty(t, extractProductName("ITW", records))
}

// --- Real-meter spot checks (synthesized / hand-validated frames) ------------

// TestDecodeGAROElectricMeterSecondFrame is the second frame of a GARO 3-phase
// electricity meter response; it exercises 32-bit integer values, DIFEs for
// device/storage selection, and the 1F end-of-frame marker.
func TestDecodeGAROElectricMeterSecondFrame(t *testing.T) {
	s := `
		68 78 78 68 08 01 72 14 21 07 90 36 1c c7 02 25 00 00 00
		84 40 2a a0 09 00 00
		84 80 40 2a ba 00 00 00
		84 c0 40 2a 00 00 00 00
		84 40 fb 97 72 fb fe ff ff
		84 80 40 fb 97 72 4b 00 00 00
		84 c0 40 fb 97 72 00 00 00 00
		84 40 fb b7 72 ae 09 00 00
		84 80 40 fb b7 72 c8 00 00 00
		84 c0 40 fb b7 72 00 00 00 00
		82 40 fd ba 73 e2 03
		82 80 40 fd ba 73 9f 03
		82 c0 40 fd ba 73 00 00 1f
		ef 16`
	df, err := LongFrame(hexToBytes(s)).Decode()
	require.NoError(t, err)

	assert.Equal(t, 90072114, df.SerialNumber)
	assert.Equal(t, "GAV", df.Manufacturer)
	assert.Equal(t, 199, df.Version)
	assert.Equal(t, byte(0), df.Status)
	assert.Len(t, df.DataRecords, 13)

	// First record: device=1, raw=2464, exp=0.1 → value=246.4.
	r0 := df.DataRecords[0]
	assert.Equal(t, "Instantaneous value", r0.Function)
	assert.Equal(t, 1, r0.Device)
	assert.InDelta(t, 2464.0, r0.RawValue, 0)
	assert.InDelta(t, 246.4, r0.Value, 1e-9)
	assert.InDelta(t, 0.1, r0.Unit.Exp, 1e-12)

	// Second record: device=2, unit "W".
	r1 := df.DataRecords[1]
	assert.Equal(t, 2, r1.Device)
	assert.Equal(t, "W", r1.Unit.Unit)

	// Tenth record: storage=0, device=1, value=994.
	r9 := df.DataRecords[9]
	assert.Equal(t, 1, r9.Device)
	assert.InDelta(t, 994.0, r9.RawValue, 0)

	assert.True(t, df.HasMoreRecords())
}

func TestDecodeGAROElectricMeterFirstFrame(t *testing.T) {
	s := `68 65 65 68 08 01 72 14 21 07 90 36 1c c7 02 4d 00 00 00 04 05 9c 31 01
	      00 04 fb 82 75 63 91 00 00 04 2a 36 08 00 00 04 fb 97 72 ca fe ff ff 04
	      fb b7 72 6d 08 00 00 02 fd ba 73 dc 03 84 80 80 40 fd 48 c4 0f 00 00 04
	      fd 48 1a 09 00 00 84 40 fd 59 d2 04 00 00 84 80 40 fd 59 78 00 00 00 84
	      c0 40 fd 59 00 00 00 00 1f 95 16`
	df, err := LongFrame(hexToBytes(s)).Decode()
	require.NoError(t, err)
	assert.Equal(t, 90072114, df.SerialNumber)
	assert.True(t, df.HasMoreRecords())
}

// TestDecodeItronWaterMeterFirstFrame: the trailing `0F 00 00 1F` is a
// manufacturer-specific section per EN 13757-3 §6.2; libmbus decodes the same
// pattern identically as 8 records with the trailing one labelled
// "Manufacturer specific".
func TestDecodeItronWaterMeterFirstFrame(t *testing.T) {
	s := `68 56 56 68 08 02 72 36 46 00 19 77 04 14 07 9d 10 00 00 0c 78 36 46 00
	      19 0d 7c 08 44 49 20 2e 74 73 75 63 0a 20 20 20 20 20 20 20 20 20 20 04
	      6d 35 14 d3 26 02 7c 09 65 6d 69 74 20 2e 74 61 62 97 10 04 13 01 6e 03
	      00 04 93 7f 00 00 00 00 44 13 27 51 03 00 0f 00 00 1f 96 16`
	df, err := LongFrame(hexToBytes(s)).Decode()
	require.NoError(t, err)

	assert.Equal(t, 19004636, df.SerialNumber)
	assert.False(t, df.HasMoreRecords())
	assert.Len(t, df.DataRecords, 8)
	assert.InDelta(t, 217383.0, df.DataRecords[6].RawValue, 0)
	assert.Equal(t, "bat. time", df.DataRecords[3].Unit.Unit)
	assert.Equal(t, "cust. ID", df.DataRecords[1].Unit.Unit)
	assert.Equal(t, "Manufacturer specific", df.DataRecords[7].Function)
}

// TestDecodeKamstrupMulticalProductName verifies that the legacy "Multical
// NNN" heuristic kicks in for a real Kamstrup MULTICAL 603 frame.
func TestDecodeKamstrupMulticalProductName(t *testing.T) {
	s := `68 C7 C7 68 08 01 72 02 75 92 72 2D 2C 34 0C 53 00 00 00
	      04 0E E0 01 00 00 04 FF 07 BA 0B 00 00 04 FF 08 24 07 00 00
	      04 13 91 12 00 00 84 40 14 00 00 00 00 84 80 40 14 00 00 00 00
	      04 22 F0 0B 00 00 34 22 00 00 00 00 02 59 50 15 02 5D FF 13
	      02 61 51 01 04 2D 00 00 00 00 14 2D 28 00 00 00 04 3B 00 00 00 00
	      14 3B 5D 00 00 00 04 FF 22 00 00 00 00 04 6D 3B 2A F6 27
	      44 0E F4 00 00 00 44 FF 07 0C 06 00 00 44 FF 08 B5 03 00 00
	      44 13 9E 09 00 00 C4 40 14 00 00 00 00 C4 80 40 14 00 00 00 00
	      54 2D 25 00 00 00 54 3B 5D 00 00 00 42 6C E1 27 02 FF 1A 01 1A 0C
	      78 02 75 92 72 04 FF 16 86 0B 20 00 04 FF 17 C9 FF 0E 01 49 16`
	df, err := LongFrame(hexToBytes(s)).Decode()
	require.NoError(t, err)
	assert.Equal(t, 72927502, df.SerialNumber)
	assert.NotEmpty(t, df.DataRecords)
	assert.Contains(t, df.ProductName, "Multical")
}

// --- Helpers ----------------------------------------------------------------

// wrapAsLongFrame builds a minimum-shaped LongFrame whose payload (between
// the 19-byte header and the 2-byte trailer) is the given record bytes.
// Header values are not meaningful; we only need Decode to reach decodeData.
func wrapAsLongFrame(payload []byte) LongFrame {
	lf := make(LongFrame, 19+len(payload)+2)
	lf[0] = 0x68
	lf[3] = 0x68
	lf[1] = byte(len(lf) - 6)
	lf[2] = lf[1]
	lf[4] = 0x08 // C
	lf[5] = 0x01 // A
	lf[6] = 0x72 // CI variable data response
	lf[11] = 0x2D
	lf[12] = 0x2C
	lf[13] = 0x01
	lf[14] = 0x00
	copy(lf[19:], payload)
	lf[len(lf)-1] = 0x16
	return lf
}

func makeRepeated(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
