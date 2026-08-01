package main

import (
	"fmt"
	"math"
	"strconv"
)

// A meter can hold 1..250 as its own address. 0 marks an unconfigured slave and
// 251..255 are reserved.
const (
	minPrimaryAddr = 1
	maxPrimaryAddr = 250
	// addrSecondarySelect (0xFD) is where a meter answers after a secondary
	// selection. addrBroadcastReply (0xFE) is broadcast-with-reply, the
	// documented way to reach the only meter on a bus when its address is
	// unknown. Both are valid places to SEND a frame.
	addrSecondarySelect = 253
	addrBroadcastReply  = 254

	// secondaryDigits is the width of the BCD identification number.
	secondaryDigits = 8
	// maxSecondaryID is the largest number that fits in 8 BCD digits.
	maxSecondaryID = 99999999
)

// destinationAddr range-checks an address a frame will be SENT TO, so it also
// accepts 0xFD and 0xFE. The byte-range check comes before the narrowing
// conversion: converting first wraps silently, turning 300 into meter 44.
func destinationAddr(value int) (uint8, error) {
	if value < 0 || value > math.MaxUint8 {
		return 0, errBadDestination()
	}
	addr := uint8(value)
	valid := (addr >= minPrimaryAddr && addr <= maxPrimaryAddr) ||
		addr == addrSecondarySelect || addr == addrBroadcastReply
	if !valid {
		return 0, errBadDestination()
	}
	return addr, nil
}

func errBadDestination() error {
	return fmt.Errorf(
		"address to read from must be %d..%d, %d (secondary select) or %d (broadcast with reply)",
		minPrimaryAddr, maxPrimaryAddr, addrSecondarySelect, addrBroadcastReply,
	)
}

// assignableAddr range-checks an address that will be WRITTEN INTO a meter as
// its own. Stricter than destinationAddr on purpose: 0xFE is a fine destination
// but writing it into a meter makes that meter answer every broadcast, which
// cannot be undone over the bus.
func assignableAddr(value int) (uint8, error) {
	if value < minPrimaryAddr || value > maxPrimaryAddr {
		return 0, fmt.Errorf(
			"address written into a meter must be %d..%d", minPrimaryAddr, maxPrimaryAddr,
		)
	}
	return uint8(value), nil
}

// parseDestinationAddr checks a flag holding a destination address and names
// the flag in the error, because at a CLI boundary the operator needs to know
// which input was wrong.
func parseDestinationAddr(flagName string, value int) (uint8, error) {
	addr, err := destinationAddr(value)
	if err != nil {
		return 0, fmt.Errorf("invalid -%s %d: %w", flagName, value, err)
	}
	return addr, nil
}

// parseAssignableAddr checks a flag holding an address to write into a meter.
func parseAssignableAddr(flagName string, value int) (uint8, error) {
	addr, err := assignableAddr(value)
	if err != nil {
		return 0, fmt.Errorf("invalid -%s %d: %w", flagName, value, err)
	}
	return addr, nil
}

// target is the meter a command acts on: either a primary address or a
// secondary identification number.
type target struct {
	secondary bool
	primary   uint8
	id        uint64
}

func (t target) String() string {
	if t.secondary {
		return fmt.Sprintf("secondary %0*d", secondaryDigits, t.id)
	}
	return fmt.Sprintf("primary %d", t.primary)
}

// parseTarget reads a meter address from the command line. Exactly 8 digits is
// a secondary identification number, because that is how a meter prints it on
// its label and no primary address is that long. Anything shorter is a primary
// address unless forceSecondary says otherwise, which is how to reach a meter
// whose number was written down without its leading zeros.
func parseTarget(arg string, forceSecondary bool) (target, error) {
	if arg == "" {
		return target{}, usagef("missing meter address")
	}
	if !isDigits(arg) {
		return target{}, usagef("invalid address %q: must be digits only", arg)
	}

	if forceSecondary || len(arg) == secondaryDigits {
		id, err := strconv.ParseUint(arg, 10, 64)
		if err != nil {
			return target{}, usagef("invalid secondary address %q: %v", arg, err)
		}
		if id > maxSecondaryID {
			return target{}, usagef(
				"invalid secondary address %q: must fit in %d digits", arg, secondaryDigits,
			)
		}
		return target{secondary: true, id: id}, nil
	}

	value, err := strconv.Atoi(arg)
	if err != nil {
		return target{}, usagef("invalid primary address %q: %v", arg, err)
	}
	addr, err := destinationAddr(value)
	if err != nil {
		return target{}, usagef("invalid address %q: %v", arg, err)
	}
	return target{primary: addr}, nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
