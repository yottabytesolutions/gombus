package gombus

import (
	"errors"
	"fmt"
	"time"
)

// ErrNoRecord is returned (wrapped) by Value when the frame carries no record
// matching the requested type and function.
var ErrNoRecord = errors.New("no matching data record")

// RecordMatch is a predicate over a decoded data record. Combine matchers in
// Find and FindAll to select records by any criteria; a custom closure works
// wherever the provided matchers fall short.
type RecordMatch func(DecodedDataRecord) bool

// MatchType matches records whose Unit.Type is the given VIF type code, for
// example VIFEnergyWh.
func MatchType(unitType int) RecordMatch {
	return func(r DecodedDataRecord) bool { return r.Unit.Type == unitType }
}

// MatchFunction matches records with the given function, for example
// FunctionInstantaneous.
func MatchFunction(function string) RecordMatch {
	return func(r DecodedDataRecord) bool { return r.Function == function }
}

// MatchStorage matches records with the given storage number. Storage 0 is
// the current value; higher numbers are historic (billing period) values.
func MatchStorage(n int) RecordMatch {
	return func(r DecodedDataRecord) bool { return r.StorageNumber == n }
}

// MatchTariff matches records with the given tariff. Tariff 0 is the total
// across tariffs.
func MatchTariff(n int) RecordMatch {
	return func(r DecodedDataRecord) bool { return r.Tariff == n }
}

// MatchDevice matches records with the given sub-device (subunit) number.
func MatchDevice(n int) RecordMatch {
	return func(r DecodedDataRecord) bool { return r.Device == n }
}

// MatchCurrent matches current-value records: storage number 0, tariff 0,
// device 0. Historic, tariff and sub-device records are excluded.
func MatchCurrent() RecordMatch {
	return func(r DecodedDataRecord) bool {
		return r.StorageNumber == 0 && r.Tariff == 0 && r.Device == 0
	}
}

// MatchTimestamp matches records whose data field is a date or date/time.
// Their decoded value is in Time, not Value.
func MatchTimestamp() RecordMatch {
	return func(r DecodedDataRecord) bool { return r.Unit.Date != DateNone }
}

// Find returns the first data record, in frame order, satisfying every given
// matcher.
func (df DecodedFrame) Find(matches ...RecordMatch) (DecodedDataRecord, bool) {
	for _, r := range df.DataRecords {
		if matchAll(r, matches) {
			return r, true
		}
	}
	return DecodedDataRecord{}, false
}

// FindAll returns every data record, in frame order, satisfying every given
// matcher. With no matchers it returns all records.
func (df DecodedFrame) FindAll(matches ...RecordMatch) []DecodedDataRecord {
	var out []DecodedDataRecord
	for _, r := range df.DataRecords {
		if matchAll(r, matches) {
			out = append(out, r)
		}
	}
	return out
}

func matchAll(r DecodedDataRecord, matches []RecordMatch) bool {
	for _, m := range matches {
		if !m(r) {
			return false
		}
	}
	return true
}

// Record returns the first current-value data record whose Unit.Type and
// Function match, for example df.Record(VIFPowerW, FunctionInstantaneous).
// Historic, tariff and sub-device records are never matched; use Find or
// FindAll with explicit matchers to reach them.
func (df DecodedFrame) Record(unitType int, function string) (DecodedDataRecord, bool) {
	return df.Find(MatchType(unitType), MatchFunction(function), MatchCurrent())
}

// Records returns every data record whose Unit.Type matches, in frame order,
// regardless of function, storage number, tariff or device.
func (df DecodedFrame) Records(unitType int) []DecodedDataRecord {
	return df.FindAll(MatchType(unitType))
}

// Value returns the scaled numeric value of the record selected by Record.
// It returns an error wrapping ErrNoRecord when no such record exists, and
// the record's ValueErr when the record is present but its value could not
// be decoded. It never reports a silent zero for either case.
func (df DecodedFrame) Value(unitType int, function string) (float64, error) {
	r, ok := df.Record(unitType, function)
	if !ok {
		return 0, fmt.Errorf("%w: type 0x%02x, function %q", ErrNoRecord, unitType, function)
	}
	if r.ValueErr != nil {
		return 0, r.ValueErr
	}
	return r.Value, nil
}

// Timestamp returns the meter's current date/time reading: the first
// current-value timestamp record whose value decoded. See the Time field of
// DecodedDataRecord for what this wall-clock value does and does not mean.
func (df DecodedFrame) Timestamp() (time.Time, bool) {
	r, ok := df.Find(MatchTimestamp(), MatchCurrent(),
		func(r DecodedDataRecord) bool { return r.ValueErr == nil })
	return r.Time, ok
}
