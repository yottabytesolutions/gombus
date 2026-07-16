package gombus

// M-Bus protocol constants per EN 13757-3. Field-bit masks for the DIF
// (Data Information Field), DIFE (extension), VIF (Value Information Field)
// and VIFE; medium / device-type codes; and C-field control bits.

// DIF field-bit masks.
//
//goland:noinspection GoUnusedConst
const (
	DataRecordDifMaskInst       = 0x00
	DataRecordDifMaskMin        = 0x10
	DataRecordDifMaskTypeInt32  = 0x04
	DataRecordDifMaskData       = 0x0F
	DataRecordDifMaskFunction   = 0x30
	DataRecordDifMaskStorageNo  = 0x40
	DataRecordDifMaskExtension  = 0x80
	DataRecordDifMaskNonData    = 0xF0
	DataRecordDifeMaskStorageNo = 0x0F
	DataRecordDifeMaskTariff    = 0x30
	DataRecordDifeMaskDevice    = 0x40
	DataRecordDifeMaskExtension = 0x80
)

// DIB / VIB framing.
//
//goland:noinspection GoUnusedConst
const (
	DibDifWithoutExtension     = 0x7F
	DibDifExtensionBit         = 0x80
	DibVifWithoutExtension     = 0x7F
	DibVifExtensionBit         = 0x80
	DibDifManufacturerSpecific = 0x0F
	DibDifMoreRecordsFollow    = 0x1F
	DibDifIdleFiller           = 0x2F
)

// Variable-data medium / device-type codes.
const (
	VariableDataMediumOther        = 0x00
	VariableDataMediumOil          = 0x01
	VariableDataMediumElectricity  = 0x02
	VariableDataMediumGas          = 0x03
	VariableDataMediumHeatOut      = 0x04
	VariableDataMediumSteam        = 0x05
	VariableDataMediumHotWater     = 0x06
	VariableDataMediumWater        = 0x07
	VariableDataMediumHeatCost     = 0x08
	VariableDataMediumComprAir     = 0x09
	VariableDataMediumCoolOut      = 0x0A
	VariableDataMediumCoolIn       = 0x0B
	VariableDataMediumHeatIn       = 0x0C
	VariableDataMediumHeatCool     = 0x0D
	VariableDataMediumBus          = 0x0E
	VariableDataMediumUnknown      = 0x0F
	VariableDataMediumIrrigation   = 0x10
	VariableDataMediumWaterLogger  = 0x11
	VariableDataMediumGasLogger    = 0x12
	VariableDataMediumGasConv      = 0x13
	VariableDataMediumColorific    = 0x14
	VariableDataMediumBoilWater    = 0x15
	VariableDataMediumColdWater    = 0x16
	VariableDataMediumDualWater    = 0x17
	VariableDataMediumPressure     = 0x18
	VariableDataMediumAdc          = 0x19
	VariableDataMediumSmoke        = 0x1A
	VariableDataMediumRoomSensor   = 0x1B
	VariableDataMediumGasDetector  = 0x1C
	VariableDataMediumCoAlarm      = 0x1D
	VariableDataMediumHeatAlarm    = 0x1E
	VariableDataMediumSensor       = 0x1F
	VariableDataMediumBreakerE     = 0x20
	VariableDataMediumValve        = 0x21
	VariableDataMediumCustomerUnit = 0x25
	VariableDataMediumWasteWater   = 0x28
	VariableDataMediumGarbage      = 0x29
	VariableDataMediumCommCtrl     = 0x31
	VariableDataMediumRepeaterUni  = 0x32
	VariableDataMediumRepeaterBi   = 0x33
	VariableDataMediumRcSystem     = 0x36
	VariableDataMediumRcMeter      = 0x37
	VariableDataMediumWiredAdapter = 0x38
)

// C-field control bits.
const (
	ControlMaskFcb = 0x20
	ControlMaskFcv = 0x10
)

// SingleCharacterFrame (E5h) is the slave acknowledgement frame, sent in
// response to SND_NKE and similar requests that do not produce a long-frame
// reply.
const SingleCharacterFrame = 0xe5
