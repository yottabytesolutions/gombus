package gombus

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CI fields LongFrame.Decode understands. Both are mode 1 (LSB first); the
// mode 2 responses 0x76 and 0x77 send multibyte values MSB first and are not
// decoded.
const (
	ciVariableDataMode1 = 0x72
	ciFixedDataMode1    = 0x73
)

// ErrUnsupportedCI is returned by LongFrame.Decode when the CI field is neither
// 0x72 (variable data response) nor 0x73 (fixed data response).
var ErrUnsupportedCI = errors.New(
	"unsupported CI field (only 0x72 variable and 0x73 fixed data responses are supported)",
)

// errShortDataRecord is wrapped by every truncation error during data-record
// decoding so a caller can errors.Is against ErrInvalidFrame for the broader
// "frame is malformed" condition.
var errShortDataRecord = errors.New("data record truncated")

// DecodedDataRecord is one record from the variable-data response.
type DecodedDataRecord struct {
	Function      string
	StorageNumber int
	Tariff        int
	Device        int

	Unit     Unit
	Exponent float64
	Type     string
	Quantity string

	Value       float64
	ValueString string
	RawValue    float64

	// ValueErr reports that this record's interpreted value could not be
	// produced: a BCD field holding a manufacturer's "not available" filler, or
	// a date the meter never meant. When it is non-nil, Value and Time are zero
	// and mean nothing. It does NOT mean the meter read zero or that the epoch
	// is a real timestamp.
	//
	// RawValue still holds the raw field bits when they were readable, which is
	// the case for a date: only the interpretation failed. It is zero when the
	// encoding itself was undecodable, as with invalid BCD. The rest of the
	// record (unit, storage number, tariff, device) is always valid.
	//
	// Only the value is affected. An invalid value does not cost us sync,
	// because the DIF gives the field width, so the surrounding records decode
	// normally and the frame still decodes. A structural error does cost us
	// sync and fails the whole frame instead.
	ValueErr error

	// Time is the timestamp of a date (VIF 0x6C) or date/time (VIF 0x6D)
	// record. It is the METER'S WALL CLOCK and carries no zone information.
	// The location is UTC only so the value is deterministic and comparable,
	// NOT because the meter claims UTC: the meter does not say what zone it
	// keeps. Do not convert it between zones and expect meaning.
	//
	// It is zero when the record is not a date, and when ValueErr is set. The
	// raw bitfield is still in RawValue either way.
	Time time.Time

	// SummerTimeFlag is the type F summer-time (SU) bit exactly as the meter
	// sent it. It is not a timezone and it does not shift Time. Interpreting it
	// needs knowledge of the meter's local rules that the frame does not carry.
	SummerTimeFlag bool

	HasMoreRecords bool
}

// DecodedFrame is the high-level result of decoding a variable-data response.
type DecodedFrame struct {
	raw          []byte
	SerialNumber int
	Manufacturer string
	ProductName  string
	Version      int
	DeviceType   string
	AccessNumber byte
	Signature    uint16
	Status       byte

	DataRecords []DecodedDataRecord
}

// Decode parses a variable-data response (CI=0x72) or a fixed-data response
// (CI=0x73). It returns ErrUnsupportedCI for any other CI field, and
// ErrInvalidFrame (wrapped) for bounds or consistency violations.
func (lf LongFrame) Decode() (*DecodedFrame, error) {
	// Variable-data response header (after the 4-byte 68 LL LL 68 start) is:
	//   C(1) A(1) CI(1) | ID(4) Mfr(2) Ver(1) Med(1) AN(1) Status(1) Sig(2)
	// = 15 bytes of header, then payload, then checksum(1) + stop(1).
	// Smallest possible long frame is therefore 15 + 4 + 2 = 21 bytes.
	if len(lf) < 21 {
		return nil, fmt.Errorf("%w: long frame too short (%d bytes)", ErrInvalidFrame, len(lf))
	}
	switch lf.CI() {
	case ciVariableDataMode1:
		// Decoded below; the fixed structure shares none of that code.
	case ciFixedDataMode1:
		return lf.decodeFixed()
	default:
		return nil, ErrUnsupportedCI
	}

	man, err := lf.DecodeManufacturer()
	if err != nil {
		return nil, err
	}
	dr, err := lf.decodeData(lf[19 : len(lf)-2])
	if err != nil {
		return nil, err
	}
	// A serial number is an unsigned identifier, so it decodes as a magnitude:
	// via bcdSigned an 0xF top nibble would yield a negative serial number.
	// An undecodable serial fails the whole frame rather than surfacing a junk
	// identifier. Every reading in the frame is attributed to this number, so a
	// wrong one mis-attributes data to another meter. Four BCD bytes hold at
	// most 99999999, so the conversion to int cannot wrap.
	serial, err := bcdMagnitude(lf[7:11])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid serial number: %w", ErrInvalidFrame, err)
	}

	return &DecodedFrame{
		raw:          lf,
		SerialNumber: int(serial),
		Manufacturer: man,
		ProductName:  extractProductName(man, dr),
		Version:      int(lf[13]),
		DeviceType:   deviceTypeLookup(lf[14]),
		AccessNumber: lf[15],
		Status:       lf[16],
		Signature:    binary.LittleEndian.Uint16(lf[17:19]),
		DataRecords:  dr,
	}, nil
}

// DecodeManufacturer extracts the 3-letter manufacturer ID from the two
// little-endian bytes at offset 11. It returns ErrInvalidFrame (wrapped) when
// the frame is too short to hold them.
func (lf LongFrame) DecodeManufacturer() (string, error) {
	const manufacturerEnd = 13
	if len(lf) < manufacturerEnd {
		return "", fmt.Errorf(
			"%w: need %d bytes for the manufacturer ID, have %d",
			ErrInvalidFrame, manufacturerEnd, len(lf),
		)
	}
	id := int(binary.LittleEndian.Uint16(lf[11:manufacturerEnd]))
	return string(
		[]rune{
			rune(((id >> 10) & 0x1F) + 64),
			rune(((id >> 5) & 0x1F) + 64),
			rune((id & 0x1F) + 64),
		},
	), nil
}

// HasMoreRecords reports whether the last decoded record carries the
// "more records follow" sentinel (DIF=0x1F).
func (df DecodedFrame) HasMoreRecords() bool {
	if len(df.DataRecords) == 0 {
		return false
	}
	return df.DataRecords[len(df.DataRecords)-1].HasMoreRecords
}

// SecondaryAddressString returns "<serial><mfr>" as a hex string suitable
// for secondary addressing. Returns just the serial number when the raw
// frame is too short to contain the manufacturer bytes.
func (df DecodedFrame) SecondaryAddressString() string {
	if len(df.raw) < 14 {
		return strconv.Itoa(df.SerialNumber)
	}
	return strconv.Itoa(df.SerialNumber) + hex.EncodeToString(df.raw[11:14])
}

// statusBitDescriptions list the EN 13757-3 §6.6 application status bits.
// Index = bit position; names for bits 5..7 are manufacturer-specific.
var statusBitDescriptions = [8]string{
	"Application busy",
	"Application error",
	"Power low",
	"Permanent error",
	"Temporary error",
	"Specific to manufacturer",
	"Specific to manufacturer",
	"Specific to manufacturer",
}

// ReadableStatus returns a comma-separated description of the status bits,
// or "Normal operation" when no bits are set.
func (df DecodedFrame) ReadableStatus() string {
	var on []string
	for i, name := range statusBitDescriptions {
		if df.Status&(1<<i) != 0 {
			on = append(on, name)
		}
	}
	if len(on) == 0 {
		return "Normal operation"
	}
	return strings.Join(on, ", ")
}

// extractProductName picks a best-effort product name out of decoded data
// records. It looks for an ASCII record whose declared unit hints at customer
// or product identification. For Kamstrup ("KAM") it additionally matches the
// legacy "Multical NNN" pattern (a numeric value in 2.1M..2.2M with unit
// "none"). It does not attempt to map Kamstrup register IDs (per the
// Kamstrup Logger Profiles document register IDs like 404/1001/393/346 are
// application-level identifiers, not encoded in M-Bus storage numbers).
func extractProductName(manufacturer string, records []DecodedDataRecord) string {
	for _, r := range records {
		if r.ValueString == "" {
			continue
		}
		u := strings.ToLower(r.Unit.Unit)
		if u == "cust. id" || strings.Contains(u, "product") || strings.Contains(u, "model") {
			return r.ValueString
		}
	}
	if manufacturer == "KAM" {
		for _, r := range records {
			if r.Unit.Unit == "none" && r.Value >= 2100000 && r.Value < 2200000 {
				return fmt.Sprintf("Multical %d", int(r.Value))
			}
		}
	}
	return ""
}

// need verifies that data has at least n bytes left starting at i. It is
// called before every fixed-size slice in decodeData / decodeLVAR so a
// truncated record returns an error instead of panicking.
func need(data []byte, i, n int) error {
	if i+n > len(data) {
		return fmt.Errorf(
			"%w: need %d bytes at offset %d, have %d",
			errShortDataRecord, n, i, len(data),
		)
	}
	return nil
}

// decodeData walks the user-data area of a long frame and emits one
// DecodedDataRecord per data record. Special-function DIFs 0x0F / 0x1F end
// parsing (the rest of the frame is opaque manufacturer-specific data); 0x2F
// is an idle filler and is skipped.
//
// The walk is index-driven: decodeRecord reports exactly how many bytes it
// read, so a record with no data bytes cannot swallow the next record's DIF.
func (lf LongFrame) decodeData(data []byte) ([]DecodedDataRecord, error) {
	records := make([]DecodedDataRecord, 0)

	for i := 0; i < len(data); {
		switch data[i] {
		// 0Fh / 1Fh: start of manufacturer-specific data (1Fh additionally
		// signals that more records follow in the next telegram). Per
		// EN 13757-3 §6.2 everything from this byte on is opaque, so we
		// stop parsing standard records.
		case 0x0f, 0x1f:
			return append(records, DecodedDataRecord{
				Function:       decodeRecordFunction(data[i]),
				StorageNumber:  int(data[i]) & DataRecordDifMaskStorageNo,
				HasMoreRecords: data[i] == 0x1f,
			}), nil
		// 2Fh: idle filler; next byte is a DIF.
		case 0x2f:
			i++
			continue
		}

		record, n, err := decodeRecord(data, i)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
		i += n
	}

	return records, nil
}

// maxExtensions caps the DIFE and VIFE chains. The spec allows 10 of each.
const maxExtensions = 10

// readExtensions collects the DIFE or VIFE chain starting at i: bytes with the
// extension bit set are followed by another extension byte. kind names the
// chain for the error message. It returns the bytes read.
func readExtensions(data []byte, i int, kind string) ([]byte, error) {
	var ext []byte
	for {
		if len(ext) >= maxExtensions {
			return nil, fmt.Errorf("too many %s extensions (max %d)", kind, maxExtensions)
		}
		if err := need(data, i, 1); err != nil {
			return nil, err
		}
		ext = append(ext, data[i])
		if !checkKthBitSet(int(data[i]), 7) {
			return ext, nil
		}
		i++
	}
}

// decodeRecord decodes the single data record starting at data[start], whose
// DIF is neither a special-function byte nor an idle filler. It returns the
// record and the number of bytes it read, so the caller advances by exactly
// the record length. A record running past the end of data is an error rather
// than a silently dropped tail.
func decodeRecord(data []byte, start int) (DecodedDataRecord, int, error) {
	var record DecodedDataRecord

	i := start
	dif := data[i]
	i++
	record.Function = decodeRecordFunction(dif)

	var dife []byte
	if checkKthBitSet(int(dif), 7) {
		var err error
		dife, err = readExtensions(data, i, "DIFE")
		if err != nil {
			return record, 0, err
		}
		i += len(dife)
	}

	if err := need(data, i, 1); err != nil {
		return record, 0, err
	}
	vif := data[i]
	i++

	// Plain-text VIF: VIF & 0x7F == 0x7C. The next byte is LVAR, followed by
	// LVAR ASCII characters (LSB-first / reversed). 0x7C has no extension;
	// 0xFC carries the extension bit so a VIFE follows the ASCII section.
	customUnit, hasCustomUnit := "", false
	if vif&DibVifWithoutExtension == 0x7c {
		if err := need(data, i, 1); err != nil {
			return record, 0, err
		}
		length := int(data[i])
		i++
		if err := need(data, i, length); err != nil {
			return record, 0, err
		}
		customUnit, hasCustomUnit = decodeASCII(data[i:i+length]), true
		i += length
	}

	var vife []byte
	if checkKthBitSet(int(vif), 7) {
		var err error
		vife, err = readExtensions(data, i, "VIFE")
		if err != nil {
			return record, 0, err
		}
		i += len(vife)
	}

	if hasCustomUnit {
		record.Unit = customUnitOf(vif, vife, customUnit)
	} else {
		record.Unit = decodeUnit(vif, vife)
	}
	record.StorageNumber = decodeStorageNumber(int(dif), dife)
	record.Device = decodeDevice(dife)
	record.Tariff = decodeTariff(dife)

	n, err := decodeRecordValue(data, i, dif, &record)
	if err != nil {
		return record, 0, err
	}
	i += n
	record.Value = record.Unit.Value(record.RawValue)
	return record, i - start, nil
}

// customUnitOf builds the unit of a plain-text VIF record. The ASCII section
// names the unit, but it does not carry the scale: for VIF 0xFC the extension
// bit is set and the VIFE that follows the ASCII still holds the multiplier, so
// the exponent comes from the VIFE rather than being assumed to be 1. Dropping
// it reported an Elvaco CMa10's humidity as 5410 %RH instead of 54.10.
//
// VIF 0x7C has no extension and so no VIFE, leaving the reading unscaled.
func customUnitOf(vif byte, vife []byte, customUnit string) Unit {
	unit := Unit{Exp: 1, Unit: customUnit, Type: vifUnit["VARIABLE_VIF"]}
	if checkKthBitSet(int(vif), 7) {
		unit.Exp = decodeUnit(vif, vife).Exp
	}
	return unit
}

// difIntegerWidths maps the DIF data-field codes carrying a binary integer to
// their width in bytes. 0x05 (32-bit real) and 0x0d (LVAR) are not integers.
var difIntegerWidths = map[byte]int{
	0x01: 1, 0x02: 2, 0x03: 3, 0x04: 4, 0x06: 6, 0x07: 8,
}

// difBCDWidths maps the DIF data-field codes carrying BCD to their width in
// bytes. Each byte holds two digits.
var difBCDWidths = map[byte]int{
	0x09: 1, 0x0a: 2, 0x0b: 3, 0x0c: 4, 0x0e: 6,
}

// decodeRecordValue reads the data bytes of one record into record, and reports
// how many bytes it read. Data fields 0x00 (no data), 0x08 (selection for
// readout) and 0x0f (sentinel) carry no data and read nothing.
func decodeRecordValue(data []byte, i int, dif byte, record *DecodedDataRecord) (int, error) {
	field := dif & DataRecordDifMaskData

	if width, ok := difIntegerWidths[field]; ok {
		if err := need(data, i, width); err != nil {
			return 0, err
		}
		record.RawValue = integerRawValue(data[i:i+width], record.Unit.Raw)
		if record.Unit.Date != DateNone {
			decodeRecordTime(data[i:i+width], record)
		}
		return width, nil
	}
	if width, ok := difBCDWidths[field]; ok {
		if err := need(data, i, width); err != nil {
			return 0, err
		}
		// The sign of a DIF BCD field lives in its most significant nibble, so
		// bcdSigned owns it. LVAR BCD is the other way round; see decodeLVAR.
		v, err := bcdSigned(data[i : i+width])
		if err != nil {
			// Undecodable BCD is a bad value, not a bad frame: the DIF gave us
			// the width, so the next record is exactly width bytes on. Real
			// meters fill error-state registers with a not-available pattern,
			// and failing the frame would drop every other reading with it.
			record.ValueErr = fmt.Errorf("BCD data field: %w", err)
			return width, nil
		}
		record.RawValue = float64(v)
		return width, nil
	}

	switch field {
	case 0x00, 0x08: // no data / selection for readout
		return 0, nil
	case 0x05: // 32-bit real
		if err := need(data, i, 4); err != nil {
			return 0, err
		}
		f, err := bytesToFloat32(data[i : i+4])
		if err != nil {
			return 0, err
		}
		record.RawValue = float64(f)
		return 4, nil
	case 0x0d: // variable length (LVAR)
		// decodeLVAR reports payload bytes; the LVAR byte itself is ours.
		n, err := decodeLVAR(data, i, record)
		if err != nil {
			return 0, err
		}
		return 1 + n, nil
	case 0x0f:
		// Reached only for DIFs whose data field is 1111 but which are not the
		// special bytes 0x0F / 0x1F / 0x2F (e.g. with the storage bit set).
		// Treat as a sentinel record.
		record.Function = "Special Function"
		return 0, nil
	default:
		return 0, fmt.Errorf("unhandled DIF data field 0x%x", field)
	}
}

// integerRawValue reads a binary integer data field. EN 13757-3 data fields
// 0x01-0x07 are type B, two's complement, so they sign-extend. Entries the unit
// table marks Raw carry a bitfield instead of a number and read as raw bits.
func integerRawValue(b []byte, raw bool) float64 {
	if raw {
		return float64(uintLE(b))
	}
	return float64(intLE(b))
}

// decodeLVAR decodes a variable-length data record per M-Bus DIF=0x0d.
// LVAR is at data[i]; payload starts at data[i+1]. Returns the number of
// payload bytes read, excluding the LVAR byte itself. Byte count, not digit
// count: the BCD ranges encode twice as many digits as they occupy bytes.
//
//	00h..BFh: ASCII string of LVAR chars
//	C0h..CFh: positive BCD with (LVAR - C0h)·2 digits
//	D0h..DFh: negative BCD with (LVAR - D0h)·2 digits
//	E0h..EFh: binary number with (LVAR - E0h) bytes
//	F0h..FAh: floating point with (LVAR - F0h) bytes
//	FBh..FFh: reserved
func decodeLVAR(data []byte, i int, dData *DecodedDataRecord) (int, error) {
	if err := need(data, i, 1); err != nil {
		return 0, err
	}
	lvar := data[i]
	payload := i + 1
	switch {
	case lvar <= 0xBF:
		size := int(lvar)
		if err := need(data, payload, size); err != nil {
			return 0, err
		}
		dData.ValueString = decodeASCII(data[payload : payload+size])
		return size, nil
	case lvar <= 0xDF:
		// C0h..CFh positive, D0h..DFh negative: the sign is in the LVAR byte,
		// not in a nibble, so the magnitude decodes sign-free and we apply the
		// sign here. Routing this through bcdSigned would double the sign on a
		// payload that also carries a sign nibble.
		negative := lvar > 0xCF
		size := int(lvar & 0x0F)
		if err := need(data, payload, size); err != nil {
			return 0, err
		}
		v, err := bcdMagnitude(data[payload : payload+size])
		if err != nil {
			// A bad value, not a bad frame: the LVAR byte gave us the width.
			// Same rule as the DIF BCD path in decodeRecordValue.
			dData.ValueErr = fmt.Errorf("LVAR BCD data field: %w", err)
			return size, nil
		}
		dData.RawValue = float64(v)
		if negative {
			dData.RawValue = -dData.RawValue
		}
		return size, nil
	case lvar <= 0xEF:
		size := int(lvar) - 0xE0
		if size < 1 || size > maxLEBytes {
			return 0, fmt.Errorf("unsupported binary size: %d", size)
		}
		if err := need(data, payload, size); err != nil {
			return 0, err
		}
		dData.RawValue = integerRawValue(data[payload:payload+size], dData.Unit.Raw)
		return size, nil
	case lvar <= 0xFA:
		size := int(lvar) - 0xF0
		if size != 4 {
			return 0, fmt.Errorf("unsupported float size: %d", size)
		}
		if err := need(data, payload, size); err != nil {
			return 0, err
		}
		f, err := bytesToFloat32(data[payload : payload+size])
		if err != nil {
			return 0, err
		}
		dData.RawValue = float64(f)
		return size, nil
	}
	return 0, fmt.Errorf("reserved LVAR value: 0x%x", lvar)
}
