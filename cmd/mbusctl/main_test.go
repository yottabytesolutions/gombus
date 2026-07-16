package main

import (
	"strings"
	"testing"
)

// The two parsers are the guard against talking to the wrong meter, so they are
// tested against each other in one table: the same address is legal to read from
// and illegal to write, and the pair only makes sense side by side.
//
// wantDestErr covers parseDestinationAddr (where a frame is SENT).
// wantAssignErr covers parseAssignableAddr (what is WRITTEN INTO a meter).
func TestParseAddrFlags(t *testing.T) {
	cases := []struct {
		name          string
		value         int
		wantDestErr   bool
		wantAssignErr bool
	}{
		{name: "negative flag default", value: -1, wantDestErr: true, wantAssignErr: true},
		{name: "zero is unconfigured", value: 0, wantDestErr: true, wantAssignErr: true},
		{name: "lowest meter address", value: 1},
		{name: "typical", value: 42},
		{name: "highest meter address", value: 250},
		{name: "251 reserved", value: 251, wantDestErr: true, wantAssignErr: true},
		{name: "252 reserved", value: 252, wantDestErr: true, wantAssignErr: true},
		{
			name:          "253 secondary select reads fine, never written",
			value:         253,
			wantAssignErr: true,
		},
		{
			name:          "254 broadcast-with-reply reads fine, bricks the meter if written",
			value:         254,
			wantAssignErr: true,
		},
		{
			name:          "255 broadcast without reply can never answer",
			value:         255,
			wantDestErr:   true,
			wantAssignErr: true,
		},
		{
			// The original bug: uint8(300) is 44, so -addr 300 silently read a
			// different meter. Both parsers must reject it, not wrap it.
			name:          "300 must not wrap to meter 44",
			value:         300,
			wantDestErr:   true,
			wantAssignErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDestinationAddr("addr", tc.value)
			assertParse(t, "parseDestinationAddr", "addr", tc.value, got, err, tc.wantDestErr)

			got, err = parseAssignableAddr("new-primary", tc.value)
			assertParse(t, "parseAssignableAddr", "new-primary", tc.value, got, err, tc.wantAssignErr)
		})
	}
}

// assertParse checks the error contract and, on success, that the value round
// trips instead of wrapping.
func assertParse(t *testing.T, fn, flagName string, value int, got uint8, err error, wantErr bool) {
	t.Helper()

	if wantErr {
		if err == nil {
			t.Fatalf("%s(%d): expected an error, got address %d", fn, value, got)
		}
		// The message is the whole point at a CLI boundary: it must tell the
		// operator which flag was wrong and what is acceptable.
		if !strings.Contains(err.Error(), "-"+flagName) {
			t.Errorf("%s(%d): error must name the flag -%s, got %q", fn, value, flagName, err)
		}
		if !strings.Contains(err.Error(), "1..250") {
			t.Errorf("%s(%d): error must name the valid range, got %q", fn, value, err)
		}
		return
	}

	if err != nil {
		t.Fatalf("%s(%d): unexpected error: %v", fn, value, err)
	}
	if int(got) != value {
		t.Fatalf("%s(%d): got address %d, want %d", fn, value, got, value)
	}
}

// TestParseDestinationAddrNamesExtraAddresses pins that the destination error
// tells the operator about 253 and 254. Without it, the single-meter workflow
// the -addr flag documents is undiscoverable from the error alone.
func TestParseDestinationAddrNamesExtraAddresses(t *testing.T) {
	_, err := parseDestinationAddr("addr", 251)
	if err == nil {
		t.Fatal("expected an error for 251")
	}
	for _, want := range []string{"253", "254"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("destination error must mention %s, got %q", want, err)
		}
	}
}
