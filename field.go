package gombus

import (
	"fmt"
	"log/slog"
	"math"
)

// decodeRecordFunction returns the function description for a DIF.
// Special-function bytes (0x0F, 0x1F, 0x2F, 0x7F) are labelled directly;
// other DIFs go through the function-bit mask.
func decodeRecordFunction(dif byte) string {
	switch dif {
	case 0x0F:
		return "Manufacturer specific"
	case 0x1F:
		return "More records follow"
	case 0x2F:
		return "Idle filler"
	case 0x7F:
		return "Global readout request"
	}
	switch dif & DataRecordDifMaskFunction {
	case 0x00:
		return "Instantaneous value"
	case 0x10:
		return "Maximum value"
	case 0x20:
		return "Minimum value"
	case 0x30:
		return "Value during error state"
	default:
		return "Unknown"
	}
}

// The medium table below merges two authorities, deliberately. Which one
// applies is a real question, so it is answered here rather than left to be
// re-derived:
//
// Wired M-Bus (m-bus.com appendix 8.4.1, "Measured Medium Variable Structure")
// defines 0x00-0x19 and then reserves "20 to FF". gombus is a wired M-Bus
// library, so that table is the base.
//
// OMS Specification Volume 2 (Primary Communication), Issue 5.0.1 / 2023-12,
// Tables 2, 3 and 4 (p26-27) define many codes the wired table reserves, and
// real meters send them. So the table is a SUPERSET: wired for 0x00-0x19,
// extended with OMS for the codes wired leaves reserved. Where wired reserves a
// code and OMS names it, OMS wins; where both reserve it, it is Reserved.
//
// This was a patchwork before: some OMS extensions adopted, some missing, one
// (0x2B "VOC Sensor") invented outright. It is now a complete, cited superset.
// Every code 0x00-0xFF resolves, so deviceTypeLookup cannot fail.
//
// The strings stay in OUR vocabulary rather than either spec's wording, because
// the field is named DeviceType where both specs say Medium. 0x0E "Bus / System"
// is the wired spec's exact spelling; 0x0F follows OMS Table 4's "Unknown Device
// Type". Do not restyle them to match libmbus.

// deviceTypeNames maps M-Bus medium codes to human-readable names.
var deviceTypeNames = map[byte]string{
	VariableDataMediumOther:       "Other",
	VariableDataMediumOil:         "Oil",
	VariableDataMediumElectricity: "Electricity",
	VariableDataMediumGas:         "Gas",
	VariableDataMediumHeatOut:     "Heat: Outlet",
	VariableDataMediumSteam:       "Steam",
	VariableDataMediumHotWater:    "Warm water (30-90°C)",
	VariableDataMediumWater:       "Water",
	VariableDataMediumHeatCost:    "Heat Cost Allocator",
	VariableDataMediumComprAir:    "Compressed Air",
	VariableDataMediumCoolOut:     "Cooling load meter: Outlet",
	VariableDataMediumCoolIn:      "Cooling load meter: Inlet",
	VariableDataMediumHeatIn:      "Heat: Inlet",
	VariableDataMediumHeatCool:    "Heat / Cooling load meter",
	VariableDataMediumBus:         "Bus / System",
	VariableDataMediumUnknown:     "Unknown Device type",
	VariableDataMediumIrrigation:  "Irrigation Water",
	VariableDataMediumWaterLogger: "Water Logger",
	VariableDataMediumGasLogger:   "Gas Logger",
	VariableDataMediumGasConv:     "Gas Converter",
	VariableDataMediumColorific:   "Calorific value",
	VariableDataMediumBoilWater:   "Hot water (>90°C)",
	VariableDataMediumColdWater:   "Cold water",
	VariableDataMediumDualWater:   "Dual water",
	VariableDataMediumPressure:    "Pressure",
	VariableDataMediumAdc:         "A/D Converter",
	VariableDataMediumSmoke:       "Smoke Detector",
	VariableDataMediumRoomSensor:  "Ambient Sensor",
	VariableDataMediumGasDetector: "Gas Detector",
	// 0x1D-0x1F, OMS Table 2. Alarm devices report their own maintenance data,
	// not the alarm itself.
	VariableDataMediumCoAlarm:   "Carbon Monoxide Alarm",
	VariableDataMediumHeatAlarm: "Heat Alarm",
	VariableDataMediumSensor:    "Sensor",

	VariableDataMediumBreakerE:     "Breaker: Electricity",
	VariableDataMediumValve:        "Valve: Gas or Water",
	VariableDataMediumCustomerUnit: "Customer Unit: Display Device",
	VariableDataMediumWasteWater:   "Waste Water",
	VariableDataMediumGarbage:      "Garbage",
	// 0x31-0x33 and 0x38, OMS Table 3. Bus infrastructure rather than meters,
	// which is why the wired table has no place for them.
	VariableDataMediumCommCtrl:     "Communication Controller",
	VariableDataMediumRepeaterUni:  "Repeater: Unidirectional",
	VariableDataMediumRepeaterBi:   "Repeater: Bidirectional",
	VariableDataMediumRcSystem:     "Radio Converter: System",
	VariableDataMediumRcMeter:      "Radio Converter: Meter",
	VariableDataMediumWiredAdapter: "Wired Adapter",
}

// wildcardDeviceType is OMS Vol.2 Table 4's FFh, "Not applicable (reserved for
// wildcard search)". A slave does not report it as its own medium; it appears
// in a master's search.
const wildcardDeviceType = 0xFF

// deviceTypeLookup returns the human-readable device-type name for a medium
// code. Every code resolves, so it cannot fail. Anything the merged table does
// not name is Reserved under one authority or both:
//
//	22h to 24h                   Reserved for switching devices  (OMS Table 4)
//	26h to 27h                   Reserved for customer units     (OMS Table 4)
//	2Ah                          Reserved for Carbon dioxide     (OMS Table 4)
//	2Bh to 2Fh                   Reserved for environmental meter(OMS Table 4)
//	30h, 34h to 35h, 39h to 3Fh  Reserved for system devices     (OMS Table 4)
//	40h to FEh                   Reserved                        (OMS Table 4)
//
// 0x2B once carried an invented "VOC Sensor" name and 0x30 an inherited
// "Service Unit". Neither is named by OMS, and the wired table reserves
// everything from 20h up, so both are reserved under both authorities. Do not
// re-invent them: add a name only with a citation.
//
// Returning Reserved rather than an error is deliberate. A medium byte we
// cannot name is no reason to reject a frame whose readings are perfectly good,
// and every code in 40h-FEh is spec-defined as Reserved anyway.
func deviceTypeLookup(deviceType byte) string {
	if name, ok := deviceTypeNames[deviceType]; ok {
		return name
	}
	if deviceType == wildcardDeviceType {
		return "Not applicable (wildcard)"
	}
	return "Reserved"
}

// decodeUnit interprets the VIF (Value Information Field) and any VIFE
// extensions to determine the unit of measurement.
func decodeUnit(vif byte, vife []byte) Unit {
	// Codes 0xFB / 0xFD / 0xFC reach into extension tables; require a VIFE byte.
	if (vif == 0xFB || vif == 0xFD || vif == 0xFC) && len(vife) == 0 {
		slog.Warn("missing VIFE for VIF", "vif", vif)
		return Unit{Exp: 1.0, Unit: "unknown", VIFUnitDesc: "missing VIFE"}
	}

	var code int
	switch vif {
	case 0xFB:
		code = int(vife[0])&DibVifWithoutExtension | 0x200
	case 0xFD:
		code = int(vife[0])&DibVifWithoutExtension | 0x100
	case 0xFC:
		code = int(vife[0]) & DibVifWithoutExtension
		// An unknown VIFE leaves the value unscaled rather than multiplying it
		// by a zero factor, which would report every reading as 0.
		factor := 1.0
		switch {
		case code >= 0x70 && code <= 0x77:
			factor = math.Pow10((int(vife[0]) & 0x07) - 6)
		case code >= 0x78 && code <= 0x7B:
			factor = math.Pow10((int(vife[0]) & 0x03) - 3)
		case code == 0x7D:
			factor = 1000
		default:
			slog.Warn("unknown VIFE for variable VIF, using factor 1", "vife", vife[0])
		}
		return Unit{
			Exp:  factor,
			Unit: "variable",
			Type: vifUnit["VARIABLE_VIF"],
		}
	default:
		code = int(vif) & DibVifWithoutExtension
	}

	unit, ok := unitTable[code]
	if !ok {
		slog.Warn("unknown unit code", "code", code)
		return Unit{Exp: 1.0, Unit: "unknown", VIFUnitDesc: fmt.Sprintf("unknown code 0x%x", code)}
	}
	if base, isDuration := durationOfLimitExceed(combinableVIFEs(vif, vife)); isDuration {
		// The value is a duration, not the VIF's quantity. Type is deliberately
		// kept: it says which quantity's limit was exceeded.
		unit.Exp, unit.Unit = base.Exp, base.Unit
	}
	return unit
}

// combinableVIFEs returns the VIFEs carrying combinable (orthogonal) meaning.
// Per m-bus.com appendix 8.4.5 that table is "defined for an enhancement of VIF
// other than $FD and $FB": under those two the first VIFE instead selects the
// extension table, so only the ones after it are combinable.
//
// A manufacturer-specific VIF has none at all. The primary VIF table says of
// E111 1111: "VIFE and data of this block are manufacturer specific". Reading
// those bytes against the standard table would be reading a manufacturer's byte
// as a standard one, which is how an EMU meter's 0x52 became a 3600x scale.
func combinableVIFEs(vif byte, vife []byte) []byte {
	if vif&DibVifWithoutExtension == vifManufacturerSpecific {
		return nil
	}
	if vif == 0xFB || vif == 0xFD {
		if len(vife) < 2 {
			return nil
		}
		return vife[1:]
	}
	return vife
}

// vifManufacturerSpecific is E111 1111, the primary VIF that hands the meaning
// of the rest of the block to the manufacturer.
const vifManufacturerSpecific = 0x7F

// durationTimeBases maps the nn field of a duration VIFE to a unit, normalised
// to seconds in the exponent as the rest of the table does (see 0x20-0x23).
var durationTimeBases = [4]Unit{
	{Exp: 1.0, Unit: measureUnit["SECONDS"]},
	{Exp: 60.0, Unit: measureUnit["SECONDS"]},
	{Exp: 3600.0, Unit: measureUnit["SECONDS"]},
	{Exp: 86400.0, Unit: measureUnit["SECONDS"]},
}

// durationOfLimitExceed looks for a duration-of-limit-exceed VIFE (E101 ufnn,
// m-bus.com appendix 8.4.5) and returns its time base. Such a record counts how
// long a limit was exceeded, so its value is a duration and the base VIF names
// only which limit it was. Reading it as the VIF's own quantity mislabels the
// record, and scales it by the VIF's exponent, which is wrong for any VIF whose
// exponent is not 1.
func durationOfLimitExceed(vifes []byte) (Unit, bool) {
	for _, v := range vifes {
		code := v & DibVifWithoutExtension
		if code == 0x7F {
			// E111 1111: the rest of the block is manufacturer specific, so
			// stop rather than read a manufacturer's byte as a standard one.
			return Unit{}, false
		}
		if code >= 0x50 && code <= 0x5F {
			return durationTimeBases[code&0x03], true
		}
	}
	return Unit{}, false
}

// decodeStorageNumber assembles the storage number from the DIF storage bit
// and any DIFE storage bits (4 per DIFE byte).
func decodeStorageNumber(dif int, dife []byte) int {
	result := (dif & DataRecordDifMaskStorageNo) >> 6
	bitIndex := 1
	for _, d := range dife {
		result |= int(d&DataRecordDifeMaskStorageNo) << bitIndex
		bitIndex += 4
	}
	return result
}

// decodeTariff assembles the tariff number from DIFE tariff bits (2 per DIFE).
func decodeTariff(dife []byte) int {
	result := 0
	bitIndex := 0
	for _, d := range dife {
		result |= int(d&DataRecordDifeMaskTariff>>4) << bitIndex
		bitIndex += 2
	}
	return result
}

// decodeDevice assembles the device number from DIFE device bits (1 per DIFE).
func decodeDevice(dife []byte) int {
	result := 0
	bitIndex := 0
	for _, d := range dife {
		result |= int(d&DataRecordDifeMaskDevice>>6) << bitIndex
		bitIndex++
	}
	return result
}
