package gombus

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestFind(t *testing.T) {
	df := DecodedFrame{DataRecords: []DecodedDataRecord{
		{Unit: Unit{Type: VIFEnergyWh}, Function: FunctionInstantaneous, StorageNumber: 1, Value: 1},
		{Unit: Unit{Type: VIFEnergyWh}, Function: FunctionInstantaneous, Tariff: 1, Value: 2},
		{Unit: Unit{Type: VIFEnergyWh}, Function: FunctionInstantaneous, Value: 42},
	}}

	r, ok := df.Find(MatchType(VIFEnergyWh), MatchStorage(1))
	if !ok || r.Value != 1 {
		t.Fatalf("Find(storage 1) = %v, %v; want 1", r.Value, ok)
	}

	r, ok = df.Find(MatchType(VIFEnergyWh), MatchTariff(1))
	if !ok || r.Value != 2 {
		t.Fatalf("Find(tariff 1) = %v, %v; want 2", r.Value, ok)
	}

	if _, ok := df.Find(MatchType(VIFEnergyWh), MatchDevice(3)); ok {
		t.Fatal("Find(device 3) should report false")
	}

	// A custom closure composes with the provided matchers.
	r, ok = df.Find(func(r DecodedDataRecord) bool { return r.Value > 10 })
	if !ok || r.Value != 42 {
		t.Fatalf("Find(custom) = %v, %v; want 42", r.Value, ok)
	}
}

func TestFindAll(t *testing.T) {
	df := DecodedFrame{DataRecords: []DecodedDataRecord{
		{Unit: Unit{Type: VIFEnergyWh}, Function: FunctionInstantaneous, Value: 1},
		{Unit: Unit{Type: VIFPowerW}, Function: FunctionInstantaneous, Value: 2},
		{Unit: Unit{Type: VIFEnergyWh}, Function: FunctionMaximum, StorageNumber: 1, Value: 3},
	}}

	if got := df.FindAll(); len(got) != 3 {
		t.Fatalf("FindAll() = %d records; want all 3", len(got))
	}

	got := df.FindAll(MatchType(VIFEnergyWh))
	if len(got) != 2 || got[0].Value != 1 || got[1].Value != 3 {
		t.Fatalf("FindAll(energy) = %v; want the two energy records in frame order", got)
	}

	if got := df.FindAll(MatchType(VIFVolume)); got != nil {
		t.Fatalf("FindAll for an absent type = %v; want nil", got)
	}
}

func TestRecord(t *testing.T) {
	df := DecodedFrame{DataRecords: []DecodedDataRecord{
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionInstantaneous, StorageNumber: 1, Value: 1},
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionInstantaneous, Device: 2, Value: 2},
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionInstantaneous, Tariff: 1, Value: 3},
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionMaximum, Value: 4},
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionInstantaneous, Value: 42},
	}}

	r, ok := df.Record(VIFEnergyJoule, FunctionInstantaneous)
	if !ok || r.Value != 42 {
		t.Fatalf("Record = %v, %v; want value 42 (current record, not storage/device/tariff/max variants)", r.Value, ok)
	}

	if _, ok := df.Record(VIFPowerW, FunctionInstantaneous); ok {
		t.Fatal("Record should report false for a missing type")
	}
}

func TestRecords(t *testing.T) {
	df := DecodedFrame{DataRecords: []DecodedDataRecord{
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionInstantaneous, Value: 1},
		{Unit: Unit{Type: VIFPowerW}, Function: FunctionInstantaneous, Value: 2},
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionMaximum, Tariff: 2, Value: 3},
	}}

	got := df.Records(VIFEnergyJoule)
	if len(got) != 2 || got[0].Value != 1 || got[1].Value != 3 {
		t.Fatalf("Records = %v; want both energy records in frame order", got)
	}
}

func TestValue(t *testing.T) {
	badBCD := errors.New("invalid BCD")
	df := DecodedFrame{DataRecords: []DecodedDataRecord{
		{Unit: Unit{Type: VIFEnergyJoule}, Function: FunctionInstantaneous, Value: 42},
		{Unit: Unit{Type: VIFPowerW}, Function: FunctionInstantaneous, ValueErr: badBCD},
	}}

	v, err := df.Value(VIFEnergyJoule, FunctionInstantaneous)
	if err != nil || v != 42 {
		t.Fatalf("Value = %v, %v; want 42, nil", v, err)
	}

	if _, err := df.Value(VIFVolume, FunctionInstantaneous); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("Value for a missing record: err = %v; want ErrNoRecord", err)
	}

	if _, err := df.Value(VIFPowerW, FunctionInstantaneous); !errors.Is(err, badBCD) {
		t.Fatalf("Value for a record with ValueErr: err = %v; want the record's ValueErr", err)
	}
}

func TestTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 30, 0, 0, time.UTC)
	billing := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	df := DecodedFrame{DataRecords: []DecodedDataRecord{
		{Unit: Unit{Type: VIFEnergyWh}, Function: FunctionInstantaneous, Value: 1},
		{Unit: Unit{Date: DateTypeG}, Function: FunctionInstantaneous, StorageNumber: 1, Time: billing},
		{Unit: Unit{Date: DateTypeF}, Function: FunctionInstantaneous, ValueErr: errors.New("invalid date")},
		{Unit: Unit{Date: DateTypeF}, Function: FunctionInstantaneous, Time: now},
	}}

	ts, ok := df.Timestamp()
	if !ok || !ts.Equal(now) {
		t.Fatalf("Timestamp = %v, %v; want %v (current record, skipping historic and undecodable ones)", ts, ok, now)
	}

	if _, ok := (DecodedFrame{}).Timestamp(); ok {
		t.Fatal("Timestamp on a frame without date records should report false")
	}
}

// The WaterStar sends an error-flags record (01 FD 17). Before extension
// types were offset into their own namespace it decoded with the same
// Unit.Type as a volume record, so MatchType(VIFVolume) returned it.
func TestExtensionTypesDoNotCollideWithPrimary(t *testing.T) {
	data := loadHexFixture(t, filepath.Join("testdata", "frames", "EFE_Engelmann-WaterStar.hex"))
	df, err := LongFrame(data).Decode()
	if err != nil {
		t.Fatal(err)
	}

	flags := df.FindAll(MatchType(VIFExtErrorFlags))
	if len(flags) != 1 {
		t.Fatalf("MatchType(VIFExtErrorFlags) matched %d records; want 1", len(flags))
	}

	for _, r := range df.FindAll(MatchType(VIFVolume)) {
		if r.Unit.Type == VIFExtErrorFlags || r.Unit.Unit == "none" {
			t.Fatalf("volume query returned a non-volume record: %+v", r)
		}
	}
}
