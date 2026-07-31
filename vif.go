package gombus

// VIF type codes as they appear in Unit.Type on decoded data records. These
// mirror the primary VIF table (EN 13757-3); use them to select records from
// a DecodedFrame without magic numbers.
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

// Function descriptions as they appear in DecodedDataRecord.Function.
const (
	FunctionInstantaneous = "Instantaneous value"
	FunctionMaximum       = "Maximum value"
	FunctionMinimum       = "Minimum value"
	FunctionDuringError   = "Value during error state"
)
