package gombus

import (
	"testing"

	"go.bug.st/serial"
)

func TestSerialConfigValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		c       SerialConfig
		wantErr bool
	}{
		{"default", DefaultSerialConfig, false},
		{"zero baud", SerialConfig{DataBits: 8, Parity: serial.EvenParity, StopBits: serial.OneStopBit}, true},
		{"huge baud", SerialConfig{BaudRate: 4000001, DataBits: 8, Parity: serial.EvenParity, StopBits: serial.OneStopBit}, true},
		{"bad bits", SerialConfig{BaudRate: 2400, DataBits: 9, Parity: serial.EvenParity, StopBits: serial.OneStopBit}, true},
		{"bad parity", SerialConfig{BaudRate: 2400, DataBits: 8, Parity: 99, StopBits: serial.OneStopBit}, true},
		{"bad stops", SerialConfig{BaudRate: 2400, DataBits: 8, Parity: serial.EvenParity, StopBits: 99}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
