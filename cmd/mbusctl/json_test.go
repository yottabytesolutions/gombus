package main

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/yottabytesolutions/gombus"
)

// syntheticFrame carries the three record shapes the schema has to cover: a
// plain reading, a date, and a value the meter sent but that does not decode.
func syntheticFrame() *gombus.DecodedFrame {
	return &gombus.DecodedFrame{
		SerialNumber: 12345678,
		Manufacturer: "KAM",
		ProductName:  "MULTICAL",
		Version:      16,
		DeviceType:   "Heat",
		AccessNumber: 42,
		Status:       0x10,
		DataRecords: []gombus.DecodedDataRecord{
			{
				Function:      "Instantaneous value",
				StorageNumber: 0,
				Tariff:        0,
				Device:        0,
				Unit:          gombus.Unit{Unit: "Wh", Type: gombus.VIFEnergyWh, Exp: 1},
				Value:         1234.5,
				RawValue:      12345,
				ValueString:   "1234.5",
			},
			{
				Function:      "Instantaneous value",
				StorageNumber: 1,
				Tariff:        2,
				Device:        3,
				// 0x6D is the date/time VIF; the package exports no constant
				// for it.
				Unit:     gombus.Unit{Unit: "date time", Type: 0x6D, Date: gombus.DateTypeF},
				RawValue: 1234,
				Time:     time.Date(2024, time.March, 1, 12, 30, 0, 0, time.UTC),
			},
			{
				Function: "Maximum value",
				Unit:     gombus.Unit{Unit: "m^3", Type: gombus.VIFVolume},
				ValueErr: errors.New("invalid BCD digit"),
			},
		},
	}
}

// TestJSONFrameSchema pins the machine interface: field names, the omitted
// fields, and that a value error is reported instead of a silent zero.
func TestJSONFrameSchema(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, newJSONFrame(syntheticFrame())); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	want := `{
  "serial_number": 12345678,
  "manufacturer": "KAM",
  "product_name": "MULTICAL",
  "version": 16,
  "device_type": "Heat",
  "access_number": 42,
  "status": 16,
  "records": [
    {
      "function": "Instantaneous value",
      "storage_number": 0,
      "tariff": 0,
      "device": 0,
      "unit": "Wh",
      "unit_type": 7,
      "value": 1234.5,
      "raw_value": 12345,
      "value_string": "1234.5"
    },
    {
      "function": "Instantaneous value",
      "storage_number": 1,
      "tariff": 2,
      "device": 3,
      "unit": "date time",
      "unit_type": 109,
      "value": 0,
      "raw_value": 1234,
      "value_string": "",
      "time": "2024-03-01T12:30:00Z"
    },
    {
      "function": "Maximum value",
      "storage_number": 0,
      "tariff": 0,
      "device": 0,
      "unit": "m^3",
      "unit_type": 23,
      "value": 0,
      "raw_value": 0,
      "value_string": "",
      "value_error": "invalid BCD digit"
    }
  ]
}
`
	if got := buf.String(); got != want {
		t.Errorf("frame JSON mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestJSONFrameNoRecords pins that records is an empty array, never null. A
// consumer should be able to range over it without a nil check.
func TestJSONFrameNoRecords(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, newJSONFrame(&gombus.DecodedFrame{Manufacturer: "ABC"})); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"records": []`)) {
		t.Errorf("empty frame must carry an empty records array, got:\n%s", buf.String())
	}
}

// TestJSONFrames pins the -all shape: an array, even for one frame.
func TestJSONFrames(t *testing.T) {
	var buf bytes.Buffer
	frames := []*gombus.DecodedFrame{syntheticFrame()}
	if err := writeJSON(&buf, newJSONFrames(frames)); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	if got := buf.Bytes(); got[0] != '[' {
		t.Errorf("multi-frame output must be an array, got:\n%s", buf.String())
	}
}

// TestJSONScanShapes pins the scan objects. The primary list must be numbers:
// encoding/json turns a []byte into a base64 string, which is why the CLI
// converts the addresses to int first.
func TestJSONScanShapes(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "primary",
			value: jsonPrimaryScan{Primary: []int{1, 5, 250}},
			want:  "{\n  \"primary\": [\n    1,\n    5,\n    250\n  ]\n}\n",
		},
		{
			name:  "primary empty",
			value: jsonPrimaryScan{Primary: []int{}},
			want:  "{\n  \"primary\": []\n}\n",
		},
		{
			name: "secondary",
			value: jsonSecondaryScan{Secondary: []jsonSecondary{
				newJSONSecondary(gombus.SecondaryAddress{
					ID: 12345678, Manufacturer: "KAM", Version: 16, Medium: 0x04,
				}),
			}},
			want: "{\n  \"secondary\": [\n    {\n      \"id\": 12345678,\n" +
				"      \"manufacturer\": \"KAM\",\n      \"version\": 16,\n" +
				"      \"medium\": 4\n    }\n  ]\n}\n",
		},
		{
			name:  "set-address",
			value: jsonSetAddress{Primary: 5},
			want:  "{\n  \"primary\": 5\n}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeJSON(&buf, tc.value); err != nil {
				t.Fatalf("writeJSON: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("JSON mismatch\ngot:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}
