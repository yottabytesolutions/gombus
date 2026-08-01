package gombus

// VIF type codes as they appear in Unit.Type on decoded data records. These
// mirror the primary VIF table (EN 13757-3); use them to select records from
// a DecodedFrame without magic numbers.
//
// Records selected through the VIF extension tables carry offset type codes
// so they can never collide with the primary table: 0x100 for the 0xFD table
// and 0x200 for the 0xFB table. The VIFExt constants below name the common
// 0xFD-table types.
const (
	VIFEnergyWh              = 0x07
	VIFEnergyJoule           = 0x0F
	VIFVolume                = 0x17
	VIFMass                  = 0x1F
	VIFOnTime                = 0x23
	VIFOperatingTime         = 0x27
	VIFPowerW                = 0x2F
	VIFPowerJoulePerHour     = 0x37
	VIFVolumeFlow            = 0x3F
	VIFVolumeFlowExt         = 0x47
	VIFVolumeFlowExtSmall    = 0x4F
	VIFMassFlow              = 0x57
	VIFFlowTemperature       = 0x5B
	VIFReturnTemperature     = 0x5F
	VIFTemperatureDifference = 0x63
	VIFExternalTemperature   = 0x67
	VIFPressure              = 0x6B
)

// VIF extension type codes (0xFD table), offset by 0x100 to keep them out of
// the primary VIF namespace. Before this offset an error-flags record decoded
// with the same Unit.Type as a volume record and MatchType could not tell
// them apart.
const (
	VIFExtAccessNumber    = 0x108
	VIFExtMedium          = 0x109
	VIFExtManufacturer    = 0x10A
	VIFExtParameterSetID  = 0x10B
	VIFExtModelVersion    = 0x10C
	VIFExtHardwareVersion = 0x10D
	VIFExtFirmwareVersion = 0x10E
	VIFExtSoftwareVersion = 0x10F
	VIFExtErrorFlags      = 0x117
	VIFExtErrorMask       = 0x118
	VIFExtDigitalOutput   = 0x11A
	VIFExtDigitalInput    = 0x11B
	VIFExtBaudrate        = 0x11C
	VIFExtVolts           = 0x14F
	VIFExtAmpere          = 0x15F
)

// Function descriptions as they appear in DecodedDataRecord.Function.
const (
	FunctionInstantaneous = "Instantaneous value"
	FunctionMaximum       = "Maximum value"
	FunctionMinimum       = "Minimum value"
	FunctionDuringError   = "Value during error state"
)
