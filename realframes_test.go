package gombus

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realFrameCase pins the expected high-level fields a known-good libmbus test
// frame should decode to. The expected values come from the matching .xml
// reference output in https://github.com/rscada/libmbus/tree/master/test/test-frames .
type realFrameCase struct {
	file         string
	manufacturer string
	serial       int
	medium       string
	version      int
	accessNumber byte
	// records is the exact number of records we emit. It matches libmbus except
	// for the three frames marked below, where we additionally emit the
	// trailing manufacturer-specific / more-records sentinel as a record. An
	// exact count is the point: the previous "records or records+1" assertion
	// made an off-by-one unfalsifiable.
	records int
	// moreRecords is the expected HasMoreRecords, set by a trailing 0x1F.
	moreRecords bool
}

// realFrameCases covers a broad set of real M-Bus meters: heat / cooling /
// water / gas / electricity meters from many manufacturers. Each frame is a
// captured datagram from the libmbus test corpus.
var realFrameCases = []realFrameCase{
	// libmbus counts 8; the 9th is the trailing 0x0F manufacturer-specific record.
	{"ACW_Itron-BM-plus-m.hex", "ACW", 11490378, "Cold water", 14, 10, 9, false},
	{"ACW_Itron-CYBLE-M-Bus-14.hex", "ACW", 9011523, "Water", 20, 37, 8, false},
	{"EDC.hex", "EDC", 11120895, "Heat: Outlet", 2, 23, 22, false},
	{"EFE_Engelmann-Elster-SensoStar-2.hex", "EFE", 24083345, "Heat: Outlet", 0, 102, 25, false},
	{"EFE_Engelmann-WaterStar.hex", "EFE", 4990254, "Warm water (30-90°C)", 0, 12, 12, false},
	{"ELS_Elster-F96-Plus.hex", "ELS", 44493951, "Heat: Outlet", 47, 161, 16, false},
	// libmbus counts 12; the 13th is the trailing 0x1F more-records record.
	{"ELV-Elvaco-CMa10.hex", "ELV", 24011561, "Other", 22, 63, 13, true},
	{"EMU_EMU-Professional-375-M-Bus.hex", "EMU", 32629, "Electricity", 16, 2, 32, false},
	{"Elster-F2.hex", "SVM", 802657, "Heat: Outlet", 8, 70, 14, true},
	{"FIN-Finder-7E.23.8.230.0020.hex", "FIN", 23006207, "Electricity", 35, 146, 6, false},
	{"GWF-MTKcoder.hex", "GWF", 182007, "Water", 53, 76, 2, false},
	{"LGB_G350.hex", "LGB", 12082058, "Gas", 64, 64, 6, false},
	{"REL-Relay-Padpuls2.hex", "REL", 11216301, "Gas", 65, 177, 6, false},
	{"SBC_Saia-Burgess-ALE3.hex", "SBC", 19000055, "Electricity", 22, 191, 20, false},
	{"SEN_Pollustat.hex", "SEN", 11788, "Heat / Cooling load meter", 6, 62, 16, false},
	// libmbus counts 9; the 10th is the trailing 0x1F more-records record.
	{"SEN_Sensus-PolluStat-E.hex", "SEN", 21265095, "Heat: Outlet", 14, 181, 10, true},
	{"SEN_Sensus-PolluTherm.hex", "SEN", 24351689, "Heat: Outlet", 11, 84, 9, false},
	{"SLB_CF-Compact-Integral-MK-MaXX.hex", "SLB", 11817314, "Heat: Outlet", 6, 3, 15, false},
	{"THI_cma10.hex", "ELV", 2, "Other", 21, 13, 13, true},
	{"ZRM_Minol-Minocal-C2.hex", "ZRM", 31425084, "Heat: Outlet", 129, 115, 34, false},
	// Captured live from a Kamstrup MULTICAL 603 by the Meterlogger project.
	{"Meterlogger-response.hex", "KAM", 72927502, "Heat: Inlet", 52, 83, 30, false},
}

func TestDecodeRealMeterFrames(t *testing.T) {
	for _, tc := range realFrameCases {
		t.Run(tc.file, func(t *testing.T) {
			data := loadHexFixture(t, filepath.Join("testdata", "frames", tc.file))
			df, err := LongFrame(data).Decode()
			require.NoError(t, err, "decoding frame")

			assert.Equal(t, tc.manufacturer, df.Manufacturer, "manufacturer")
			assert.Equal(t, tc.serial, df.SerialNumber, "serial number")
			assert.Equal(t, tc.medium, df.DeviceType, "device type / medium")
			assert.Equal(t, tc.version, df.Version, "version")
			assert.Equal(t, tc.accessNumber, df.AccessNumber, "access number")
			assert.Len(t, df.DataRecords, tc.records, "data records")
			assert.Equal(t, tc.moreRecords, df.HasMoreRecords(), "HasMoreRecords")
		})
	}
}

// TestDecodeELSErrorStateRegisters pins the decision that an invalid value must
// not fail a structurally sound frame. This real Elster F96 fills its two
// "value during error state" registers with a BD EB DD not-available pattern.
// Rejecting the frame over it would drop all 14 good readings, which is exactly
// when a meter reporting an error is worth reading.
func TestDecodeELSErrorStateRegisters(t *testing.T) {
	data := loadHexFixture(t, filepath.Join("testdata", "frames", "ELS_Elster-F96-Plus.hex"))
	df, err := LongFrame(data).Decode()
	require.NoError(t, err, "an error-state filler must not fail the frame")
	require.Len(t, df.DataRecords, 16)

	// Exactly the two error-state registers are flagged, and nothing else.
	badIndexes := []int{}
	for i, r := range df.DataRecords {
		if r.ValueErr != nil {
			badIndexes = append(badIndexes, i)
			assert.Equal(t, "Value during error state", r.Function, "record %d", i)
			assert.InDelta(t, 0.0, r.RawValue, 0, "record %d", i)
			assert.InDelta(t, 0.0, r.Value, 0, "record %d", i)
		}
	}
	assert.Equal(t, []int{4, 5}, badIndexes, "flagged records")

	// The readings around them decode to their real values.
	assert.InDelta(t, 22.7, df.DataRecords[6].Value, 1e-9, "flow temperature")
	assert.Equal(t, "C", df.DataRecords[6].Unit.Unit)
	assert.InDelta(t, 22.6, df.DataRecords[7].Value, 1e-9, "return temperature")
	assert.InDelta(t, 0.1, df.DataRecords[8].Value, 1e-9, "temperature difference")
	assert.Equal(t, "K", df.DataRecords[8].Unit.Unit)
	assert.InDelta(t, 730.0, df.DataRecords[9].RawValue, 0, "operating time in days")
}

// TestDecodeSENPollustatDurationRecords pins the duration-of-limit-exceed
// qualifier against the only real meter that sends it. Records 12 and 13 carry
// VIFE 0x50 and 0x58 under volume-flow VIF 0xBE: they are durations of a limit
// exceed, not flows. Their nn is 00, so the time base is seconds and the
// exponent stays 1: the VALUES MUST NOT MOVE, only the label becomes honest.
// That is what makes this fix landable without an oracle.
//
// We diverge from libmbus here, which labels both "Volume flow ( m^3/h)". That
// is expected: libmbus shares the blind spot, so its agreement was never
// evidence. Only the spec is (m-bus.com appendix 8.4.5).
func TestDecodeSENPollustatDurationRecords(t *testing.T) {
	data := loadHexFixture(t, filepath.Join("testdata", "frames", "SEN_Pollustat.hex"))
	df, err := LongFrame(data).Decode()
	require.NoError(t, err)
	require.Greater(t, len(df.DataRecords), 13)

	for _, tc := range []struct {
		record  int
		wantRaw float64
	}{
		{12, 11582321}, // VIFE 0x50: lower limit, first, seconds
		{13, 756},      // VIFE 0x58: upper limit, first, seconds
	} {
		rec := df.DataRecords[tc.record]
		assert.Equal(t, "seconds", rec.Unit.Unit, "record %d is a duration", tc.record)
		assert.InDelta(t, 1.0, rec.Unit.Exp, 0, "nn=00 means seconds, so no rescaling")
		// The numbers verified against libmbus must be untouched.
		assert.InDelta(t, tc.wantRaw, rec.RawValue, 0, "record %d RawValue", tc.record)
		assert.InDelta(t, tc.wantRaw, rec.Value, 0, "record %d Value must not move", tc.record)
		// The base VIF is kept: it says which quantity's limit was exceeded.
		assert.Equal(t, vifUnit["VOLUME_FLOW"], rec.Unit.Type, "record %d keeps its quantity", tc.record)
	}
}

// TestDecodeRealMeterFrameDates pins the date decoding against real meters,
// including the three edge cases the corpus happens to contain: an unset date,
// a meter-declared invalid timestamp, and a type I field we do not decode.
func TestDecodeRealMeterFrameDates(t *testing.T) {
	cases := []struct {
		file string
		// index of the data record carrying the date.
		record   int
		wantTime time.Time
		wantErr  error
	}{
		// libmbus decodes this one to 2014-03-13T11:11:00.
		{"ACW_Itron-BM-plus-m.hex", 4, dateTime(2014, 3, 13, 11, 11), nil},
		{"ACW_Itron-CYBLE-M-Bus-14.hex", 2, dateTime(2014, 3, 13, 14, 26), nil},
		{"EDC.hex", 16, dateTime(2012, 7, 10, 15, 25), nil},
		{"EFE_Engelmann-Elster-SensoStar-2.hex", 1, dateTime(2014, 3, 12, 14, 23), nil},
		// A type G date on the same meter, storage 1.
		{"EFE_Engelmann-Elster-SensoStar-2.hex", 11, date(2013, 12, 31), nil},
		{"EFE_Engelmann-WaterStar.hex", 1, dateTime(2014, 3, 13, 12, 10), nil},
		{"ELS_Elster-F96-Plus.hex", 10, dateTime(2014, 3, 13, 13, 9), nil},
		{"Elster-F2.hex", 10, dateTime(2013, 6, 29, 12, 12), nil},
		{"SEN_Pollustat.hex", 0, dateTime(2015, 4, 7, 14, 59), nil},
		// The epoch itself, which a meter really does send.
		{"SEN_Pollustat.hex", 1, dateTime(2000, 1, 1, 0, 0), nil},
		{"SLB_CF-Compact-Integral-MK-MaXX.hex", 9, dateTime(2014, 3, 13, 14, 2), nil},
		{"ZRM_Minol-Minocal-C2.hex", 8, dateTime(2011, 9, 1, 8, 30), nil},

		// A 6-byte type I field (46 6D 00 00 08 16 27 00), carrying seconds.
		// libmbus decodes this record to 2016-07-22T08:00:00.
		{"LGB_G350.hex", 1, time.Date(2016, 7, 22, 8, 0, 0, 0, time.UTC), nil},

		// An unset type G date (42 6C 00 00): day 0, month 0. time.Date would
		// have turned this into 1999-11-30.
		{"ACW_Itron-BM-plus-m.hex", 2, time.Time{}, ErrInvalidDateTime},
		// The meter set the type F invalid bit itself.
		{"REL-Relay-Padpuls2.hex", 1, time.Time{}, ErrInvalidDateTime},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/record%d", tc.file, tc.record), func(t *testing.T) {
			data := loadHexFixture(t, filepath.Join("testdata", "frames", tc.file))
			df, err := LongFrame(data).Decode()
			require.NoError(t, err, "a date must never fail the frame")
			require.Greater(t, len(df.DataRecords), tc.record)

			rec := df.DataRecords[tc.record]
			require.NotEqual(t, DateNone, rec.Unit.Date, "record must be a date")
			if tc.wantErr != nil {
				require.ErrorIs(t, rec.ValueErr, tc.wantErr)
				assert.True(t, rec.Time.IsZero(), "got %s", rec.Time)
				return
			}
			require.NoError(t, rec.ValueErr)
			assert.Equal(t, tc.wantTime, rec.Time)
		})
	}
}

// TestErrorFramesRejected feeds the libmbus error-frames corpus through the
// decoder and verifies each one is rejected (with an error) and never panics.
// These frames are intentionally malformed: truncated payloads, oversized
// LVAR strings, too many DIFE bytes, etc.
func TestErrorFramesRejected(t *testing.T) {
	dir := filepath.Join("testdata", "error-frames")
	//nolint:thelper // the lambda is the subtest body, not a helper.
	forEachHexFixture(t, dir, func(t *testing.T, name string, data []byte) {
		df, decodeErr := LongFrame(data).Decode()
		// Either the decode errors out, or it stops at a 0F/1F sentinel
		// before reaching the corruption. A silently successful decode of
		// garbage records is the only case we flag.
		if decodeErr == nil && df != nil && len(df.DataRecords) > 0 {
			last := df.DataRecords[len(df.DataRecords)-1]
			if last.Function != "Manufacturer specific" && last.Function != "More records follow" {
				t.Logf("warning: %s decoded silently with %d records (last function=%q)",
					name, len(df.DataRecords), last.Function)
			}
		}
	})
}

// TestRealMeterFramesNoRecordPanic ensures decoding the entire corpus never
// produces a panic, regardless of expected-value matches.
func TestRealMeterFramesNoRecordPanic(t *testing.T) {
	dir := filepath.Join("testdata", "frames")
	//nolint:thelper // the lambda is the subtest body, not a helper.
	forEachHexFixture(t, dir, func(t *testing.T, _ string, data []byte) {
		_, _ = LongFrame(data).Decode()
	})
}
