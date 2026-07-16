package gombus

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeTypeG covers the 16-bit date, including the year boundary that the
// Raw flag exists to protect: raw 64 sets the field's most significant bit, so
// any sign extension leaking in would show up here as a wrong year.
func TestDecodeTypeG(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want time.Time
	}{
		// day = b0 & 0x1F, month = b1 & 0x0F,
		// year = (b0 & 0xE0) >> 5 | (b1 & 0xF0) >> 1.
		{"2014-03-13", []byte{0xCD, 0x13}, date(2014, 3, 13)},
		{"epoch 2000-01-01", []byte{0x01, 0x01}, date(2000, 1, 1)},
		{"2013-12-31", []byte{0xBF, 0x1C}, date(2013, 12, 31)},
		// Year 63 is the last year whose MSB is clear, 64 the first with it set.
		{"year 63 boundary", []byte{0xE1, 0x71}, date(2063, 1, 1)},
		{"year 64 MSB set", []byte{0x01, 0x01 | 0x80}, date(2064, 1, 1)},
		{"year 127 all bits", []byte{0xE1, 0xF1}, date(2127, 1, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeTypeG(tc.b)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDecodeTypeF covers the 32-bit date and time.
func TestDecodeTypeF(t *testing.T) {
	cases := []struct {
		name           string
		b              []byte
		want           time.Time
		wantSummerTime bool
	}{
		{"2014-03-13 11:11", []byte{0x0B, 0x0B, 0xCD, 0x13}, dateTime(2014, 3, 13, 11, 11), false},
		{"2014-03-13 14:26", []byte{0x1A, 0x0E, 0xCD, 0x13}, dateTime(2014, 3, 13, 14, 26), false},
		{"epoch 2000-01-01 00:00", []byte{0x00, 0x00, 0x01, 0x01}, dateTime(2000, 1, 1, 0, 0), false},
		{"end of day 23:59", []byte{0x3B, 0x17, 0x01, 0x01}, dateTime(2000, 1, 1, 23, 59), false},
		// SU is bit 7 of b1 and must not bleed into the hour.
		{"summer time flag", []byte{0x0B, 0x0B | 0x80, 0xCD, 0x13}, dateTime(2014, 3, 13, 11, 11), true},
		{"year 64 MSB set", []byte{0x00, 0x00, 0x01, 0x01 | 0x80}, dateTime(2064, 1, 1, 0, 0), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, summerTime, err := decodeTypeF(tc.b)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantSummerTime, summerTime)
		})
	}
}

// TestDecodeTypeI covers the 48-bit compound date and time. The layout is
// verified against libmbus's published decoding of LGB_G350 record 1 rather
// than read off the spec alone.
func TestDecodeTypeI(t *testing.T) {
	cases := []struct {
		name           string
		b              []byte
		want           time.Time
		wantSummerTime bool
	}{
		// The LGB_G350 fixture: libmbus renders it 2016-07-22T08:00:00.
		{"LGB_G350 record", []byte{0x00, 0x00, 0x08, 0x16, 0x27, 0x00}, dateTimeSec(2016, 7, 22, 8, 0, 0), false},
		// Seconds are the field type F does not have.
		{"seconds", []byte{0x2D, 0x00, 0x08, 0x16, 0x27, 0x00}, dateTimeSec(2016, 7, 22, 8, 0, 45), false},
		{"end of minute", []byte{0x3B, 0x00, 0x08, 0x16, 0x27, 0x00}, dateTimeSec(2016, 7, 22, 8, 0, 59), false},
		{"epoch", []byte{0x00, 0x00, 0x00, 0x01, 0x01, 0x00}, dateTimeSec(2000, 1, 1, 0, 0, 0), false},
		// DST is bit 6 of b1 and must not bleed into the minute.
		{"summer time", []byte{0x00, 0x40, 0x08, 0x16, 0x27, 0x00}, dateTimeSec(2016, 7, 22, 8, 0, 0), true},
		{"summer time with minutes", []byte{0x00, 0x0B | 0x40, 0x08, 0x16, 0x27, 0x00}, dateTimeSec(2016, 7, 22, 8, 11, 0), true},
		// b5 is the week day / week number and is not part of the timestamp.
		{"week day byte ignored", []byte{0x00, 0x00, 0x08, 0x16, 0x27, 0xFF}, dateTimeSec(2016, 7, 22, 8, 0, 0), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, summerTime, err := decodeTypeI(tc.b)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantSummerTime, summerTime)
		})
	}
}

func TestDecodeTypeIRejectsUnrealDates(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
	}{
		{"unset day and month", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"day 0", []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x00}},
		{"month 0", []byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x00}},
		{"month 13", []byte{0x00, 0x00, 0x00, 0x01, 0x0D, 0x00}},
		{"31 February", []byte{0x00, 0x00, 0x00, 0x1F, 0x02, 0x00}},
		// second and minute are 6 bits, hour 5, so all three can overflow.
		{"second 60", []byte{0x3C, 0x00, 0x00, 0x01, 0x01, 0x00}},
		{"second 63", []byte{0x3F, 0x00, 0x00, 0x01, 0x01, 0x00}},
		{"minute 60", []byte{0x00, 0x3C, 0x00, 0x01, 0x01, 0x00}},
		{"hour 24", []byte{0x00, 0x00, 0x18, 0x01, 0x01, 0x00}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := decodeTypeI(tc.b)
			require.ErrorIs(t, err, ErrInvalidDateTime)
			assert.True(t, got.IsZero(), "must not normalise, got %s", got)
		})
	}
}

// TestDecodeDateTimeRejectsUnrealDates is the point of the whole feature.
// time.Date normalises rather than rejects: month 0 rolls back to the previous
// December, day 0 to the last day of the prior month, 31 February forward into
// March. Meters really do send unset dates, so normalising would invent a
// plausible timestamp the meter never sent.
func TestDecodeDateTimeRejectsUnrealDates(t *testing.T) {
	t.Run("type G", func(t *testing.T) {
		cases := []struct {
			name string
			b    []byte
		}{
			// The all-zero field a meter sends for "never set". time.Date would
			// render this 1999-11-30.
			{"unset day and month", []byte{0x00, 0x00}},
			{"day 0", []byte{0x00, 0x01}},
			{"month 0", []byte{0x01, 0x00}},
			{"month 13", []byte{0x01, 0x0D}},
			{"month 15", []byte{0x01, 0x0F}},
			{"31 February", []byte{0x1F, 0x02}},
			{"31 April", []byte{0x1F, 0x04}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := decodeTypeG(tc.b)
				require.ErrorIs(t, err, ErrInvalidDateTime)
				assert.True(t, got.IsZero(), "must not normalise to a real-looking date, got %s", got)
			})
		}
	})

	t.Run("type F", func(t *testing.T) {
		cases := []struct {
			name string
			b    []byte
		}{
			{"unset day and month", []byte{0x00, 0x00, 0x00, 0x00}},
			{"day 0", []byte{0x00, 0x00, 0x00, 0x01}},
			{"month 0", []byte{0x00, 0x00, 0x01, 0x00}},
			{"month 13", []byte{0x00, 0x00, 0x01, 0x0D}},
			{"31 February", []byte{0x00, 0x00, 0x1F, 0x02}},
			// hour is 5 bits and minute 6, so both can exceed the clock.
			{"hour 24", []byte{0x00, 0x18, 0x01, 0x01}},
			{"hour 31", []byte{0x00, 0x1F, 0x01, 0x01}},
			{"minute 60", []byte{0x3C, 0x00, 0x01, 0x01}},
			{"minute 63", []byte{0x3F, 0x00, 0x01, 0x01}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, _, err := decodeTypeF(tc.b)
				require.ErrorIs(t, err, ErrInvalidDateTime)
				assert.True(t, got.IsZero(), "must not normalise to a real-looking date, got %s", got)
			})
		}
	})
}

// TestDecodeTypeFInvalidBit pins that the meter's own IV bit is believed rather
// than decoded around, and that the summer-time flag is still reported.
func TestDecodeTypeFInvalidBit(t *testing.T) {
	cases := []struct {
		name           string
		b              []byte
		wantSummerTime bool
	}{
		// The bytes underneath are a perfectly valid 2014-03-13 11:11.
		{"IV set over a valid date", []byte{0x0B | 0x80, 0x0B, 0xCD, 0x13}, false},
		{"IV set with summer time", []byte{0x0B | 0x80, 0x0B | 0x80, 0xCD, 0x13}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, summerTime, err := decodeTypeF(tc.b)
			require.ErrorIs(t, err, ErrInvalidDateTime)
			assert.True(t, got.IsZero(), "the meter says this timestamp is invalid")
			assert.Equal(t, tc.wantSummerTime, summerTime, "the SU bit is reported either way")
		})
	}
}

// TestDecodeRecordTimeInFrame pins the end-to-end behaviour through Decode: a
// date record carries both Time and its raw bits, and a bad date sets ValueErr
// without failing the frame or losing the records around it.
func TestDecodeRecordTimeInFrame(t *testing.T) {
	cases := []struct {
		name           string
		payload        string
		wantTime       time.Time
		wantRaw        float64
		wantErr        error
		wantSummerTime bool
	}{
		// DIF 0x04 (32-bit) + VIF 0x6D (type F) + 0B 0B CD 13.
		{
			name:     "type F",
			payload:  "04 6D 0B 0B CD 13",
			wantTime: dateTime(2014, 3, 13, 11, 11),
			wantRaw:  0x13CD0B0B,
		},
		// DIF 0x02 (16-bit) + VIF 0x6C (type G) + CD 13.
		{
			name:     "type G",
			payload:  "02 6C CD 13",
			wantTime: date(2014, 3, 13),
			wantRaw:  0x13CD,
		},
		{
			name:           "type F summer time",
			payload:        "04 6D 0B 8B CD 13",
			wantTime:       dateTime(2014, 3, 13, 11, 11),
			wantRaw:        0x13CD8B0B,
			wantSummerTime: true,
		},
		{
			name:    "type G unset date",
			payload: "02 6C 00 00",
			wantRaw: 0,
			wantErr: ErrInvalidDateTime,
		},
		{
			name:    "type F invalid bit",
			payload: "04 6D 8B 0B CD 13",
			wantRaw: 0x13CD0B8B,
			wantErr: ErrInvalidDateTime,
		},
		// The DIF width, not the VIF, picks type I over type F.
		{
			name:     "type I selected by width",
			payload:  "06 6D 00 00 08 16 27 00",
			wantTime: dateTimeSec(2016, 7, 22, 8, 0, 0),
			wantRaw:  0x002716080000,
		},
		// 0x6D in three bytes is type J, a time with no date.
		{
			name:    "type J is unsupported not invalid",
			payload: "03 6D 00 00 08",
			wantRaw: 0x080000,
			wantErr: ErrUnsupportedDateTime,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			df, err := wrapAsLongFrame(hexToBytes(tc.payload)).Decode()
			require.NoError(t, err, "a date must never fail the frame")
			require.Len(t, df.DataRecords, 1)

			rec := df.DataRecords[0]
			if tc.wantErr != nil {
				require.ErrorIs(t, rec.ValueErr, tc.wantErr)
				assert.True(t, rec.Time.IsZero(), "Time must stay zero when ValueErr is set")
			} else {
				require.NoError(t, rec.ValueErr)
				assert.Equal(t, tc.wantTime, rec.Time)
			}
			assert.Equal(t, tc.wantSummerTime, rec.SummerTimeFlag)
			// Time is additive: the raw bitfield survives either way, and is
			// never sign-extended.
			assert.InDelta(t, tc.wantRaw, rec.RawValue, 0, "raw bits must be preserved")
		})
	}
}

// TestDecodeBadDateDoesNotAbortFrame pairs with the truncation tests: an
// unusable date must not cost the readings around it.
func TestDecodeBadDateDoesNotAbortFrame(t *testing.T) {
	// A good reading, an unset type G date, then another good reading.
	df, err := wrapAsLongFrame(hexToBytes("01 13 07 02 6C 00 00 01 13 09")).Decode()
	require.NoError(t, err)
	require.Len(t, df.DataRecords, 3)

	require.ErrorIs(t, df.DataRecords[1].ValueErr, ErrInvalidDateTime)
	assert.True(t, df.DataRecords[1].Time.IsZero())

	require.NoError(t, df.DataRecords[0].ValueErr)
	require.NoError(t, df.DataRecords[2].ValueErr)
	assert.InDelta(t, 7.0, df.DataRecords[0].RawValue, 0)
	assert.InDelta(t, 9.0, df.DataRecords[2].RawValue, 0)
}

// TestNonDateRecordHasNoTime pins that ordinary readings are untouched.
func TestNonDateRecordHasNoTime(t *testing.T) {
	df, err := wrapAsLongFrame(hexToBytes("02 5A D4 09")).Decode()
	require.NoError(t, err)
	require.Len(t, df.DataRecords, 1)
	assert.Equal(t, DateNone, df.DataRecords[0].Unit.Date)
	assert.True(t, df.DataRecords[0].Time.IsZero())
	assert.False(t, df.DataRecords[0].SummerTimeFlag)
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func dateTime(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}

func dateTimeSec(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}
