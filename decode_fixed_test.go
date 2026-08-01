package gombus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedFrameParts are the fields of a fixed data response, in frame order.
// The two counters are already in transmission order (LSB first).
type fixedFrameParts struct {
	id       [4]byte
	access   byte
	status   byte
	unit1    byte
	unit2    byte
	counter1 [4]byte
	counter2 [4]byte
}

// buildFixedFrame assembles a complete CI=0x73 long frame with a valid length
// and checksum, so the tests exercise the same bytes a meter would send.
func buildFixedFrame(p fixedFrameParts) LongFrame {
	// 25 bytes: 4 start bytes, C/A/CI, the 16-byte fixed structure, checksum
	// and stop byte. The structure has no optional or repeating part.
	lf := make(LongFrame, 0, 25)
	lf = append(lf,
		0x68, 0x00, 0x00, 0x68, // start, L, L, start
		0x08, // C: RSP_UD
		0x01, // A
		0x73, // CI: fixed data response
	)
	lf = append(lf, p.id[:]...)
	lf = append(lf, p.access, p.status, p.unit1, p.unit2)
	lf = append(lf, p.counter1[:]...)
	lf = append(lf, p.counter2[:]...)
	lf = append(lf, 0x00, 0x16) // checksum, stop
	lf.SetLength()
	lf.SetChecksum()
	return lf
}

// mediumUnitByte packs a 6-bit unit code together with the two medium bits the
// unit byte carries. mediumHalf is the half of the 4-bit medium code this byte
// holds: the low half in unit byte 1, the high half in unit byte 2.
func mediumUnitByte(unitCode, mediumHalf byte) byte {
	return mediumHalf<<6 | unitCode&fixedUnitCodeMask
}

func TestDecodeFixedCounters(t *testing.T) {
	tests := []struct {
		name       string
		parts      fixedFrameParts
		wantSerial int
		wantDevice string
		wantUnits  [2]string
		wantValues [2]float64
	}{
		{
			// Electricity meter, BCD counters: 12345678 kWh and 4321 l.
			name: "BCD counters, energy and volume",
			parts: fixedFrameParts{
				id:       [4]byte{0x78, 0x56, 0x34, 0x12},
				access:   0x2A,
				status:   0x00, // bit 0 clear: BCD
				unit1:    mediumUnitByte(0x05, 0x02),
				unit2:    mediumUnitByte(0x29, 0x00),
				counter1: [4]byte{0x78, 0x56, 0x34, 0x12},
				counter2: [4]byte{0x21, 0x43, 0x00, 0x00},
			},
			wantSerial: 12345678,
			wantDevice: "Electricity",
			wantUnits:  [2]string{"WH", "m^3"},
			wantValues: [2]float64{12345678 * 1.0e3, 4.321},
		},
		{
			// Same meter reading sent binary: status bit 0 set. 1234 and -2.
			name: "binary counters, sign extended",
			parts: fixedFrameParts{
				id:       [4]byte{0x01, 0x00, 0x00, 0x00},
				access:   0x01,
				status:   0x01, // bit 0 set: binary
				unit1:    mediumUnitByte(0x05, 0x02),
				unit2:    mediumUnitByte(0x14, 0x00),
				counter1: [4]byte{0xD2, 0x04, 0x00, 0x00},
				counter2: [4]byte{0xFE, 0xFF, 0xFF, 0xFF},
			},
			wantSerial: 1,
			wantDevice: "Electricity",
			wantUnits:  [2]string{"WH", "W"},
			wantValues: [2]float64{1234 * 1.0e3, -2},
		},
		{
			// Water meter, both counters in m^3: the second counter's 3Eh code
			// takes its unit from the first and marks the reading historic.
			name: "same unit but historic",
			parts: fixedFrameParts{
				id:       [4]byte{0x00, 0x00, 0x00, 0x00},
				access:   0x00,
				status:   0x00,
				unit1:    mediumUnitByte(0x2C, 0x03),
				unit2:    mediumUnitByte(fixedUnitSameHistoric, 0x01),
				counter1: [4]byte{0x50, 0x00, 0x00, 0x00},
				counter2: [4]byte{0x25, 0x00, 0x00, 0x00},
			},
			wantSerial: 0,
			wantDevice: "Water",
			wantUnits:  [2]string{"m^3", "m^3"},
			wantValues: [2]float64{50, 25},
		},
		{
			// Unit code 3Fh names no unit, so the counter reaches Value unscaled.
			name: "without units",
			parts: fixedFrameParts{
				id:       [4]byte{0x99, 0x00, 0x00, 0x00},
				access:   0x00,
				status:   0x00,
				unit1:    mediumUnitByte(0x3F, 0x00),
				unit2:    mediumUnitByte(0x3F, 0x00),
				counter1: [4]byte{0x11, 0x00, 0x00, 0x00},
				counter2: [4]byte{0x22, 0x00, 0x00, 0x00},
			},
			wantSerial: 99,
			wantDevice: "Other",
			wantUnits:  [2]string{"none", "none"},
			wantValues: [2]float64{11, 22},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := buildFixedFrame(tc.parts).Decode()
			require.NoError(t, err)

			assert.Equal(t, tc.wantSerial, frame.SerialNumber)
			assert.Equal(t, tc.wantDevice, frame.DeviceType)
			assert.Equal(t, tc.parts.access, frame.AccessNumber)
			assert.Equal(t, tc.parts.status, frame.Status)
			require.Len(t, frame.DataRecords, 2)

			for i, r := range frame.DataRecords {
				require.NoError(t, r.ValueErr, "record %d", i)
				assert.Equal(t, FunctionInstantaneous, r.Function, "record %d", i)
				assert.Equal(t, i, r.StorageNumber, "record %d", i)
				assert.Equal(t, tc.wantUnits[i], r.Unit.Unit, "record %d", i)
				assert.InDelta(t, tc.wantValues[i], r.Value, 1e-9, "record %d", i)
			}
		})
	}
}

// TestDecodeFixedQueryAPI checks that the records.go queries work on a fixed
// response. Counter 2 is the value at the fixed date, so it is storage 1 and
// the current-value queries must not return it.
func TestDecodeFixedQueryAPI(t *testing.T) {
	frame, err := buildFixedFrame(
		fixedFrameParts{
			id:       [4]byte{0x78, 0x56, 0x34, 0x12},
			access:   0x07,
			status:   0x00,
			unit1:    mediumUnitByte(0x05, 0x02), // 1 kWh, electricity
			unit2:    mediumUnitByte(0x29, 0x00), // 1 l
			counter1: [4]byte{0x00, 0x10, 0x00, 0x00},
			counter2: [4]byte{0x21, 0x43, 0x00, 0x00},
		},
	).Decode()
	require.NoError(t, err)

	energy, err := frame.Value(VIFEnergyWh, FunctionInstantaneous)
	require.NoError(t, err)
	assert.InDelta(t, 1000*1.0e3, energy, 1e-9)

	_, err = frame.Value(VIFVolume, FunctionInstantaneous)
	require.ErrorIs(t, err, ErrNoRecord, "counter 2 is historic and must not answer a current-value query")

	historic, ok := frame.Find(MatchType(VIFVolume), MatchStorage(1))
	require.True(t, ok)
	assert.InDelta(t, 4.321, historic.Value, 1e-9)

	assert.Equal(t, "12345678", frame.SecondaryAddressString(),
		"a fixed response carries no manufacturer, so only the serial number is known")
	assert.False(t, frame.HasMoreRecords())
}

// TestDecodeFixedInvalidBCDCounter pins the ValueErr contract: an undecodable
// counter costs its own value and nothing else.
func TestDecodeFixedInvalidBCDCounter(t *testing.T) {
	frame, err := buildFixedFrame(
		fixedFrameParts{
			id:       [4]byte{0x01, 0x00, 0x00, 0x00},
			access:   0x00,
			status:   0x00,
			unit1:    mediumUnitByte(0x05, 0x02),
			unit2:    mediumUnitByte(0x05, 0x00),
			counter1: [4]byte{0x34, 0x12, 0x00, 0x00},
			counter2: [4]byte{0xFF, 0xFF, 0xFF, 0xFF}, // not-available filler
		},
	).Decode()
	require.NoError(t, err, "a bad counter value must not fail the frame")
	require.Len(t, frame.DataRecords, 2)

	require.NoError(t, frame.DataRecords[0].ValueErr)
	assert.InDelta(t, 1234*1.0e3, frame.DataRecords[0].Value, 1e-9)

	bad := frame.DataRecords[1]
	require.Error(t, bad.ValueErr)
	assert.Zero(t, bad.Value)
	assert.Zero(t, bad.RawValue)
	assert.Equal(t, "WH", bad.Unit.Unit, "the unit stays valid when only the value failed")

	_, err = frame.Value(VIFEnergyWh, FunctionInstantaneous)
	assert.NoError(t, err, "the good counter still answers")
}

func TestDecodeFixedInvalidSerial(t *testing.T) {
	parts := fixedFrameParts{
		id:       [4]byte{0x78, 0x56, 0x34, 0xAB}, // 0xAB is not BCD
		unit1:    mediumUnitByte(0x05, 0x02),
		unit2:    mediumUnitByte(0x05, 0x00),
		counter1: [4]byte{0x00, 0x00, 0x00, 0x00},
		counter2: [4]byte{0x00, 0x00, 0x00, 0x00},
	}
	_, err := buildFixedFrame(parts).Decode()
	assert.ErrorIs(t, err, ErrInvalidFrame)
}

func TestDecodeFixedTruncated(t *testing.T) {
	full := buildFixedFrame(
		fixedFrameParts{
			id:    [4]byte{0x01, 0x00, 0x00, 0x00},
			unit1: mediumUnitByte(0x05, 0x02),
			unit2: mediumUnitByte(0x05, 0x00),
		},
	)
	require.Len(t, full, fixedMinFrameLen)

	// Every length that still passes Decode's generic 21-byte floor but is too
	// short for the fixed structure must fail as an invalid frame, not panic.
	for n := 21; n < fixedMinFrameLen; n++ {
		_, err := full[:n].Decode()
		assert.ErrorIs(t, err, ErrInvalidFrame, "length %d", n)
	}
}

func TestFixedMedium(t *testing.T) {
	tests := []struct {
		name       string
		mediumCode byte
		want       string
	}{
		{"other", 0x0, "Other"},
		{"oil", 0x1, "Oil"},
		{"electricity", 0x2, "Electricity"},
		{"gas", 0x3, "Gas"},
		{"heat", 0x4, "Heat: Outlet"},
		{"steam", 0x5, "Steam"},
		{"hot water", 0x6, "Warm water (30-90°C)"},
		{"water", 0x7, "Water"},
		{"heat cost allocator", 0x8, "Heat Cost Allocator"},
		{"reserved low", 0x9, "Unknown Device type"},
		{"gas mode 2", 0xA, "Gas"},
		{"heat mode 2", 0xB, "Heat: Outlet"},
		{"hot water mode 2", 0xC, "Warm water (30-90°C)"},
		{"water mode 2", 0xD, "Water"},
		{"heat cost allocator mode 2", 0xE, "Heat Cost Allocator"},
		{"reserved high", 0xF, "Unknown Device type"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The code is split across the two unit bytes, low half first.
			unit1 := mediumUnitByte(0x00, tc.mediumCode&0x03)
			unit2 := mediumUnitByte(0x00, tc.mediumCode>>2)
			assert.Equal(t, tc.want, deviceTypeLookup(fixedMedium(unit1, unit2)))
		})
	}
}

func TestFixedUnitScaling(t *testing.T) {
	tests := []struct {
		name     string
		code     byte
		wantUnit string
		wantType int
		raw      float64
		want     float64
	}{
		{"1 Wh", 0x02, "WH", VIFEnergyWh, 7, 7},
		{"100 Wh", 0x04, "WH", VIFEnergyWh, 7, 700},
		{"1 kWh", 0x05, "WH", VIFEnergyWh, 7, 7000},
		{"100 MWh", 0x0A, "WH", VIFEnergyWh, 1, 1e8},
		{"1 kJ", 0x0B, "J", VIFEnergyJoule, 1, 1e3},
		{"100 GJ", 0x13, "J", VIFEnergyJoule, 1, 1e11},
		{"1 W", 0x14, "W", VIFPowerW, 3, 3},
		{"100 MW", 0x1C, "W", VIFPowerW, 1, 1e8},
		{"1 kJ/h", 0x1D, "J/h", VIFPowerJoulePerHour, 1, 1e3},
		{"1 ml", 0x26, "m^3", VIFVolume, 1, 1e-6},
		{"1 l", 0x29, "m^3", VIFVolume, 1500, 1.5},
		{"1 m^3", 0x2C, "m^3", VIFVolume, 42, 42},
		{"100 m^3", 0x2E, "m^3", VIFVolume, 3, 300},
		{"1 ml/h", 0x2F, "m^3/h", VIFVolumeFlow, 1, 1e-6},
		{"1 m^3/h", 0x35, "m^3/h", VIFVolumeFlow, 2, 2},
		{"1e-3 degC", 0x38, "C", VIFExternalTemperature, 21500, 21.5},
		{"H.C.A. units", 0x39, "H.C.A", vifUnit["UNITS_FOR_HCA"], 12, 12},
		{"time of day", 0x00, "time", 0, 5, 5},
		{"date", 0x01, "date", 0, 5, 5},
		{"reserved", 0x3B, "none", 0, 5, 5},
		{"without units", 0x3F, "none", 0, 5, 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			unit := fixedUnitOf(tc.code, 0x3F)
			assert.Equal(t, tc.wantUnit, unit.Unit)
			assert.Equal(t, tc.wantType, unit.Type)
			assert.InDelta(t, tc.want, unit.Value(tc.raw), 1e-12)
			assert.Equal(t, DateNone, unit.Date, "the fixed structure has no VIF date types")
		})
	}
}

// TestFixedUnitBothHistoric covers the degenerate case of two counters that
// each point at the other for their unit.
func TestFixedUnitBothHistoric(t *testing.T) {
	unit := fixedUnitOf(fixedUnitSameHistoric, fixedUnitSameHistoric)
	assert.Equal(t, "none", unit.Unit)
	assert.InDelta(t, 42.0, unit.Value(42), 1e-12)
}
