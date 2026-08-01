package gombus

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// testProbeTimeout keeps a silent probe short. The fake bus answers instantly,
// so this only bounds the windows where nothing is meant to answer.
const testProbeTimeout = 20 * time.Millisecond

// fakeSlave is one meter on the fake bus.
type fakeSlave struct {
	primary uint8
	sec     SecondaryAddress
	frame   []byte // its variable data response
}

// fakeBus is an in-memory M-Bus segment. It parses the master's frames and
// answers as slaves would, including the byte-level mess two slaves replying at
// once make of each other's frames.
type fakeBus struct {
	slaves   []*fakeSlave
	selected []*fakeSlave
	pending  []byte
	writes   [][]byte
}

func (b *fakeBus) Read(p []byte) (int, error) {
	if len(b.pending) == 0 {
		return 0, os.ErrDeadlineExceeded
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *fakeBus) Write(p []byte) (int, error) {
	frame := append([]byte(nil), p...)
	b.writes = append(b.writes, frame)
	b.handle(frame)
	return len(p), nil
}

func (*fakeBus) SetReadDeadline(time.Time) error  { return nil }
func (*fakeBus) SetWriteDeadline(time.Time) error { return nil }
func (*fakeBus) Close() error                     { return nil }

func (b *fakeBus) handle(frame []byte) {
	switch {
	case len(frame) == 5 && frame[0] == 0x10:
		b.handleShort(frame[1], frame[2])
	case len(frame) > 6 && frame[0] == 0x68 && frame[6] == ciSelectSlave && frame[5] == addrSecondarySelect:
		b.handleSelect(frame)
	}
}

func (b *fakeBus) handleShort(c, addr byte) {
	targets := b.targets(addr)
	switch c {
	case 0x40: // SND_NKE
		for range targets {
			b.pending = append(b.pending, SingleCharacterFrame)
		}
	case 0x5b: // REQ_UD2
		switch len(targets) {
		case 0:
		case 1:
			b.pending = append(b.pending, targets[0].frame...)
		default:
			b.pending = append(b.pending, collide(targets[0].frame, targets[1].frame)...)
		}
	}
}

// targets resolves the destination of a short frame: a primary address reaches
// at most one slave, 0xFD reaches whatever the last selection matched.
func (b *fakeBus) targets(addr byte) []*fakeSlave {
	if addr == addrSecondarySelect {
		return b.selected
	}
	for _, s := range b.slaves {
		if s.primary == addr {
			return []*fakeSlave{s}
		}
	}
	return nil
}

func (b *fakeBus) handleSelect(frame []byte) {
	filter := secondaryFilter{
		id:           [4]byte{frame[7], frame[8], frame[9], frame[10]},
		manufacturer: [2]byte{frame[11], frame[12]},
		version:      frame[13],
		medium:       frame[14],
	}
	b.selected = nil
	for _, s := range b.slaves {
		if matchesFilter(s.sec, filter) {
			b.selected = append(b.selected, s)
		}
	}
	// Every matching slave acks at once. Two acks on the wire is what lets the
	// master tell a collision from a clean selection.
	for range b.selected {
		b.pending = append(b.pending, SingleCharacterFrame)
	}
}

// collide models two slaves transmitting over each other: the bytes of the
// first frame are cut short by the second, so nothing parses.
func collide(a, b []byte) []byte {
	out := append([]byte(nil), a[:len(a)-2]...)
	return append(out, b[:4]...)
}

func matchesFilter(sec SecondaryAddress, f secondaryFilter) bool {
	mask := bcdToMask(f.id)
	want := sec.Mask()
	for i := range mask {
		if mask[i] != maskWildcard && mask[i] != want[i] {
			return false
		}
	}
	if f.manufacturer != [2]byte{0xFF, 0xFF} {
		man, err := encodeManufacturer(sec.Manufacturer)
		if err != nil || man != f.manufacturer {
			return false
		}
	}
	if f.version != 0xFF && f.version != sec.Version {
		return false
	}
	return f.medium == 0xFF || f.medium == sec.Medium
}

// bcdToMask renders the 4 little-endian BCD bytes of an ID field as the 8-digit
// mask string, wildcards included. Inverse of maskToBCD.
func bcdToMask(id [4]byte) string {
	out := make([]byte, secondaryDigits)
	for i := range id {
		by := id[len(id)-1-i]
		out[2*i] = maskDigit(by >> 4)
		out[2*i+1] = maskDigit(by & 0x0F)
	}
	return string(out)
}

func maskDigit(nibble byte) byte {
	if nibble > 9 {
		return maskWildcard
	}
	return '0' + nibble
}

// newFakeSlave builds a slave whose variable data response carries the given
// secondary address in its identification header.
func newFakeSlave(t *testing.T, primary uint8, sec SecondaryAddress) *fakeSlave {
	t.Helper()
	id, err := uintToBCD(sec.ID, 4)
	if err != nil {
		t.Fatalf("encoding id %d: %v", sec.ID, err)
	}
	man, err := encodeManufacturer(sec.Manufacturer)
	if err != nil {
		t.Fatalf("encoding manufacturer %q: %v", sec.Manufacturer, err)
	}

	frame := LongFrame{
		0x68, 0x00, 0x00, 0x68,
		0x08, primary, 0x72,
		id[0], id[1], id[2], id[3],
		man[0], man[1],
		sec.Version, sec.Medium,
		0x00,       // access number
		0x00,       // status
		0x00, 0x00, // signature
		0x0C, 0x13, 0x00, 0x00, 0x00, 0x00, // one BCD volume record
		0x00, 0x16,
	}
	frame.SetLength()
	frame.SetChecksum()
	return &fakeSlave{primary: primary, sec: sec, frame: frame}
}

func newBusClient(bus *fakeBus) *Client {
	client := NewClient(bus)
	client.probeTimeout = testProbeTimeout
	return client
}

func TestScanPrimary(t *testing.T) {
	cases := []struct {
		name      string
		primaries []uint8
		from, to  uint8
		want      []uint8
	}{
		{name: "no slaves", primaries: nil, from: 1, to: 3},
		{
			name:      "slaves inside the range are found and gaps skipped",
			primaries: []uint8{3, 5},
			from:      1, to: 6,
			want: []uint8{3, 5},
		},
		{
			name:      "slaves outside the range are not reported",
			primaries: []uint8{3, 9},
			from:      1, to: 4,
			want: []uint8{3},
		},
		{
			name:      "single address range still probes once",
			primaries: []uint8{7},
			from:      7, to: 7,
			want: []uint8{7},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &fakeBus{}
			for i, p := range tc.primaries {
				sec := SecondaryAddress{ID: uint64(1000 + i), Manufacturer: "ABC", Version: 1, Medium: 0x07}
				bus.slaves = append(bus.slaves, newFakeSlave(t, p, sec))
			}

			got, err := newBusClient(bus).ScanPrimary(t.Context(), tc.from, tc.to)
			if err != nil {
				t.Fatalf("ScanPrimary: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("found %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("found %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestScanPrimaryRejectsBadRange(t *testing.T) {
	cases := []struct {
		name     string
		from, to uint8
		wantErr  error
	}{
		{name: "zero start", from: 0, to: 10, wantErr: ErrInvalidPrimaryID},
		{name: "reserved end", from: 1, to: 251, wantErr: ErrInvalidPrimaryID},
		{name: "broadcast end", from: 1, to: 255, wantErr: ErrInvalidPrimaryID},
		{name: "reversed range", from: 10, to: 2, wantErr: ErrInvalidScanRange},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &fakeBus{}
			if _, err := newBusClient(bus).ScanPrimary(t.Context(), tc.from, tc.to); !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected errors.Is(err, %v), got %v", tc.wantErr, err)
			}
			if len(bus.writes) != 0 {
				t.Fatalf("wrote %d frame(s) for an invalid range, want 0", len(bus.writes))
			}
		})
	}
}

func TestScanPrimaryHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	bus := &fakeBus{}
	if _, err := newBusClient(bus).ScanPrimary(ctx, 1, 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected errors.Is(err, context.Canceled), got %v", err)
	}
}

func TestSelectMask(t *testing.T) {
	ids := []uint64{11111111, 12345678}
	cases := []struct {
		name          string
		mask          string
		wantResponded bool
		wantCollision bool
	}{
		{name: "exact match acks", mask: "12345678", wantResponded: true},
		{name: "unique prefix acks", mask: "1234FFFF", wantResponded: true},
		{name: "shared prefix collides", mask: "1FFFFFFF", wantCollision: true},
		{name: "all wildcards collide", mask: "FFFFFFFF", wantCollision: true},
		{name: "no match is silent", mask: "9FFFFFFF"},
		{name: "lowercase wildcard is accepted", mask: "1234ffff", wantResponded: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &fakeBus{}
			for i, id := range ids {
				sec := SecondaryAddress{ID: id, Manufacturer: "ABC", Version: 1, Medium: 0x07}
				bus.slaves = append(bus.slaves, newFakeSlave(t, uint8(i+1), sec))
			}

			responded, collision, err := newBusClient(bus).selectMask(t.Context(), tc.mask)
			if err != nil {
				t.Fatalf("selectMask(%q): %v", tc.mask, err)
			}
			if responded != tc.wantResponded || collision != tc.wantCollision {
				t.Fatalf(
					"selectMask(%q) = responded %v, collision %v; want %v, %v",
					tc.mask, responded, collision, tc.wantResponded, tc.wantCollision,
				)
			}
		})
	}
}

func TestSelectMaskRejectsBadMask(t *testing.T) {
	for _, mask := range []string{"", "1234567", "123456789", "1234567X"} {
		t.Run(mask, func(t *testing.T) {
			bus := &fakeBus{}
			_, _, err := newBusClient(bus).selectMask(t.Context(), mask)
			if !errors.Is(err, ErrInvalidSecondaryMask) {
				t.Fatalf("expected errors.Is(err, ErrInvalidSecondaryMask), got %v", err)
			}
			if len(bus.writes) != 0 {
				t.Fatalf("wrote %d frame(s) for an invalid mask, want 0", len(bus.writes))
			}
		})
	}
}

// TestScanSecondaryResolvesCollision covers the point of the wildcard search:
// two meters sharing the leading digit answer the same mask, and the search has
// to fix one more digit to tell them apart.
func TestScanSecondaryResolvesCollision(t *testing.T) {
	want := []SecondaryAddress{
		{ID: 11111111, Manufacturer: "ABC", Version: 1, Medium: 0x07},
		{ID: 12345678, Manufacturer: "XYZ", Version: 2, Medium: 0x04},
	}
	bus := &fakeBus{}
	for i, sec := range want {
		bus.slaves = append(bus.slaves, newFakeSlave(t, uint8(i+1), sec))
	}

	got, err := newBusClient(bus).ScanSecondary(t.Context())
	if err != nil {
		t.Fatalf("ScanSecondary: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("found %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slave %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestScanSecondaryEmptyBus(t *testing.T) {
	got, err := newBusClient(&fakeBus{}).ScanSecondary(t.Context())
	if err != nil {
		t.Fatalf("ScanSecondary: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("found %+v on an empty bus, want none", got)
	}
}

func TestSelectSecondaryErrors(t *testing.T) {
	present := SecondaryAddress{ID: 12345678, Manufacturer: "ABC", Version: 1, Medium: 0x07}
	twin := SecondaryAddress{ID: 12345678, Manufacturer: "XYZ", Version: 3, Medium: 0x04}

	cases := []struct {
		name    string
		slaves  []SecondaryAddress
		want    SecondaryAddress
		wantErr error
	}{
		{name: "unique slave selects", slaves: []SecondaryAddress{present}, want: present},
		{
			name:    "absent slave",
			slaves:  []SecondaryAddress{present},
			want:    SecondaryAddress{ID: 87654321, Manufacturer: "ABC"},
			wantErr: ErrSelectNoAnswer,
		},
		{
			name:    "same id at two manufacturers collides without a manufacturer filter",
			slaves:  []SecondaryAddress{present, twin},
			want:    SecondaryAddress{ID: 12345678},
			wantErr: ErrSelectCollision,
		},
		{
			name:   "manufacturer disambiguates the twin",
			slaves: []SecondaryAddress{present, twin},
			want:   twin,
		},
		{
			name:    "bad manufacturer code",
			slaves:  []SecondaryAddress{present},
			want:    SecondaryAddress{ID: 12345678, Manufacturer: "ab"},
			wantErr: ErrInvalidManufacturer,
		},
		{
			name:    "id wider than 8 digits",
			slaves:  []SecondaryAddress{present},
			want:    SecondaryAddress{ID: 123456789, Manufacturer: "ABC"},
			wantErr: ErrInvalidSecondaryMask,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &fakeBus{}
			for i, sec := range tc.slaves {
				bus.slaves = append(bus.slaves, newFakeSlave(t, uint8(i+1), sec))
			}

			err := newBusClient(bus).SelectSecondary(t.Context(), tc.want)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected errors.Is(err, %v), got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SelectSecondary: %v", err)
			}
			if len(bus.selected) != 1 || bus.selected[0].sec != tc.want {
				t.Fatalf("bus selected %+v, want exactly %+v", bus.selected, tc.want)
			}
		})
	}
}

// TestReadBySecondary reads a captured meter frame through the select-then-read
// path, so the identification header the scan reports and the frame the read
// returns have to describe the same meter.
func TestReadBySecondary(t *testing.T) {
	raw := loadHexFixture(t, "testdata/frames/ACW_Itron-BM-plus-m.hex")
	sec, err := secondaryFromHeader(raw)
	if err != nil {
		t.Fatalf("reading the fixture header: %v", err)
	}

	bus := &fakeBus{slaves: []*fakeSlave{{primary: 8, sec: sec, frame: raw}}}
	client := newBusClient(bus)

	frame, err := client.ReadBySecondary(t.Context(), sec)
	if err != nil {
		t.Fatalf("ReadBySecondary: %v", err)
	}
	if uint64(frame.SerialNumber) != sec.ID {
		t.Fatalf("read serial %d, want %d", frame.SerialNumber, sec.ID)
	}
	if frame.Manufacturer != sec.Manufacturer {
		t.Fatalf("read manufacturer %q, want %q", frame.Manufacturer, sec.Manufacturer)
	}
	if len(frame.DataRecords) == 0 {
		t.Fatal("expected the fixture's data records to survive the read")
	}

	// Two frames on the wire: the selection, then the REQ_UD2 at 0xFD.
	if len(bus.writes) != 2 {
		t.Fatalf("wrote %d frame(s), want 2", len(bus.writes))
	}
	if got := bus.writes[0][6]; got != ciSelectSlave {
		t.Fatalf("first frame CI is 0x%02x, want 0x%02x", got, ciSelectSlave)
	}
	if got := bus.writes[1][2]; got != addrSecondarySelect {
		t.Fatalf("REQ_UD2 addressed to %d, want %d", got, addrSecondarySelect)
	}
}

func TestMaskToBCD(t *testing.T) {
	cases := []struct {
		name string
		mask string
		want [4]byte
	}{
		{name: "digits are little endian", mask: "12345678", want: [4]byte{0x78, 0x56, 0x34, 0x12}},
		{name: "all wildcards", mask: "FFFFFFFF", want: [4]byte{0xFF, 0xFF, 0xFF, 0xFF}},
		{name: "leading zeroes are kept", mask: "00001234", want: [4]byte{0x34, 0x12, 0x00, 0x00}},
		{name: "partial wildcard", mask: "1234FFFF", want: [4]byte{0xFF, 0xFF, 0x34, 0x12}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := maskToBCD(tc.mask)
			if err != nil {
				t.Fatalf("maskToBCD(%q): %v", tc.mask, err)
			}
			if got != tc.want {
				t.Fatalf("maskToBCD(%q) = % x, want % x", tc.mask, got, tc.want)
			}
			if round := bcdToMask(got); round != tc.mask {
				t.Fatalf("round trip of %q gave %q", tc.mask, round)
			}
		})
	}
}

// TestEncodeManufacturerRoundTrip pins the encoder against the decoder the rest
// of the package already uses, so a selection cannot filter on a manufacturer
// code that differs from the one a scan reported.
func TestEncodeManufacturerRoundTrip(t *testing.T) {
	for _, code := range []string{"ABC", "ACW", "EFE", "ZZZ", "AAA"} {
		t.Run(code, func(t *testing.T) {
			man, err := encodeManufacturer(code)
			if err != nil {
				t.Fatalf("encodeManufacturer(%q): %v", code, err)
			}
			frame := make(LongFrame, 13)
			copy(frame[11:], man[:])
			got, err := frame.DecodeManufacturer()
			if err != nil {
				t.Fatalf("DecodeManufacturer: %v", err)
			}
			if got != code {
				t.Fatalf("round trip of %q gave %q", code, got)
			}
		})
	}
}

func TestEncodeManufacturerRejectsBadCodes(t *testing.T) {
	for _, code := range []string{"", "AB", "ABCD", "abc", "A1C"} {
		t.Run(code, func(t *testing.T) {
			if _, err := encodeManufacturer(code); !errors.Is(err, ErrInvalidManufacturer) {
				t.Fatalf("expected errors.Is(err, ErrInvalidManufacturer), got %v", err)
			}
		})
	}
}
