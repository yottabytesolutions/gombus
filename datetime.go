package gombus

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidDateTime means the meter sent a date it cannot have meant: an unset
// field, an out-of-range component, or its own invalid bit.
//
// ErrUnsupportedDateTime means the date is legal but in an encoding this package
// does not decode. The two are separate so a caller can tell a broken meter from
// a gap in gombus, which are different problems with different fixes.
//
// Both reach the caller wrapped in DecodedDataRecord.ValueErr, never as a frame
// error: an unusable date costs us no sync, so it must not reject the frame.
// Match them with errors.Is.
var (
	ErrInvalidDateTime     = errors.New("invalid date/time field")
	ErrUnsupportedDateTime = errors.New("unsupported date/time encoding")
)

// dateEpochYear is the base of the 7-bit year field. EN 13757-3 counts from
// 2000, matching libmbus (tm_year = 100 + raw, and tm_year is years since
// 1900). There is no 1900/2000 windowing rule. The year field is 7 bits, so it
// runs 2000..2127 and its most significant bit only sets from 2064 on.
const dateEpochYear = 2000

// Widths of the encodings, in bytes. VIF 0x6C is always type G. VIF 0x6D names
// the date-and-time family, and the DIF width picks the member: type J at 3
// bytes (time only), type F at 4, type I at 6.
const (
	dateTypeGWidth = 2
	dateTypeJWidth = 3
	dateTypeFWidth = 4
	dateTypeIWidth = 6
)

// decodeTypeG decodes an EN 13757-3 type G date: two bytes holding day, month
// and year, with no time of day. The year is split across both bytes.
func decodeTypeG(b []byte) (time.Time, error) {
	day := int(b[0] & 0x1F)
	month := int(b[1] & 0x0F)
	year := int((b[0]&0xE0)>>5 | (b[1]&0xF0)>>1)
	return assembleDateTime(year, month, day, 0, 0, 0)
}

// decodeTypeF decodes an EN 13757-3 type F date and time: four bytes holding
// minute through year, plus the invalid (IV) and summer-time (SU) flags. It
// reports the summer-time flag even when the timestamp is unusable, because the
// meter sent it either way.
func decodeTypeF(b []byte) (time.Time, bool, error) {
	summerTime := b[1]&0x80 != 0
	if b[0]&0x80 != 0 {
		// IV: the meter is declaring its own timestamp invalid. Believe it
		// rather than decode the bits underneath.
		return time.Time{}, summerTime, fmt.Errorf(
			"%w: meter set the invalid bit", ErrInvalidDateTime,
		)
	}
	minute := int(b[0] & 0x3F)
	hour := int(b[1] & 0x1F)
	day := int(b[2] & 0x1F)
	month := int(b[3] & 0x0F)
	year := int((b[2]&0xE0)>>5 | (b[3]&0xF0)>>1)

	t, err := assembleDateTime(year, month, day, hour, minute, 0)
	return t, summerTime, err
}

// decodeTypeI decodes an EN 13757-3 type I date and time (compound CP48): six
// bytes holding second through year. It is the same family as type F, selected
// by a 6-byte data field rather than a 4-byte one, and adds seconds.
//
// b[5] carries the day of the week and week number, which are not part of the
// timestamp and are left in RawValue for a caller that wants them.
//
// Verified against libmbus's published decoding of LGB_G350 record 1
// (2016-07-22T08:00:00), not just read off the spec.
func decodeTypeI(b []byte) (time.Time, bool, error) {
	summerTime := b[1]&0x40 != 0
	second := int(b[0] & 0x3F)
	minute := int(b[1] & 0x3F)
	hour := int(b[2] & 0x1F)
	day := int(b[3] & 0x1F)
	month := int(b[4] & 0x0F)
	year := int((b[3]&0xE0)>>5 | (b[4]&0xF0)>>1)

	t, err := assembleDateTime(year, month, day, hour, minute, second)
	return t, summerTime, err
}

// assembleDateTime validates the fields before building a time.Time, because
// time.Date normalises out-of-range input instead of rejecting it: month 0
// becomes the previous December and day 0 the last day of the prior month. An
// unset meter date would silently become a real-looking one, so every field is
// checked first and the result is verified not to have been normalised.
//
// year is the raw 7-bit field, not the calendar year.
func assembleDateTime(year, month, day, hour, minute, second int) (time.Time, error) {
	switch {
	case month < 1 || month > 12:
		return time.Time{}, fmt.Errorf("%w: month %d out of range", ErrInvalidDateTime, month)
	case day < 1:
		return time.Time{}, fmt.Errorf("%w: day %d out of range", ErrInvalidDateTime, day)
	case hour > 23:
		return time.Time{}, fmt.Errorf("%w: hour %d out of range", ErrInvalidDateTime, hour)
	case minute > 59:
		return time.Time{}, fmt.Errorf("%w: minute %d out of range", ErrInvalidDateTime, minute)
	case second > 59:
		return time.Time{}, fmt.Errorf("%w: second %d out of range", ErrInvalidDateTime, second)
	}

	t := time.Date(dateEpochYear+year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	// The day field holds up to 31, so it can still name a day the month does
	// not have. time.Date would roll 31 February forward into March; compare
	// the result against the input to catch that.
	if t.Day() != day || int(t.Month()) != month {
		return time.Time{}, fmt.Errorf(
			"%w: %04d-%02d-%02d is not a real date",
			ErrInvalidDateTime, dateEpochYear+year, month, day,
		)
	}
	return t, nil
}

// decodeRecordTime fills in Time and SummerTimeFlag for the date and date/time
// VIFs. It is additive: the raw bitfield stays in RawValue, so nothing is lost
// and a caller that wants the bits still has them.
//
// An unusable date sets ValueErr and leaves Time zero rather than reporting a
// date the meter never sent.
func decodeRecordTime(b []byte, record *DecodedDataRecord) {
	switch record.Unit.Date {
	case DateTypeG:
		// 0x6C has one width, so anything else is not a type G field.
		if len(b) != dateTypeGWidth {
			record.ValueErr = fmt.Errorf(
				"%w: %d-byte field with a type G (0x6C) VIF", ErrUnsupportedDateTime, len(b),
			)
			return
		}
		t, err := decodeTypeG(b)
		if err != nil {
			record.ValueErr = err
			return
		}
		record.Time = t
	case DateTypeF:
		decodeRecordDateTime(b, record)
	case DateTypeI, DateNone:
	}
}

// decodeRecordDateTime handles the VIF 0x6D family, where the DIF width selects
// which member of it the record carries. Reading a 6-byte type I as the type F
// we expected would silently produce a wrong timestamp, so the width chooses the
// decoder rather than being assumed.
func decodeRecordDateTime(b []byte, record *DecodedDataRecord) {
	var (
		t          time.Time
		summerTime bool
		err        error
	)
	switch len(b) {
	case dateTypeFWidth:
		t, summerTime, err = decodeTypeF(b)
	case dateTypeIWidth:
		t, summerTime, err = decodeTypeI(b)
	case dateTypeJWidth:
		// Type J is a time of day with no date. It has no sensible time.Time
		// without one, so it is reported rather than invented.
		record.ValueErr = fmt.Errorf(
			"%w: EN 13757-3 type J (time only, no date), from a %d-byte field",
			ErrUnsupportedDateTime, len(b),
		)
		return
	default:
		record.ValueErr = fmt.Errorf(
			"%w: %d-byte field with a date and time (0x6D) VIF",
			ErrUnsupportedDateTime, len(b),
		)
		return
	}

	// The meter sent the flag whether or not the timestamp is usable.
	record.SummerTimeFlag = summerTime
	if err != nil {
		record.ValueErr = err
		return
	}
	record.Time = t
}
