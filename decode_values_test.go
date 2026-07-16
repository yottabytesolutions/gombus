package gombus

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here assert decoded values rather than header fields. The suite
// they complement asserts only the fixed ident block, so a signedness or
// scaling regression passed it green.

// TestDecodeSignedIntegerWidths pins EN 13757-3 type B two's complement decoding
// for every binary DIF data field. Before the fix these read as unsigned, so a
// -1 flow temperature decoded as 65535 at 16 bits and the 32-bit case decoded
// differently per target architecture.
func TestDecodeSignedIntegerWidths(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantRaw float64
	}{
		// DIF 0x01..0x07 with VIF 0x5A (flow temperature, exp 0.1 C).
		{"8-bit -1", "01 5A FF", -1},
		{"8-bit min", "01 5A 80", -128},
		{"8-bit positive", "01 5A 7F", 127},
		{"16-bit -1", "02 5A FF FF", -1},
		{"16-bit min", "02 5A 00 80", -32768},
		{"16-bit positive", "02 5A D4 09", 2516},
		{"24-bit -100", "03 5A 9C FF FF", -100},
		{"24-bit min", "03 5A 00 00 80", -8388608},
		{"32-bit -1", "04 5A FF FF FF FF", -1},
		{"32-bit min", "04 5A 00 00 00 80", -2147483648},
		{"48-bit -1", "06 5A FF FF FF FF FF FF", -1},
		{"64-bit -1", "07 5A FF FF FF FF FF FF FF FF", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lf := wrapAsLongFrame(hexToBytes(tc.payload))
			df, err := lf.Decode()
			require.NoError(t, err)
			require.Len(t, df.DataRecords, 1)
			assert.InDelta(t, tc.wantRaw, df.DataRecords[0].RawValue, 0, "RawValue")
			// Exp 0.1: the sign must survive scaling into Value too.
			assert.InDelta(t, tc.wantRaw*0.1, df.DataRecords[0].Value, 1e-9, "Value")
		})
	}
}

// TestDecodeRawFieldsNotSignExtended pins the other half of the signedness
// decision: entries the unit table marks Raw carry bitfields, so sign-extending
// them is corruption. The live case is a 32-bit error-flags field with the top
// flag bit set.
func TestDecodeRawFieldsNotSignExtended(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantRaw float64
	}{
		// VIF 0xFD + VIFE 0x17 = error flags (0x117), marked Raw.
		{"error flags top bit set", "04 FD 17 00 00 00 80", 2147483648},
		{"error flags all ones", "04 FD 17 FF FF FF FF", 4294967295},
		// VIFE 0x18 = error masks (0x118).
		{"error masks all ones", "04 FD 18 FF FF FF FF", 4294967295},
		// VIFE 0x1A / 0x1B = digital output / input (0x11A / 0x11B).
		{"digital output", "02 FD 1A FF FF", 65535},
		{"digital input", "02 FD 1B FF FF", 65535},
		// VIF 0x6C = date (type G), 0x6D = date time (type F).
		{"date top bit set", "02 6C 00 80", 32768},
		{"date time top bit set", "04 6D 00 00 00 80", 2147483648},
		// VIF 0x78 / 0x79 / 0x7A = fabrication no / identification / address.
		{"fabrication number", "04 78 FF FF FF FF", 4294967295},
		{"identification", "04 79 FF FF FF FF", 4294967295},
		{"address", "04 7A FF FF FF FF", 4294967295},
		// VIF 0xFD + VIFE 0x70 = date and time of battery change (0x170).
		{"battery change date", "04 FD 70 00 00 00 80", 2147483648},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lf := wrapAsLongFrame(hexToBytes(tc.payload))
			df, err := lf.Decode()
			require.NoError(t, err)
			require.Len(t, df.DataRecords, 1)
			require.True(t, df.DataRecords[0].Unit.Raw, "unit must be marked Raw")
			assert.InDelta(t, tc.wantRaw, df.DataRecords[0].RawValue, 0)
		})
	}
}

// TestDecodeUnmarkedFieldOfSameWidthStillSigns guards the Raw flag against being
// applied too widely: an ordinary 32-bit reading must still sign-extend.
func TestDecodeUnmarkedFieldOfSameWidthStillSigns(t *testing.T) {
	lf := wrapAsLongFrame(hexToBytes("04 5A FF FF FF FF"))
	df, err := lf.Decode()
	require.NoError(t, err)
	require.Len(t, df.DataRecords, 1)
	assert.False(t, df.DataRecords[0].Unit.Raw)
	assert.InDelta(t, -1.0, df.DataRecords[0].RawValue, 0)
}

// TestDecodeBCDSigned pins the DIF BCD path: the sign lives in the most
// significant nibble, and illegal nibbles are rejected instead of decoding as
// bogus digits.
func TestDecodeBCDSigned(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantRaw float64
	}{
		{"2-digit", "09 5A 12", 12},
		{"4-digit", "0A 5A 34 12", 1234},
		{"6-digit", "0B 5A 56 34 12", 123456},
		{"8-digit", "0C 5A 78 56 34 12", 12345678},
		{"12-digit", "0E 5A 12 00 00 00 00 00", 12},
		{"negative 8-digit", "0C 5A 34 12 00 F0", -1234},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lf := wrapAsLongFrame(hexToBytes(tc.payload))
			df, err := lf.Decode()
			require.NoError(t, err)
			require.Len(t, df.DataRecords, 1)
			require.NoError(t, df.DataRecords[0].ValueErr)
			assert.InDelta(t, tc.wantRaw, df.DataRecords[0].RawValue, 0)
		})
	}
}

// TestDecodeInvalidValueDoesNotAbortFrame and TestDecodeTruncatedTrailingRecord
// are a pair. They document the rule that decides which failures are fatal: a
// structural error loses sync and fails the frame, an invalid value does not.
// The DIF gives the field width, so the next record's position is known and the
// surrounding readings survive.
func TestDecodeInvalidValueDoesNotAbortFrame(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		// index of the record expected to carry ValueErr.
		badIndex int
	}{
		// Illegal nibbles in each BCD width, between two good readings.
		{"2-digit BCD", "01 13 07 09 5A AB 01 13 09", 1},
		{"4-digit BCD", "01 13 07 0A 5A AB CD 01 13 09", 1},
		{"8-digit BCD", "01 13 07 0C 2B BD EB DD DD 01 13 09", 1},
		// All-F is a not-available marker, not a large negative number.
		{"all-F BCD", "01 13 07 0C 5A FF FF FF FF 01 13 09", 1},
		// LVAR BCD carries its width in the LVAR byte, so it recovers too.
		{"LVAR BCD", "01 13 07 0D 13 C2 AB CD 01 13 09", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			df, err := wrapAsLongFrame(hexToBytes(tc.payload)).Decode()
			require.NoError(t, err, "an invalid value must not fail the frame")
			require.Len(t, df.DataRecords, 3, "the good records must survive")

			bad := df.DataRecords[tc.badIndex]
			require.Error(t, bad.ValueErr, "the bad record must report why")
			assert.InDelta(t, 0.0, bad.RawValue, 0)
			assert.InDelta(t, 0.0, bad.Value, 0)

			// The readings either side decode normally and stay in sync.
			for _, i := range []int{0, 2} {
				require.NoError(t, df.DataRecords[i].ValueErr, "record %d", i)
			}
			assert.InDelta(t, 7.0, df.DataRecords[0].RawValue, 0)
			assert.InDelta(t, 9.0, df.DataRecords[2].RawValue, 0)
		})
	}
}

// TestDecodeRecordCounts covers the two record-walk bugs together: both came
// from the skip counter, and both let one record swallow the next.
func TestDecodeRecordCounts(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		wantRecords int
		wantRaw     []float64
	}{
		// LVAR BCD returned a digit count where the caller wanted a byte count,
		// so the walk skipped twice the record length and ate the next record.
		{"LVAR BCD then 16-bit", "0D 13 C2 34 12 01 13 07", 2, []float64{1234, 7}},
		{"LVAR negative BCD then 16-bit", "0D 13 D2 34 12 01 13 07", 2, []float64{-1234, 7}},
		// Zero-length data fields consumed the next record's DIF.
		{"no data then 16-bit", "00 13 01 13 07", 2, []float64{0, 7}},
		{"selection for readout then 16-bit", "08 13 01 13 07", 2, []float64{0, 7}},
		{"two zero-length then 16-bit", "00 13 00 13 01 13 07", 3, []float64{0, 0, 7}},
		// A DIF whose data field is 1111 but which is not 0x0F / 0x1F / 0x2F.
		{"sentinel then 16-bit", "4F 13 01 13 07", 2, []float64{0, 7}},
		// Idle filler is skipped without emitting a record.
		{"idle filler between records", "01 13 07 2F 01 13 09", 2, []float64{7, 9}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lf := wrapAsLongFrame(hexToBytes(tc.payload))
			df, err := lf.Decode()
			require.NoError(t, err)
			require.Len(t, df.DataRecords, tc.wantRecords)
			for i, want := range tc.wantRaw {
				assert.InDelta(t, want, df.DataRecords[i].RawValue, 0, "record %d", i)
			}
		})
	}
}

// TestDecodeTruncatedTrailingRecord pins that a meter truncating mid-record is
// distinguishable from a clean end. The walk used to return the records it had
// collected with a nil error.
func TestDecodeTruncatedTrailingRecord(t *testing.T) {
	cases := map[string]string{
		"DIF with no VIF":       "01 13 07 02",
		"DIFE chain unfinished": "01 13 07 82",
		"VIF with no data":      "01 13 07 02 13",
		"VIFE chain unfinished": "01 13 07 02 FD",
		"data field short":      "01 13 07 04 13 01 02",
		"LVAR with no payload":  "01 13 07 0D 13 04 41",
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			lf := wrapAsLongFrame(hexToBytes(payload))
			_, err := lf.Decode()
			require.Error(t, err)
			assert.ErrorIs(t, err, errShortDataRecord)
		})
	}
}

// TestDecodeManufacturerShortFrame pins the bounds check. This used to panic
// with "slice bounds out of range [:13] with capacity 8", and the error return
// was always nil so the branch every caller wrote was dead.
func TestDecodeManufacturerShortFrame(t *testing.T) {
	for _, n := range []int{0, 1, 8, 11, 12} {
		t.Run(string(rune('0'+n%10)), func(t *testing.T) {
			short := make(LongFrame, n)
			man, err := short.DecodeManufacturer()
			require.ErrorIs(t, err, ErrInvalidFrame)
			assert.Empty(t, man)
		})
	}
}

func TestDecodeManufacturerValid(t *testing.T) {
	lf := make(LongFrame, 13)
	// "GAV": ((7<<10) | (1<<5) | 22) = 0x1C36, little-endian 36 1C.
	lf[11], lf[12] = 0x36, 0x1C
	man, err := lf.DecodeManufacturer()
	require.NoError(t, err)
	assert.Equal(t, "GAV", man)
}

// TestDecodePlainTextVIFEmptyUnit pins that an empty plain-text unit is still
// recognised as a plain-text VIF. Detecting it by empty-string sentinel meant
// LVAR=0 fell through to decodeUnit(0xFC, vife), which then hit the unknown-VIFE
// factor and reported the reading as 0 while RawValue held the truth.
func TestDecodePlainTextVIFEmptyUnit(t *testing.T) {
	cases := map[string]string{
		// DIF 0x02 (16-bit), VIF 0xFC plain text with extension, LVAR 0x00
		// (no ASCII), VIFE 0x00 (unknown), data 07 00.
		"0xFC with extension": "02 FC 00 00 07 00",
		// 0x7C is the same shape without the extension bit.
		"0x7C without extension": "02 7C 00 07 00",
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			df, err := wrapAsLongFrame(hexToBytes(payload)).Decode()
			require.NoError(t, err)
			require.Len(t, df.DataRecords, 1)

			rec := df.DataRecords[0]
			assert.Empty(t, rec.Unit.Unit)
			assert.Equal(t, vifUnit["VARIABLE_VIF"], rec.Unit.Type)
			assert.InDelta(t, 1.0, rec.Unit.Exp, 0, "empty custom unit must not zero the exponent")
			assert.InDelta(t, 7.0, rec.RawValue, 0)
			assert.InDelta(t, 7.0, rec.Value, 0, "value must not be annihilated")
		})
	}
}

func TestDecodePlainTextVIFUnit(t *testing.T) {
	// LVAR 0x03, ASCII "ABC" reversed on the wire.
	lf := wrapAsLongFrame(hexToBytes("02 7C 03 43 42 41 07 00"))
	df, err := lf.Decode()
	require.NoError(t, err)
	require.Len(t, df.DataRecords, 1)
	assert.Equal(t, "ABC", df.DataRecords[0].Unit.Unit)
	assert.InDelta(t, 7.0, df.DataRecords[0].RawValue, 0)
}

// TestDecodePlainTextVIFKeepsVIFEExponent pins that the ASCII section of a
// plain-text VIF names the unit but does not set the scale. VIF 0xFC has the
// extension bit, so a VIFE follows the ASCII and still carries the multiplier.
// Hardcoding Exp 1 reported a real Elvaco CMa10's humidity as 5410 %RH rather
// than 54.10, a silent factor of 100 that every fixture passed green.
func TestDecodePlainTextVIFKeepsVIFEExponent(t *testing.T) {
	cases := []struct {
		name      string
		payload   string
		wantUnit  string
		wantExp   float64
		wantValue float64
	}{
		// The ELV-Elvaco-CMa10 record: DIF 0x02, VIF 0xFC, LVAR 3, "%RH"
		// reversed, VIFE 0x74 (10^(4-6) = 1e-2), data 22 15 = 5410.
		{"relative humidity", "02 FC 03 48 52 25 74 22 15", "%RH", 1e-2, 54.10},
		// VIFE 0x70 is the bottom of the same range: 10^(0-6).
		{"VIFE 0x70", "02 FC 03 48 52 25 70 22 15", "%RH", 1e-6, 5410e-6},
		// VIFE 0x7D is the fixed 1000 multiplier.
		{"VIFE 0x7D", "02 FC 03 48 52 25 7D 07 00", "%RH", 1000, 7000},
		// An unknown VIFE leaves the reading unscaled rather than zeroing it.
		{"unknown VIFE", "02 FC 03 48 52 25 00 07 00", "%RH", 1, 7},
		// VIF 0x7C has no extension bit, so there is no VIFE and no scale.
		{"0x7C has no VIFE", "02 7C 03 48 52 25 07 00", "%RH", 1, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			df, err := wrapAsLongFrame(hexToBytes(tc.payload)).Decode()
			require.NoError(t, err)
			require.Len(t, df.DataRecords, 1)

			rec := df.DataRecords[0]
			assert.Equal(t, tc.wantUnit, rec.Unit.Unit, "the ASCII names the unit")
			assert.InDelta(t, tc.wantExp, rec.Unit.Exp, tc.wantExp*1e-9, "the VIFE sets the scale")
			assert.InDelta(t, tc.wantValue, rec.Value, math.Abs(tc.wantValue)*1e-9)
			assert.Equal(t, vifUnit["VARIABLE_VIF"], rec.Unit.Type)
		})
	}
}
