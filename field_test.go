package gombus

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeviceTypeLookup covers every entry in deviceTypeNames. Nine of them,
// including medium 0x07, had no row here at all, which is half of why 0x07 read
// "Warm" (a truncation of the 0x06 string above it) until the libmbus
// differential test caught it. The other half was realframes_test.go asserting
// the typo as expected. Every string below is checked against libmbus's
// mbus_data_variable_medium_lookup.
type deviceTypeCase struct {
	code    byte
	want    string
	wantErr bool
}

func TestDeviceTypeLookup(t *testing.T) {
	for _, tc := range deviceTypeLookupCases() {
		assert.Equal(t, tc.want, deviceTypeLookup(tc.code), "code 0x%02x", tc.code)
	}
}

func deviceTypeLookupCases() []deviceTypeCase {
	return []deviceTypeCase{
		{VariableDataMediumOther, "Other", false},
		{VariableDataMediumOil, "Oil", false},
		{VariableDataMediumElectricity, "Electricity", false},
		{VariableDataMediumGas, "Gas", false},
		{VariableDataMediumHeatOut, "Heat: Outlet", false},
		{VariableDataMediumSteam, "Steam", false},
		{VariableDataMediumColdWater, "Cold water", false},
		{VariableDataMediumHotWater, "Warm water (30-90°C)", false},
		// 0x06 is warm water and 0x07 is plain water. Two real water meters in
		// the corpus send 0x07, so mixing them up mislabels every read.
		{VariableDataMediumWater, "Water", false},
		{VariableDataMediumHeatCost, "Heat Cost Allocator", false},
		{VariableDataMediumComprAir, "Compressed Air", false},
		{VariableDataMediumCoolOut, "Cooling load meter: Outlet", false},
		{VariableDataMediumCoolIn, "Cooling load meter: Inlet", false},
		{VariableDataMediumBus, "Bus / System", false},
		{VariableDataMediumUnknown, "Unknown Device type", false},
		{VariableDataMediumIrrigation, "Irrigation Water", false},
		{VariableDataMediumHeatIn, "Heat: Inlet", false},
		{VariableDataMediumHeatCool, "Heat / Cooling load meter", false},
		{VariableDataMediumWaterLogger, "Water Logger", false},
		{VariableDataMediumGasLogger, "Gas Logger", false},
		{VariableDataMediumGasConv, "Gas Converter", false},
		{VariableDataMediumColorific, "Calorific value", false},
		{VariableDataMediumBoilWater, "Hot water (>90°C)", false},
		{VariableDataMediumDualWater, "Dual water", false},
		{VariableDataMediumPressure, "Pressure", false},
		{VariableDataMediumAdc, "A/D Converter", false},
		{VariableDataMediumSmoke, "Smoke Detector", false},
		{VariableDataMediumRoomSensor, "Ambient Sensor", false},
		{VariableDataMediumGasDetector, "Gas Detector", false},
		{VariableDataMediumBreakerE, "Breaker: Electricity", false},
		{VariableDataMediumValve, "Valve: Gas or Water", false},
		{VariableDataMediumCustomerUnit, "Customer Unit: Display Device", false},
		{VariableDataMediumWasteWater, "Waste Water", false},
		{VariableDataMediumGarbage, "Garbage", false},
		{VariableDataMediumRcSystem, "Radio Converter: System", false},
		{VariableDataMediumRcMeter, "Radio Converter: Meter", false},

		// OMS Vol.2 Issue 5.0.1, Table 2: alarm and sensor devices.
		{VariableDataMediumCoAlarm, "Carbon Monoxide Alarm", false},
		{VariableDataMediumHeatAlarm, "Heat Alarm", false},
		{VariableDataMediumSensor, "Sensor", false},
		// OMS Vol.2 Issue 5.0.1, Table 3: bus infrastructure. The wired table
		// reserves these, so we previously reported them as Reserved.
		{VariableDataMediumCommCtrl, "Communication Controller", false},
		{VariableDataMediumRepeaterUni, "Repeater: Unidirectional", false},
		{VariableDataMediumRepeaterBi, "Repeater: Bidirectional", false},
		{VariableDataMediumWiredAdapter, "Wired Adapter", false},

		// OMS Vol.2 Issue 5.0.1, Table 4 reserved ranges.
		{0x22, "Reserved", false}, // 22h-24h switching devices
		{0x26, "Reserved", false}, // 26h-27h customer units
		{0x2A, "Reserved", false}, // 2Ah carbon dioxide
		// 0x2B is reserved for environmental meters, not a VOC sensor. It
		// carried an invented name until the spec settled it.
		{0x2B, "Reserved", false}, // 2Bh-2Fh environmental meter
		{0x2F, "Reserved", false},
		// 30h, 34h-35h and 39h-3Fh are one "Reserved for system devices" cell
		// in Table 4. 0x30 previously carried an inherited "Service Unit".
		{0x30, "Reserved", false},
		{0x34, "Reserved", false},
		{0x35, "Reserved", false},
		{0x39, "Reserved", false},
		{0x3F, "Reserved", false},
		// Table 4 reserves 40h-FEh outright. These used to be a hard error,
		// which rejected the whole frame over one header byte.
		{0x40, "Reserved", false},
		{0x99, "Reserved", false},
		{0xFE, "Reserved", false},
		// Table 4: FFh "Not applicable (reserved for wildcard search)".
		{0xFF, "Not applicable (wildcard)", false},
	}
}

// TestDeviceTypeLookupIsTotal pins that every medium code resolves to something.
// The owner's requirement is that any medium is supported, and a medium byte we
// cannot name must never reject a frame whose readings are fine.
func TestDeviceTypeLookupIsTotal(t *testing.T) {
	for i := range 0x100 {
		got := deviceTypeLookup(byte(i))
		assert.NotEmpty(t, got, "code 0x%02x must resolve", i)
	}
}

// TestDeviceTypeNamesAllCovered fails if a table entry has no row in
// TestDeviceTypeLookup. Nine entries had none, including 0x07, which is half of
// why "Warm" was undetectable: the unit test could not fail. Keep it at zero.
func TestDeviceTypeNamesAllCovered(t *testing.T) {
	covered := map[byte]bool{}
	for _, tc := range deviceTypeLookupCases() {
		covered[tc.code] = true
	}
	for code := range deviceTypeNames {
		assert.True(t, covered[code],
			"medium 0x%02x (%q) has no row in TestDeviceTypeLookup", code, deviceTypeNames[code])
	}
}

func TestDecodeUnitMissingVIFE(t *testing.T) {
	// VIFs that require a VIFE byte must produce a sentinel "unknown" Unit
	// when no VIFE is supplied, instead of panicking.
	for _, vif := range []byte{0xFB, 0xFC, 0xFD} {
		u := decodeUnit(vif, nil)
		assert.Equal(t, "unknown", u.Unit, "vif=0x%x", vif)
		assert.Equal(t, "missing VIFE", u.VIFUnitDesc, "vif=0x%x", vif)
		assert.InDelta(t, 1.0, u.Exp, 1e-12, "vif=0x%x", vif)
	}
}

func TestDecodeUnitFBExtension(t *testing.T) {
	// 0xFB + VIFE selects an entry in the first extension table (code | 0x200).
	// Pick a known entry: 0x279 = "Cumul count max power" with Exp=1e-3.
	u := decodeUnit(0xFB, []byte{0x79})
	assert.InDelta(t, 1.0e-3, u.Exp, 1e-15)
	assert.Equal(t, "W", u.Unit)
}

func TestDecodeUnitFCWithKnownFactorRange(t *testing.T) {
	// 0xFC with VIFE 0x70..0x77 produces a calculated factor and the
	// "variable" unit string.
	u := decodeUnit(0xFC, []byte{0x70})
	assert.Equal(t, "variable", u.Unit)
	// 10 ** ((0 & 0x07) - 6) = 1e-6.
	assert.InDelta(t, 1e-6, u.Exp, 1e-12)
}

// TestDecodeUnitFCFactors pins the factor for every 0xFC VIFE range. An unknown
// VIFE used to leave the factor at its zero value, which zeroed Value while
// RawValue held the truth.
func TestDecodeUnitFCFactors(t *testing.T) {
	cases := []struct {
		name string
		vife byte
		want float64
	}{
		{"0x70 lower bound", 0x70, 1e-6},
		{"0x77 upper bound", 0x77, 1e1},
		{"0x78 lower bound", 0x78, 1e-3},
		{"0x7B upper bound", 0x7B, 1e0},
		{"0x7D", 0x7D, 1000},
		{"unknown 0x00", 0x00, 1.0},
		{"unknown 0x7C", 0x7C, 1.0},
		{"unknown 0x7E", 0x7E, 1.0},
		{"unknown 0x6F", 0x6F, 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := decodeUnit(0xFC, []byte{tc.vife})
			assert.Equal(t, "variable", u.Unit)
			assert.InDelta(t, tc.want, u.Exp, tc.want*1e-9)
			// The factor must scale a reading, never annihilate it.
			assert.InDelta(t, 1234*tc.want, u.Value(1234), 1e-9)
		})
	}
}

// TestDecodeUnitDurationOfLimitExceed pins the E101 ufnn qualifier
// (m-bus.com appendix 8.4.5). Such a record counts how long a limit was
// exceeded, so its value is a duration; the base VIF only says which limit.
// Reading it as the VIF's own quantity mislabels it and scales it by the VIF's
// exponent, which is wrong for any VIF whose exponent is not 1.
func TestDecodeUnitDurationOfLimitExceed(t *testing.T) {
	cases := []struct {
		name    string
		vif     byte
		vife    byte
		wantExp float64
	}{
		// nn = 00 seconds, 01 minutes, 10 hours, 11 days. u and f select which
		// limit and first/last; neither changes the time base.
		{"lower first, seconds", 0xBE, 0x50, 1},
		{"lower first, minutes", 0xBE, 0x51, 60},
		{"lower first, hours", 0xBE, 0x52, 3600},
		{"lower first, days", 0xBE, 0x53, 86400},
		{"lower last, seconds", 0xBE, 0x54, 1},
		{"upper first, seconds", 0xBE, 0x58, 1},
		{"upper first, days", 0xBE, 0x5B, 86400},
		{"upper last, hours", 0xBE, 0x5E, 3600},
		// The base VIF's own exponent must not leak in: 0xBB is 1e-3 m^3/h, and
		// scaling a duration in seconds by 1e-3 is exactly the bug.
		{"base VIF exponent ignored", 0xBB, 0x50, 1},
		{"base VIF exponent ignored, hours", 0xBB, 0x52, 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := decodeUnit(tc.vif, []byte{tc.vife})
			assert.Equal(t, measureUnit["SECONDS"], u.Unit, "value is a duration")
			assert.InDelta(t, tc.wantExp, u.Exp, tc.wantExp*1e-9)
			// Type is kept: it says which quantity's limit was exceeded.
			assert.Equal(t, vifUnit["VOLUME_FLOW"], u.Type)
		})
	}
}

// TestDecodeUnitDurationNotAppliedToManufacturerBlock pins the guard that the
// corpus caught. Under a manufacturer-specific VIF the primary VIF table says
// "VIFE and data of this block are manufacturer specific", so 0x52 there is not
// a standard duration qualifier. Reading it as one scaled a real EMU meter's
// reading of 500 by 3600.
func TestDecodeUnitDurationNotAppliedToManufacturerBlock(t *testing.T) {
	for _, vif := range []byte{0x7F, 0xFF} {
		t.Run(fmt.Sprintf("VIF_0x%02X", vif), func(t *testing.T) {
			u := decodeUnit(vif, []byte{0x52, 0xFF, 0x02})
			assert.InDelta(t, 1.0, u.Exp, 0, "a manufacturer's byte is not a standard qualifier")
			assert.Equal(t, vifUnit["MANUFACTURER_SPEC"], u.Type)
		})
	}
	// The same guard at VIFE level: once 0x7F appears, stop interpreting.
	u := decodeUnit(0xBE, []byte{0xFF, 0x52})
	assert.InDelta(t, 1.0, u.Exp, 0, "0x52 after a 0x7F escape is manufacturer specific")
	assert.Equal(t, measureUnit["M3_H"], u.Unit)
}

func TestDecodeStorageNumberWithDIFE(t *testing.T) {
	// DIF storage bit (0x40) + 2 DIFE storage nibbles (mask 0x0F).
	//   bit 0           : (0x40 & 0x40) >> 6  = 1
	//   bits 1..4 (<<1) : (0x05 & 0x0F) << 1  = 10
	//   bits 5..8 (<<5) : (0x09 & 0x0F) << 5  = 288
	//   total = 1 | 10 | 288 = 299.
	got := decodeStorageNumber(0x40, []byte{0x05, 0x09})
	assert.Equal(t, 299, got)
}

func TestDecodeTariff(t *testing.T) {
	// Each DIFE contributes 2 tariff bits (mask 0x30 >> 4).
	// dife[0] = 0x10 → tariff bits 00 0001 → val 1, shifted into bits 0..1.
	// dife[1] = 0x20 → val 2, shifted into bits 2..3.
	got := decodeTariff([]byte{0x10, 0x20})
	assert.Equal(t, 0b00001001, got) // 1 << 0 | 2 << 2 = 0x09
}

func TestDecodeDevice(t *testing.T) {
	// Each DIFE contributes 1 device bit (mask 0x40 >> 6).
	got := decodeDevice([]byte{0x40, 0x40, 0x00})
	assert.Equal(t, 0b011, got)
}

// TestDecodeUnnamedMediumDoesNotRejectFrame is the point of making the lookup
// total. A medium code we cannot name used to return an error from
// deviceTypeLookup, which Decode passed straight up, throwing away every reading
// in the frame over one header byte. Reserved codes are spec-defined; an unknown
// one is still no reason to discard good data.
func TestDecodeUnnamedMediumDoesNotRejectFrame(t *testing.T) {
	for _, medium := range []byte{0x30, 0x35, 0x40, 0x99, 0xFE, 0xFF} {
		t.Run(fmt.Sprintf("medium_0x%02X", medium), func(t *testing.T) {
			// One 16-bit reading, VIF 0x5A (flow temperature, exp 0.1).
			lf := wrapAsLongFrame(hexToBytes("02 5A D4 09"))
			lf[14] = medium

			df, err := lf.Decode()
			require.NoError(t, err, "an unnamed medium must not reject the frame")
			assert.NotEmpty(t, df.DeviceType)
			require.Len(t, df.DataRecords, 1)
			assert.InDelta(t, 251.6, df.DataRecords[0].Value, 1e-9, "the reading survives")
		})
	}
}
