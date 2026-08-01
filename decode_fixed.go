package gombus

import "fmt"

// Fixed data response (CI=0x73), EN 13757-3 clause 6.3.
//
// The fixed structure carries no DIF/VIF records at all. Its layout is purely
// positional and always the same size: identification number, transmission
// counter, status, two medium/unit bytes, two 4-byte counters. Nothing in it is
// optional and nothing repeats.
//
// Decoding still produces the ordinary DecodedFrame with one
// DecodedDataRecord per counter, so every matcher and query in records.go
// works on a fixed response exactly as it does on a variable one.

// Byte offsets into a LongFrame of the fixed structure. They are absolute
// because the fixed response has no variable-length part before them: 68h LL LL
// 68h is 4 bytes, then C, A and CI.
const (
	fixedIDOffset          = 7
	fixedAccessNumOffset   = 11
	fixedStatusOffset      = 12
	fixedUnit1Offset       = 13
	fixedUnit2Offset       = 14
	fixedCounter1Offset    = 15
	fixedCounter2Offset    = 19
	fixedStructureEnd      = 23
	fixedIDWidth           = 4
	fixedCounterWidth      = 4
	fixedMinFrameLen       = fixedStructureEnd + 2 // checksum and stop byte
	fixedStatusBinaryMask  = 0x01                  // 0: counters are BCD, 1: binary
	fixedUnitCodeMask      = 0x3F
	fixedUnitMediumMask    = 0xC0
	fixedUnitSameHistoric  = 0x3E // same unit as the other counter, historic value
	fixedCounter2StorageNo = 1
)

// decodeFixed decodes a fixed data response into the same model the variable
// path produces. The fixed structure names no manufacturer and no version, so
// those fields stay zero.
func (lf LongFrame) decodeFixed() (*DecodedFrame, error) {
	if len(lf) < fixedMinFrameLen {
		return nil, fmt.Errorf(
			"%w: fixed data response needs %d bytes, have %d",
			ErrInvalidFrame, fixedMinFrameLen, len(lf),
		)
	}

	// Same rule as the variable path: an undecodable identification number
	// fails the whole frame. Every reading in the frame is attributed to that
	// number, so a junk one mis-attributes data to another meter. Four BCD
	// bytes hold at most 99999999, so the conversion to int cannot wrap.
	serial, err := bcdMagnitude(lf[fixedIDOffset : fixedIDOffset+fixedIDWidth])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid serial number: %w", ErrInvalidFrame, err)
	}

	status := lf[fixedStatusOffset]
	unit1, unit2 := lf[fixedUnit1Offset], lf[fixedUnit2Offset]
	counter1 := lf[fixedCounter1Offset : fixedCounter1Offset+fixedCounterWidth]
	counter2 := lf[fixedCounter2Offset : fixedCounter2Offset+fixedCounterWidth]

	return &DecodedFrame{
		// raw stays nil deliberately. It exists so SecondaryAddressString can
		// read the manufacturer bytes out of the variable header, and the fixed
		// structure has no manufacturer at those offsets. Leaving it nil makes
		// that method fall back to the serial number instead of reporting the
		// status and unit bytes as a manufacturer ID.
		SerialNumber: int(serial),
		DeviceType:   deviceTypeLookup(fixedMedium(unit1, unit2)),
		AccessNumber: lf[fixedAccessNumOffset],
		Status:       status,
		DataRecords: []DecodedDataRecord{
			fixedRecord(counter1, unit1, unit2, status, 0),
			fixedRecord(counter2, unit2, unit1, status, fixedCounter2StorageNo),
		},
	}, nil
}

// fixedRecord decodes one counter into a data record. other is the sibling
// counter's unit byte, which unit code 3Eh refers to. storage is the record's
// storage number: counter 1 is the current value, counter 2 is the value at the
// fixed date and so is historic.
func fixedRecord(counter []byte, own, other, status byte, storage int) DecodedDataRecord {
	record := DecodedDataRecord{
		Function:      FunctionInstantaneous,
		StorageNumber: storage,
		Unit:          fixedUnitOf(own, other),
	}

	if status&fixedStatusBinaryMask != 0 {
		// Type B, two's complement, LSB first. Mode 1 (CI=0x73) is little
		// endian; mode 2 is a separate CI and is not decoded here.
		record.RawValue = float64(intLE(counter))
		record.Value = record.Unit.Value(record.RawValue)
		return record
	}

	value, err := bcdSigned(counter)
	if err != nil {
		// Undecodable BCD is a bad value, not a bad frame. The counter width is
		// fixed by the structure, so nothing downstream loses sync and the other
		// counter still decodes. Same contract as the DIF BCD path in
		// decodeRecordValue.
		record.ValueErr = fmt.Errorf("fixed counter %d: %w", storage+1, err)
		return record
	}
	record.RawValue = float64(value)
	record.Value = record.Unit.Value(record.RawValue)
	return record
}

// fixedMedium assembles the 4-bit medium code and maps it onto the medium codes
// the variable path uses, so DeviceType reads the same on both paths. The code
// is split across the two unit bytes: bits 7-6 of the first unit byte are its
// low half, bits 7-6 of the second are its high half.
func fixedMedium(unit1, unit2 byte) byte {
	code := (unit1&fixedUnitMediumMask)>>6 | (unit2&fixedUnitMediumMask)>>4
	return fixedMediumCodes[code]
}

// fixedMediumCodes maps the fixed structure's own 4-bit medium table onto the
// variable structure's medium codes. The fixed table is much smaller and it
// spends codes Ah to Eh on mode 2 (MSB first) repeats of media it already
// names, so those map to the same medium as their mode 1 counterpart. 9h and Fh
// are reserved and resolve to the unknown medium rather than to a name we would
// have to invent.
var fixedMediumCodes = [16]byte{
	VariableDataMediumOther,       // 0h
	VariableDataMediumOil,         // 1h
	VariableDataMediumElectricity, // 2h
	VariableDataMediumGas,         // 3h
	VariableDataMediumHeatOut,     // 4h
	VariableDataMediumSteam,       // 5h
	VariableDataMediumHotWater,    // 6h
	VariableDataMediumWater,       // 7h
	VariableDataMediumHeatCost,    // 8h
	VariableDataMediumUnknown,     // 9h reserved
	VariableDataMediumGas,         // Ah gas, mode 2
	VariableDataMediumHeatOut,     // Bh heat, mode 2
	VariableDataMediumHotWater,    // Ch hot water, mode 2
	VariableDataMediumWater,       // Dh water, mode 2
	VariableDataMediumHeatCost,    // Eh H.C.A., mode 2
	VariableDataMediumUnknown,     // Fh reserved
}

// fixedUnitNone is the unit of a counter whose code names no unit: 3Fh
// ("without units"), a reserved code, or two counters that both claim 3Eh. The
// exponent is 1 so the raw counter value survives into Value unscaled.
var fixedUnitNone = Unit{Exp: 1.0, Unit: measureUnit["NONE"]}

// fixedUnitOf resolves a counter's unit from its own unit byte, falling back to
// the other counter's byte for code 3Eh.
func fixedUnitOf(own, other byte) Unit {
	code := own & fixedUnitCodeMask
	if code == fixedUnitSameHistoric {
		// 3Eh means "same unit as the other counter, but a historic value". Two
		// counters both pointing at each other name no unit at all, so stop
		// rather than follow the reference twice.
		if other&fixedUnitCodeMask == fixedUnitSameHistoric {
			return fixedUnitNone
		}
		code = other & fixedUnitCodeMask
	}
	if unit, ok := fixedUnitTable[code]; ok {
		return unit
	}
	return fixedUnitNone
}

// fixedUnitTable maps the fixed structure's 6-bit unit and multiplier code to
// the same Unit values the VIF table yields, so a reading means the same thing
// whichever response carried it.
//
// The encoding is a table of its own and has nothing to do with the VIF: it
// names an absolute unit and multiplier ("100 kWh") where a VIF names a
// quantity and an exponent. The exponents here therefore normalise every entry
// to the base unit the VIF table uses for that quantity (Wh, J, W, J/h, m^3,
// m^3/h, degC), which is what makes Value comparable across the two paths.
//
// Codes 3Ah to 3Dh are reserved, 3Eh is handled by fixedUnitOf and 3Fh names no
// unit, so none of them appear here.
var fixedUnitTable = map[byte]Unit{
	// 00h and 01h: the counter holds a time or a date, not a measurement. The
	// digits are not one of the VIF date types, so Date stays DateNone and only
	// RawValue carries them. Type stays unset for the same reason: claiming the
	// date VIF would promise a decoded timestamp this structure cannot give.
	0x00: {Exp: 1.0, Unit: measureUnit["TIME"]},
	0x01: {Exp: 1.0, Unit: measureUnit["DATE"]},

	// 02h to 0Ah: energy, 1 Wh to 100 MWh, normalised to Wh.
	0x02: {Exp: 1.0e0, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x03: {Exp: 1.0e1, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x04: {Exp: 1.0e2, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x05: {Exp: 1.0e3, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x06: {Exp: 1.0e4, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x07: {Exp: 1.0e5, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x08: {Exp: 1.0e6, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x09: {Exp: 1.0e7, Unit: measureUnit["WH"], Type: VIFEnergyWh},
	0x0A: {Exp: 1.0e8, Unit: measureUnit["WH"], Type: VIFEnergyWh},

	// 0Bh to 13h: energy, 1 kJ to 100 GJ, normalised to J.
	0x0B: {Exp: 1.0e3, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x0C: {Exp: 1.0e4, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x0D: {Exp: 1.0e5, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x0E: {Exp: 1.0e6, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x0F: {Exp: 1.0e7, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x10: {Exp: 1.0e8, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x11: {Exp: 1.0e9, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x12: {Exp: 1.0e10, Unit: measureUnit["J"], Type: VIFEnergyJoule},
	0x13: {Exp: 1.0e11, Unit: measureUnit["J"], Type: VIFEnergyJoule},

	// 14h to 1Ch: power, 1 W to 100 MW, normalised to W.
	0x14: {Exp: 1.0e0, Unit: measureUnit["W"], Type: VIFPowerW},
	0x15: {Exp: 1.0e1, Unit: measureUnit["W"], Type: VIFPowerW},
	0x16: {Exp: 1.0e2, Unit: measureUnit["W"], Type: VIFPowerW},
	0x17: {Exp: 1.0e3, Unit: measureUnit["W"], Type: VIFPowerW},
	0x18: {Exp: 1.0e4, Unit: measureUnit["W"], Type: VIFPowerW},
	0x19: {Exp: 1.0e5, Unit: measureUnit["W"], Type: VIFPowerW},
	0x1A: {Exp: 1.0e6, Unit: measureUnit["W"], Type: VIFPowerW},
	0x1B: {Exp: 1.0e7, Unit: measureUnit["W"], Type: VIFPowerW},
	0x1C: {Exp: 1.0e8, Unit: measureUnit["W"], Type: VIFPowerW},

	// 1Dh to 25h: power, 1 kJ/h to 100 GJ/h, normalised to J/h.
	0x1D: {Exp: 1.0e3, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x1E: {Exp: 1.0e4, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x1F: {Exp: 1.0e5, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x20: {Exp: 1.0e6, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x21: {Exp: 1.0e7, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x22: {Exp: 1.0e8, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x23: {Exp: 1.0e9, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x24: {Exp: 1.0e10, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},
	0x25: {Exp: 1.0e11, Unit: measureUnit["J_H"], Type: VIFPowerJoulePerHour},

	// 26h to 2Eh: volume, 1 ml to 100 m^3, normalised to m^3.
	0x26: {Exp: 1.0e-6, Unit: measureUnit["M3"], Type: VIFVolume},
	0x27: {Exp: 1.0e-5, Unit: measureUnit["M3"], Type: VIFVolume},
	0x28: {Exp: 1.0e-4, Unit: measureUnit["M3"], Type: VIFVolume},
	0x29: {Exp: 1.0e-3, Unit: measureUnit["M3"], Type: VIFVolume},
	0x2A: {Exp: 1.0e-2, Unit: measureUnit["M3"], Type: VIFVolume},
	0x2B: {Exp: 1.0e-1, Unit: measureUnit["M3"], Type: VIFVolume},
	0x2C: {Exp: 1.0e0, Unit: measureUnit["M3"], Type: VIFVolume},
	0x2D: {Exp: 1.0e1, Unit: measureUnit["M3"], Type: VIFVolume},
	0x2E: {Exp: 1.0e2, Unit: measureUnit["M3"], Type: VIFVolume},

	// 2Fh to 37h: volume flow, 1 ml/h to 100 m^3/h, normalised to m^3/h.
	0x2F: {Exp: 1.0e-6, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x30: {Exp: 1.0e-5, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x31: {Exp: 1.0e-4, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x32: {Exp: 1.0e-3, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x33: {Exp: 1.0e-2, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x34: {Exp: 1.0e-1, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x35: {Exp: 1.0e0, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x36: {Exp: 1.0e1, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},
	0x37: {Exp: 1.0e2, Unit: measureUnit["M3_H"], Type: VIFVolumeFlow},

	// 38h: temperature in units of 1e-3 degC. The fixed structure does not say
	// which temperature it is, unlike the VIF table which has separate codes for
	// flow, return, difference and external. It files under external because
	// that is the one of the four that claims nothing about the meter's
	// hydraulics.
	0x38: {Exp: 1.0e-3, Unit: measureUnit["C"], Type: VIFExternalTemperature},

	// 39h: units for a heat cost allocator, dimensionless.
	0x39: {Exp: 1.0e0, Unit: measureUnit["HCA"], Type: vifUnit["UNITS_FOR_HCA"]},
}
