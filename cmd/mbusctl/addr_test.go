package main

import (
	"errors"
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

// TestParseTarget covers the auto-detection that decides whether an operator
// typed a primary address or a secondary id. Getting it wrong addresses the
// wrong meter, so the length rule and its override are pinned here.
func TestParseTarget(t *testing.T) {
	cases := []struct {
		name          string
		arg           string
		forceSecond   bool
		wantErr       bool
		wantSecondary bool
		wantPrimary   uint8
		wantID        uint64
	}{
		{name: "short number is primary", arg: "1", wantPrimary: 1},
		{name: "highest primary", arg: "250", wantPrimary: 250},
		{name: "secondary select is a valid destination", arg: "253", wantPrimary: 253},
		{name: "broadcast with reply is a valid destination", arg: "254", wantPrimary: 254},
		{name: "eight digits is a secondary id", arg: "12345678", wantSecondary: true, wantID: 12345678},
		{
			name:          "leading zeros keep the eight digit rule",
			arg:           "00001234",
			wantSecondary: true,
			wantID:        1234,
		},
		{
			name:          "flag forces a short number to secondary",
			arg:           "1234",
			forceSecond:   true,
			wantSecondary: true,
			wantID:        1234,
		},
		{name: "empty", arg: "", wantErr: true},
		{name: "not a number", arg: "1a", wantErr: true},
		{name: "negative sign is not a digit", arg: "-1", wantErr: true},
		{name: "zero is unconfigured, not readable", arg: "0", wantErr: true},
		{name: "251 reserved", arg: "251", wantErr: true},
		{name: "255 broadcast without reply", arg: "255", wantErr: true},
		{name: "300 must not wrap to meter 44", arg: "300", wantErr: true},
		{name: "nine digits does not fit in BCD", arg: "123456789", forceSecond: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTarget(tc.arg, tc.forceSecond)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseTarget(%q, %v): expected an error, got %v", tc.arg, tc.forceSecond, got)
				}
				// Bad input on the command line must exit 2, not look like a
				// bus failure.
				var ue *usageError
				if !errors.As(err, &ue) {
					t.Errorf("parseTarget(%q): error must be a usage error, got %T", tc.arg, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTarget(%q, %v): unexpected error: %v", tc.arg, tc.forceSecond, err)
			}
			if got.secondary != tc.wantSecondary {
				t.Fatalf("parseTarget(%q): secondary = %v, want %v", tc.arg, got.secondary, tc.wantSecondary)
			}
			if got.primary != tc.wantPrimary {
				t.Errorf("parseTarget(%q): primary = %d, want %d", tc.arg, got.primary, tc.wantPrimary)
			}
			if got.id != tc.wantID {
				t.Errorf("parseTarget(%q): id = %d, want %d", tc.arg, got.id, tc.wantID)
			}
		})
	}
}

// TestTargetString pins the operator-facing description, which is what an error
// message names when a read fails.
func TestTargetString(t *testing.T) {
	if got := (target{primary: 7}).String(); got != "primary 7" {
		t.Errorf("primary target: got %q", got)
	}
	if got := (target{secondary: true, id: 1234}).String(); got != "secondary 00001234" {
		t.Errorf("secondary target: got %q", got)
	}
}
