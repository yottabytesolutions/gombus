package main

// JSON output schema. It is the machine interface of this tool, so treat it as
// stable: fields may be added, existing names and types may not change.
//
//	mbusctl read -json <addr>        one frame object
//	mbusctl read -json -all <addr>   an array of frame objects
//	mbusctl scan -json -primary      {"primary": [1, 5]}
//	mbusctl scan -json -secondary    {"secondary": [{"id": 12345678, ...}]}
//	mbusctl set-address -json        {"primary": 5}
//
// A frame object is:
//
//	{
//	  "serial_number": 12345678,
//	  "manufacturer": "KAM",
//	  "product_name": "",
//	  "version": 16,
//	  "device_type": "Heat",
//	  "access_number": 42,
//	  "status": 0,
//	  "records": [ ... ]
//	}
//
// A record object carries both the scaled value and the raw field value. Two
// fields are omitted when empty: value_error (set when the meter sent a value
// that does not decode, in which case value means nothing) and time (set only
// for date and date/time records, RFC3339 in the meter's own wall clock, which
// carries no zone).

import (
	"encoding/json"
	"io"
	"time"

	"github.com/yottabytesolutions/gombus"
)

type jsonFrame struct {
	SerialNumber int          `json:"serial_number"`
	Manufacturer string       `json:"manufacturer"`
	ProductName  string       `json:"product_name"`
	Version      int          `json:"version"`
	DeviceType   string       `json:"device_type"`
	AccessNumber int          `json:"access_number"`
	Status       int          `json:"status"`
	Records      []jsonRecord `json:"records"`
}

type jsonRecord struct {
	Function      string  `json:"function"`
	StorageNumber int     `json:"storage_number"`
	Tariff        int     `json:"tariff"`
	Device        int     `json:"device"`
	Unit          string  `json:"unit"`
	UnitType      int     `json:"unit_type"`
	Value         float64 `json:"value"`
	RawValue      float64 `json:"raw_value"`
	ValueString   string  `json:"value_string"`
	ValueError    string  `json:"value_error,omitempty"`
	Time          string  `json:"time,omitempty"`
}

type jsonPrimaryScan struct {
	Primary []int `json:"primary"`
}

// jsonSetAddress reports the primary address a meter now answers at.
type jsonSetAddress struct {
	Primary int `json:"primary"`
}

type jsonSecondaryScan struct {
	Secondary []jsonSecondary `json:"secondary"`
}

// jsonSecondary reports medium as the raw EN 13757-3 code. The scan only learns
// the code, not the name the decoder puts on a full frame.
type jsonSecondary struct {
	ID           uint64 `json:"id"`
	Manufacturer string `json:"manufacturer"`
	Version      int    `json:"version"`
	Medium       int    `json:"medium"`
}

func newJSONFrame(df *gombus.DecodedFrame) jsonFrame {
	records := make([]jsonRecord, 0, len(df.DataRecords))
	for _, r := range df.DataRecords {
		records = append(records, newJSONRecord(r))
	}
	return jsonFrame{
		SerialNumber: df.SerialNumber,
		Manufacturer: df.Manufacturer,
		ProductName:  df.ProductName,
		Version:      df.Version,
		DeviceType:   df.DeviceType,
		AccessNumber: int(df.AccessNumber),
		Status:       int(df.Status),
		Records:      records,
	}
}

func newJSONFrames(frames []*gombus.DecodedFrame) []jsonFrame {
	out := make([]jsonFrame, 0, len(frames))
	for _, f := range frames {
		out = append(out, newJSONFrame(f))
	}
	return out
}

func newJSONRecord(r gombus.DecodedDataRecord) jsonRecord {
	rec := jsonRecord{
		Function:      r.Function,
		StorageNumber: r.StorageNumber,
		Tariff:        r.Tariff,
		Device:        r.Device,
		Unit:          r.Unit.Unit,
		UnitType:      r.Unit.Type,
		Value:         r.Value,
		RawValue:      r.RawValue,
		ValueString:   r.ValueString,
	}
	if r.ValueErr != nil {
		rec.ValueError = r.ValueErr.Error()
	}
	if !r.Time.IsZero() {
		rec.Time = r.Time.Format(time.RFC3339)
	}
	return rec
}

func newJSONSecondary(sec gombus.SecondaryAddress) jsonSecondary {
	return jsonSecondary{
		ID:           sec.ID,
		Manufacturer: sec.Manufacturer,
		Version:      int(sec.Version),
		Medium:       int(sec.Medium),
	}
}

// writeJSON prints v as indented JSON with a trailing newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
