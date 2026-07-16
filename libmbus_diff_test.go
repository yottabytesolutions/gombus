package gombus

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is a differential test against libmbus, the reference C
// implementation the frame corpus in testdata/frames comes from. Every
// expectation here is libmbus's own decoded output, vendored verbatim into
// testdata/libmbus-xml, not an assertion we wrote. That is the point:
// assertions we author can encode our own bugs, a third-party reference
// cannot. See NOTICE and testdata/LICENSE.libmbus for attribution.
//
// The test is hermetic. It reads only the vendored files and never touches the
// network.

// libmbusUnreferencedFixture is the one frame in testdata/frames with no
// libmbus reference output: it is a local Kamstrup MULTICAL 603 capture from
// the Meterlogger project, not part of the libmbus corpus. It is excluded here
// by name rather than by silently tolerating a missing file, so that a fixture
// whose reference fails to vendor is a failure instead of a gap.
const libmbusUnreferencedFixture = "Meterlogger-response.hex"

// libmbusData is the root of a libmbus reference document.
type libmbusData struct {
	XMLName xml.Name               `xml:"MBusData"`
	Slave   libmbusSlaveInfo       `xml:"SlaveInformation"`
	Records []libmbusDataRecordXML `xml:"DataRecord"`
}

// libmbusSlaveInfo is the fixed identification block. Every field is kept as a
// string because that is what libmbus emits; the test parses the ones it
// compares numerically.
type libmbusSlaveInfo struct {
	ID           string `xml:"Id"`
	Manufacturer string `xml:"Manufacturer"`
	Version      string `xml:"Version"`
	ProductName  string `xml:"ProductName"`
	Medium       string `xml:"Medium"`
	AccessNumber string `xml:"AccessNumber"`
	Status       string `xml:"Status"`
	Signature    string `xml:"Signature"`
}

// libmbusDataRecordXML is one <DataRecord>. StorageNumber is a pointer because
// libmbus omits it entirely on the special-function records, and "absent" must
// not be read as "zero".
type libmbusDataRecordXML struct {
	ID            int    `xml:"id,attr"`
	Function      string `xml:"Function"`
	StorageNumber *int   `xml:"StorageNumber"`
	Unit          string `xml:"Unit"`
	Value         string `xml:"Value"`
}

// latin1Reader is an xml.Decoder CharsetReader for the ISO-8859-1 declaration
// the libmbus references carry. encoding/xml refuses any non-UTF-8 declaration
// without one, so some reader has to be supplied.
//
// The declaration is wrong. Every vendored reference is byte-for-byte UTF-8
// despite saying ISO-8859-1: the only non-ASCII text in the corpus is "Warm
// water (30-90°C)" in EFE_Engelmann-WaterStar.xml, and its degree sign is
// stored as C2 B0, the two-byte UTF-8 form, not as B0. Transcoding those bytes
// as Latin-1 would widen each one separately and yield "30-90Â°C", corrupting
// the very text the CharsetReader exists to preserve. So the bytes are passed
// through and the label is ignored.
//
// Trusting content over declaration is only safe if it is checked, hence the
// explicit UTF-8 validation: were a genuinely Latin-1 reference vendored later,
// its high bytes would not be valid UTF-8 and this errors out loudly instead of
// letting encoding/xml turn them into U+FFFD replacement characters and quietly
// compare mojibake.
func latin1Reader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "iso_8859-1", "latin1":
	default:
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	raw, err := io.ReadAll(input)
	if err != nil {
		return nil, fmt.Errorf("reading %s input: %w", charset, err)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf(
			"reference declares %s but is not valid UTF-8; the vendored corpus is "+
				"UTF-8 despite its declaration, so this file needs real transcoding",
			charset,
		)
	}
	return bytes.NewReader(raw), nil
}

// loadLibmbusXML parses a vendored libmbus reference document.
func loadLibmbusXML(t *testing.T, path string) libmbusData {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err, "opening reference %s", path)
	defer func() {
		require.NoError(t, f.Close(), "closing reference %s", path)
	}()

	dec := xml.NewDecoder(f)
	dec.CharsetReader = latin1Reader

	var doc libmbusData
	require.NoError(t, dec.Decode(&doc), "parsing reference %s", path)
	return doc
}

// skipReason names, from a closed set, why a record's value cannot be compared
// with libmbus. Every skip must carry one and every skip is counted. A
// differential test that quietly drops the hard records reads as "we match
// libmbus" when it means "we match libmbus on the easy ones", so an unnamed
// skip is not allowed and a mismatch is never resolved by inventing a reason.
type skipReason string

const (
	// skipManufacturerSpecific: past a 0x0F DIF the payload is opaque. libmbus
	// dumps the remaining bytes as hex, we emit a sentinel record.
	skipManufacturerSpecific skipReason = "skipManufacturerSpecific"

	// skipSentinel: same for the 0x1F more-records-follow DIF.
	skipSentinel skipReason = "skipSentinel"

	// skipValueErr: a value we flag as undecodable (the Elster F96 error-state
	// filler). libmbus prints the raw nibbles as hex, we set ValueErr and leave
	// Value at zero.
	skipValueErr skipReason = "skipValueErr"

	// skipStringValue: libmbus emits a textual value for an LVAR ASCII field
	// (a customer ID, a fabrication number). Not a number on either side.
	skipStringValue skipReason = "skipStringValue"

	// skipDateTimeInvalid: we reject a date the meter cannot have meant and set
	// ValueErr; libmbus renders the out-of-range components verbatim
	// ("2000-00-00", a zeroth day in "1900-01-00T00:00:00"). A deliberate
	// divergence, not a decoding disagreement: there is no timestamp here for
	// the two to agree on.
	skipDateTimeInvalid skipReason = "skipDateTimeInvalid"

	// skipDateTimeUnsupported: an encoding libmbus decodes and we do not yet
	// (EN 13757-3 type I, the 6-byte date and time to the second). A real gap
	// on our side rather than a difference of opinion.
	skipDateTimeUnsupported skipReason = "skipDateTimeUnsupported"

	// skipUnitNotScaled: the register is dimensionless or descriptive ("Error
	// flags", "Fabrication number", "Digital input (binary)"), so neither side
	// has a decimal scale and there is genuinely nothing to compare.
	skipUnitNotScaled skipReason = "skipUnitNotScaled"

	// skipUnitTimeBase: a duration, where the two implementations disagree
	// about the UNIT rather than about how to print it. libmbus reports
	// operating time in the base the meter used ("Operating time (days)") and
	// leaves Exp meaning "days"; we normalise to seconds and carry the
	// conversion in Exp (86400 for days, 3600 for hours). Both are defensible
	// and ours is arguably more useful, but the numbers are not comparable
	// without adopting libmbus's vocabulary, which is out of bounds.
	//
	// Deliberately NOT folded into skipUnitNotScaled: that reason means "there
	// is no scale here", this one means "there is a scale on both sides and we
	// measure it in different units". Counting them together would imply more
	// agreement than exists. This is the one place in the corpus where we and
	// libmbus genuinely model the same fact differently, so a caller
	// integrating against both would want to know.
	skipUnitTimeBase skipReason = "skipUnitTimeBase"
)

// allSkipReasons fixes the tally order so the reported totals are stable.
var allSkipReasons = []skipReason{
	skipManufacturerSpecific,
	skipSentinel,
	skipValueErr,
	skipStringValue,
	skipDateTimeInvalid,
	skipDateTimeUnsupported,
	skipUnitNotScaled,
	skipUnitTimeBase,
}

// libmbusTimeBases are the duration units libmbus reports in, where we instead
// normalise to seconds. Written as the parenthesised tail libmbus emits, e.g.
// "Operating time (days)".
var libmbusTimeBases = map[string]bool{
	"seconds": true, "minutes": true, "hours": true, "days": true,
}

// libmbusSaysTimeBase reports whether the unit is a duration in one of
// libmbus's time bases, so that exponent difference gets labelled as the
// semantic one it is rather than as "no scale to compare".
func libmbusSaysTimeBase(unit string) bool {
	i := strings.LastIndex(unit, "(")
	j := strings.LastIndex(unit, ")")
	if i < 0 || j < i {
		return false
	}
	return libmbusTimeBases[unit[i+1:j]]
}

// libmbusSaysDate reports whether libmbus classified this record as a
// timestamp. libmbus marks every one with a "Time Point" unit, and its <Value>
// is then a rendered date rather than a number.
//
// This reads one bit of meaning out of libmbus's unit text, not its vocabulary:
// the question is only "did the reference call this a date", which decides
// whether <Value> is even parseable as a number. Nothing about libmbus's unit
// wording is compared or mapped onto ours.
func libmbusSaysDate(ref libmbusDataRecordXML) bool {
	return strings.HasPrefix(ref.Unit, "Time Point")
}

// isDateRecord reports whether WE decoded this record as a timestamp, which is
// what drives the comparison. The decision comes from our own API (Unit.Date,
// per unit.go: "Use Date != DateNone to test whether a record carries a
// timestamp"), never from libmbus's text, so the test cannot be fooled into
// agreeing by borrowing the reference's classification.
//
// Whether libmbus agrees is then asserted rather than assumed, in
// compareRecord. "Is this record a date at all" is a semantic question, so a
// disagreement about it is a finding, not a formatting difference.
func isDateRecord(got DecodedDataRecord) bool {
	return got.Unit.Date != DateNone
}

// libmbusBaseUnits are the physical base units libmbus writes a decimal scale
// in front of. Anything else ("binary", "days", "H.C.A.") carries no exponent
// to compare and is skipped as skipUnitNotScaled.
var libmbusBaseUnits = map[string]bool{
	"m^3": true, "m^3/h": true, "W": true, "Wh": true,
	"J": true, "J/h": true, "A": true, "V": true, "%RH": true,
}

// libmbusScalePrefixes are the non-numeric scale tokens libmbus emits. Numeric
// tokens ("1e-1", "10", "100") are parsed instead.
var libmbusScalePrefixes = map[string]float64{
	"m":  1.0e-3,
	"my": 1.0e-6,
}

// splitLibmbusUnit peels the base unit off the end of a libmbus unit's fields,
// returning the leading scale tokens, the base's own factor, and whether the
// tail was a physical unit at all. "deg C" is the one two-word base. A leading
// "k" on a known unit ("kWh") is the base's own factor of 1e3, because libmbus
// writes the kilo into the unit rather than into the scale.
func splitLibmbusUnit(fields []string) (scale []string, baseFactor float64, ok bool) {
	if len(fields) == 0 {
		return nil, 0, false
	}
	if n := len(fields); n >= 2 && fields[n-2] == "deg" && fields[n-1] == "C" {
		return fields[:n-2], 1.0, true
	}

	last := fields[len(fields)-1]
	rest := fields[:len(fields)-1]
	switch {
	case libmbusBaseUnits[last]:
		return rest, 1.0, true
	case strings.HasPrefix(last, "k") && libmbusBaseUnits[strings.TrimPrefix(last, "k")]:
		return rest, 1.0e3, true
	}
	return nil, 0, false
}

// libmbusExp recovers the decimal scale libmbus folded into a unit string, so
// our Unit.Exp can be checked against it. This is the only way to test Exp
// against libmbus at all: libmbus never scales its <Value>, it prints the raw
// register and puts the decade in the unit text ("Volume (m m^3)", "Energy (10
// kWh)", "Flow temperature (1e-1 deg C)"). Comparing RawValue therefore proves
// nothing about Exp, and comparing whole unit strings proves nothing about
// anything, since the two vocabularies differ. Reading the decade out is what
// makes a wrong Exp in unitTable visible across the corpus.
//
// It parses rather than tabulates on purpose: a hand-written table of expected
// exponents would be an assertion we authored, which is exactly what this file
// exists to avoid. Every number returned here is read out of libmbus's own text.
//
// The second result is false when the unit carries no scale to compare.
func libmbusExp(unit string) (float64, bool) {
	s := unit
	// The scale lives inside the parentheses when there is a quantity name
	// ("Volume (m m^3)"); bare units ("m A", " V") have none.
	if i := strings.LastIndex(s, "("); i >= 0 {
		j := strings.LastIndex(s, ")")
		if j < i {
			return 0, false
		}
		s = s[i+1 : j]
	}

	// Fields also collapses the double spaces libmbus emits after a scale.
	scale, baseFactor, ok := splitLibmbusUnit(strings.Fields(s))
	if !ok {
		return 0, false
	}

	switch len(scale) {
	case 0:
		return baseFactor, true
	case 1:
		if f, found := libmbusScalePrefixes[scale[0]]; found {
			return f * baseFactor, true
		}
		f, err := strconv.ParseFloat(scale[0], 64)
		if err != nil {
			return 0, false
		}
		return f * baseFactor, true
	}
	return 0, false
}

// classifyValue reports why ref's numeric value is not comparable with got's, or
// "" when it must be compared. Every arm is a structural difference between the
// two implementations, decided before looking at the numbers. A value that
// merely disagrees is never classified here: that is a finding, not a skip.
func classifyValue(ref libmbusDataRecordXML, got DecodedDataRecord) skipReason {
	switch {
	case got.ValueErr != nil:
		return skipValueErr
	case ref.Function == "Manufacturer specific":
		return skipManufacturerSpecific
	case ref.Function == "More records follow":
		return skipSentinel
	}
	// libmbus's own output is not a number, so there is nothing to compare
	// against. This cannot hide a numeric disagreement: it triggers on the
	// reference side only.
	if _, err := strconv.ParseFloat(ref.Value, 64); err != nil {
		return skipStringValue
	}
	return ""
}

// classifyTime reports why ref's timestamp is not comparable with got's, or ""
// when it must be compared. Only our own two named date sentinels qualify: any
// other ValueErr on a date record, or a timestamp that simply differs, is a
// finding.
func classifyTime(got DecodedDataRecord) skipReason {
	switch {
	case errors.Is(got.ValueErr, ErrInvalidDateTime):
		return skipDateTimeInvalid
	case errors.Is(got.ValueErr, ErrUnsupportedDateTime):
		return skipDateTimeUnsupported
	}
	return ""
}

// libmbusTimeLayouts are the two renderings libmbus uses: type F carries a time
// of day, type G is a date alone.
var libmbusTimeLayouts = []string{"2006-01-02T15:04:05", "2006-01-02"}

// parseLibmbusTime parses a libmbus timestamp in UTC, matching how the decoder
// builds DecodedDataRecord.Time. Neither side means UTC: both are the meter's
// wall clock, and UTC only makes the comparison deterministic.
func parseLibmbusTime(s string) (time.Time, error) {
	for _, layout := range libmbusTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised libmbus timestamp %q", s)
}

// diffTally counts what a fixture actually proved, per comparison kind, so the
// weight of the evidence is visible rather than asserted.
type diffTally struct {
	slaveFields    int
	functions      int
	storageNumbers int
	values         int
	times          int
	exponents      int
	// dateClasses counts the "is this record a date at all" agreement check.
	// It is asserted on every record, so it is counted on every record: an
	// assertion the tally does not count is an assertion the tally lies about.
	dateClasses int

	// skipped counts the value/timestamp axis, expSkipped the exponent axis.
	// They are separate maps because every record is classified once on each
	// axis independently: a record can have a comparable value and an
	// incomparable unit, so folding them together would make the per-axis
	// accounting invariants unenforceable.
	skipped    map[skipReason]int
	expSkipped map[skipReason]int
}

func newTally() *diffTally {
	return &diffTally{
		skipped:    map[skipReason]int{},
		expSkipped: map[skipReason]int{},
	}
}

func (d *diffTally) skip(r skipReason)    { d.skipped[r]++ }
func (d *diffTally) skipExp(r skipReason) { d.expSkipped[r]++ }

func totalOf(m map[skipReason]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}

func (d *diffTally) totalSkipped() int    { return totalOf(d.skipped) }
func (d *diffTally) totalExpSkipped() int { return totalOf(d.expSkipped) }

// totalCompared is every individual field checked against libmbus. It is summed
// from the counters rather than worked out by hand, because a headline figure
// nobody can re-derive is the same class of error as an untested assumption:
// this number was once reported as 977 purely because it was added up in
// someone's head, and it was wrong by 190. Anything reported about this test
// should come from here.
func (d *diffTally) totalCompared() int {
	return d.slaveFields + d.functions + d.storageNumbers +
		d.values + d.times + d.exponents + d.dateClasses
}

func (d *diffTally) merge(o *diffTally) {
	d.slaveFields += o.slaveFields
	d.functions += o.functions
	d.storageNumbers += o.storageNumbers
	d.values += o.values
	d.times += o.times
	d.exponents += o.exponents
	d.dateClasses += o.dateClasses
	for r, c := range o.skipped {
		d.skipped[r] += c
	}
	for r, c := range o.expSkipped {
		d.expSkipped[r] += c
	}
}

// formatSkips renders a skip map in the fixed allSkipReasons order.
func formatSkips(b *strings.Builder, m map[skipReason]int) {
	fmt.Fprintf(b, "%d", totalOf(m))
	for _, r := range allSkipReasons {
		if c := m[r]; c > 0 {
			fmt.Fprintf(b, " %s=%d", r, c)
		}
	}
}

func (d *diffTally) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "compared %d fields: %d slave, %d functions, %d storage numbers, %d values, %d timestamps, %d exponents, %d date classifications",
		d.totalCompared(), d.slaveFields, d.functions, d.storageNumbers,
		d.values, d.times, d.exponents, d.dateClasses)
	b.WriteString("; value/time skipped: ")
	formatSkips(&b, d.skipped)
	b.WriteString("; exponent skipped: ")
	formatSkips(&b, d.expSkipped)
	return b.String()
}

// valueEpsilon bounds a match against libmbus's printed output.
//
// The absolute term is the dominant one. libmbus prints IEEE reals with "%f",
// i.e. six decimals, so its own text rounds by up to 5e-7 before we ever see
// it; 1e-6 clears that with a factor of two to spare and is still far tighter
// than any bug this test hunts. A wrong Exp is off by a power of ten and a
// sign-extension error flips the sign, so neither hides under any epsilon worth
// arguing about.
//
// The relative term only matters for the large integer fields (error flags run
// to ~4.29e9), where float64 is still exact but leaving no relative slack would
// be brittle for no gain.
func valueEpsilon(expected float64) float64 {
	return 1e-6 + 1e-9*math.Abs(expected)
}

// TestLibmbusDifferential decodes every libmbus fixture and compares the result
// field by field against libmbus's own reference output.
func TestLibmbusDifferential(t *testing.T) {
	total := newTally()

	for _, name := range libmbusFixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			tally := compareFixture(t, name)
			t.Log(tally)
			total.merge(tally)
		})
	}

	t.Logf("TOTAL across %d fixtures: %s", len(libmbusFixtureNames(t)), total)

	// Pin the totals. Without this the tally is decoration: a change that
	// widened a skip arm, or one that stopped emitting records, would still
	// report "all green" while quietly comparing less. These numbers must only
	// move with a deliberate edit and an explanation.
	assert.Equal(t, 140, total.slaveFields, "slave fields compared")
	assert.Equal(t, 288, total.functions, "record functions compared")
	assert.Equal(t, 279, total.storageNumbers, "storage numbers compared")
	assert.Equal(t, 242, total.values, "record values compared")
	assert.Equal(t, 31, total.times, "record timestamps compared")
	assert.Equal(t, 288, total.dateClasses, "date classifications compared")
	// skipDateTimeUnsupported is absent on purpose: the corpus no longer has an
	// encoding we cannot decode. The reason stays in the closed set because
	// decodeRecordTime still returns ErrUnsupportedDateTime, so a future fixture
	// can legitimately land in it. If it ever reappears here, that is a gap
	// against libmbus and wants reporting, not a silent bump of this map.
	assert.Equal(t, map[skipReason]int{
		skipManufacturerSpecific: 5,
		skipSentinel:             4,
		skipValueErr:             2,
		skipStringValue:          2,
		skipDateTimeInvalid:      2,
	}, total.skipped, "records skipped by reason")

	assert.Equal(t, 187, total.exponents, "unit exponents compared")
	assert.Equal(t, map[skipReason]int{
		skipUnitNotScaled: 89,
		skipUnitTimeBase:  12,
	}, total.expSkipped, "exponents skipped by reason")

	// Every record is accounted for on both axes: compared as a value or a
	// timestamp, or skipped for exactly one named reason; and compared for its
	// exponent, or skipped for one. This is what stops a record from quietly
	// falling through the classification.
	assert.Equal(t, total.functions, total.values+total.times+total.totalSkipped(),
		"every record must be compared or skipped for a named reason")
	assert.Equal(t, total.functions, total.exponents+total.totalExpSkipped(),
		"every record's exponent must be compared or skipped for a named reason")

	// The headline figure, pinned so it can only be quoted from a real run.
	// It is the sum of the counters above, which the assertions have already
	// fixed individually, so this cannot drift without one of them failing too.
	assert.Equal(t, 1455, total.totalCompared(), "total fields compared against libmbus")
}

// compareFixture runs one fixture against its reference and returns the tally.
func compareFixture(t *testing.T, name string) *diffTally {
	t.Helper()
	tally := newTally()

	base := strings.TrimSuffix(name, ".hex")
	data := loadHexFixture(t, filepath.Join("testdata", "frames", name))
	ref := loadLibmbusXML(t, filepath.Join("testdata", "libmbus-xml", base+".xml"))

	df, err := LongFrame(data).Decode()
	require.NoError(t, err, "decoding %s", name)

	compareSlaveInfo(t, ref.Slave, df, tally)

	require.Len(t, df.DataRecords, len(ref.Records), "record count")
	for i, rr := range ref.Records {
		compareRecord(t, i, rr, df.DataRecords[i], tally)
	}
	return tally
}

// compareSlaveInfo checks the fixed identification block. The medium and
// function vocabularies in this package were derived from libmbus, so string
// equality is meaningful for them. The unit vocabulary is NOT: libmbus folds
// the scale into the unit text ("Volume (m m^3)") where we carry a unit plus a
// separate Exp, so comparing unit strings would test nothing and is not done.
func compareSlaveInfo(t *testing.T, ref libmbusSlaveInfo, df *DecodedFrame, tally *diffTally) {
	t.Helper()

	serial, err := strconv.Atoi(ref.ID)
	require.NoError(t, err, "reference Id")
	assert.Equal(t, serial, df.SerialNumber, "serial number")

	version, err := strconv.Atoi(ref.Version)
	require.NoError(t, err, "reference Version")
	assert.Equal(t, version, df.Version, "version")

	access, err := strconv.Atoi(ref.AccessNumber)
	require.NoError(t, err, "reference AccessNumber")
	assert.Equal(t, access, int(df.AccessNumber), "access number")

	status, err := strconv.ParseUint(ref.Status, 16, 8)
	require.NoError(t, err, "reference Status")
	assert.Equal(t, byte(status), df.Status, "status")

	signature, err := strconv.ParseUint(ref.Signature, 16, 16)
	require.NoError(t, err, "reference Signature")
	assert.Equal(t, uint16(signature), df.Signature, "signature")

	assert.Equal(t, ref.Manufacturer, df.Manufacturer, "manufacturer")
	assert.Equal(t, ref.Medium, df.DeviceType, "medium / device type")

	// ProductName is deliberately NOT compared: the two implementations mean
	// different things by it. libmbus's mbus_data_product_name is a hardcoded
	// device database keyed on the manufacturer/version pair in the header, so
	// it answers "which model is this" from a table and returns "" for any
	// device not in it. Ours (extractProductName) reads an identifying ASCII
	// record out of the frame itself. On ACW_Itron-CYBLE-M-Bus-14 libmbus says
	// "Itron CYBLE M-Bus 1.4" from its table while we say "09LA076755" from the
	// frame's customer-ID record. Both are correct answers to different
	// questions, so an equality check here would assert a false expectation.
	tally.slaveFields += 7
}

// compareRecord checks one data record against its reference.
//
// Function and StorageNumber are compared on every record libmbus reports them
// for, including the ones whose value is skipped: a skipped value is not a
// skipped record.
//
// A record carries either a number or a timestamp, and libmbus's <Value> holds
// whichever it is, so each record contributes exactly one of the two
// comparisons. The unit vocabulary is NOT compared: libmbus folds the scale into
// the unit text ("Volume (m m^3)") where we carry a unit plus a separate Exp, so
// string equality there would test nothing.
func compareRecord(t *testing.T, i int, ref libmbusDataRecordXML, got DecodedDataRecord, tally *diffTally) {
	t.Helper()

	assert.Equal(t, ref.Function, got.Function, "record %d: function", i)
	tally.functions++

	if ref.StorageNumber != nil {
		assert.Equal(t, *ref.StorageNumber, got.StorageNumber, "record %d: storage number", i)
		tally.storageNumbers++
	}

	compareRecordExp(t, i, ref, got, tally)

	// Both sides must agree on whether this record is a timestamp at all.
	// That is a semantic claim, not a rendering choice, so a disagreement is a
	// finding. Asserting it also stops the two branches below from silently
	// diverging: a date we failed to recognise would otherwise be compared as
	// a number against libmbus's date string and skip as skipStringValue.
	assert.Equal(t, libmbusSaysDate(ref), isDateRecord(got),
		"record %d: date classification (libmbus unit %q, our Unit.Date %d)",
		i, ref.Unit, got.Unit.Date)
	tally.dateClasses++

	if isDateRecord(got) {
		compareRecordTime(t, i, ref, got, tally)
		return
	}
	compareRecordValue(t, i, ref, got, tally)
}

// compareRecordExp checks our Unit.Exp against the decade libmbus folded into
// its unit text. This is the half of the record that comparing RawValue cannot
// reach: RawValue is the register before scaling, so it is identical whatever
// Exp says. Together the two pin the whole reading, since our Value is
// RawValue * Exp.
func compareRecordExp(t *testing.T, i int, ref libmbusDataRecordXML, got DecodedDataRecord, tally *diffTally) {
	t.Helper()

	want, ok := libmbusExp(ref.Unit)
	if !ok {
		if libmbusSaysTimeBase(ref.Unit) {
			tally.skipExp(skipUnitTimeBase)
			return
		}
		tally.skipExp(skipUnitNotScaled)
		return
	}
	// Relative, not absolute: these span 1e-6 to 1e4, so a fixed epsilon would
	// be meaningless at one end or the other. Any real Exp bug is a whole
	// decade out, so this is not a tight call.
	assert.InEpsilon(t, want, got.Unit.Exp, 1e-9,
		"record %d: unit exponent (libmbus unit %q)", i, ref.Unit)
	tally.exponents++
}

// compareRecordValue checks the numeric register.
//
// The field compared is RawValue, not Value. libmbus does not scale: it folds
// the decade into the unit text and prints the raw register. Verified on
// ELS_Elster-F96-Plus record 6, where the unit table's Exp is 1e-1: libmbus
// reports "Flow temperature (1e-1 deg C)" = 227, our RawValue is 227 and our
// Value is 22.7. Value would therefore be the wrong field to compare here, and
// checking it against libmbus would fail on every scaled register. Value has its
// own tests; what libmbus pins is the register underneath it, which is where a
// sign-extension or BCD bug lives.
func compareRecordValue(t *testing.T, i int, ref libmbusDataRecordXML, got DecodedDataRecord, tally *diffTally) {
	t.Helper()

	if reason := classifyValue(ref, got); reason != "" {
		tally.skip(reason)
		return
	}

	want, err := strconv.ParseFloat(ref.Value, 64)
	require.NoError(t, err, "record %d: reference Value", i)
	assert.InDelta(t, want, got.RawValue, valueEpsilon(want),
		"record %d: raw value (libmbus unit %q)", i, ref.Unit)
	tally.values++
}

// compareRecordTime checks a decoded timestamp against libmbus's rendering.
// This compares Time rather than RawValue: for a date record RawValue is the
// undecoded bitfield, so libmbus's rendered date is only comparable with our
// assembled time.Time. Both sides are the meter's wall clock with no zone.
func compareRecordTime(t *testing.T, i int, ref libmbusDataRecordXML, got DecodedDataRecord, tally *diffTally) {
	t.Helper()

	if reason := classifyTime(got); reason != "" {
		tally.skip(reason)
		return
	}
	require.NoError(t, got.ValueErr,
		"record %d: date record failed to decode for an unnamed reason (libmbus read it as %q)",
		i, ref.Value)

	want, err := parseLibmbusTime(ref.Value)
	require.NoError(t, err, "record %d: reference Value", i)
	assert.True(t, want.Equal(got.Time),
		"record %d: timestamp: libmbus %s, gombus %s", i, want, got.Time)
	tally.times++
}

// libmbusFixtureNames lists the .hex fixtures that have a libmbus reference.
func libmbusFixtureNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "frames"))
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".hex") || e.Name() == libmbusUnreferencedFixture {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// TestLibmbusReferencesComplete pins the corpus itself. Every fixture except
// the one named exclusion must have a vendored reference, and no reference may
// be orphaned. Without this a reference that failed to vendor would silently
// shrink the differential test rather than fail it.
func TestLibmbusReferencesComplete(t *testing.T) {
	names := libmbusFixtureNames(t)
	assert.Len(t, names, 20, "fixtures with a libmbus reference")

	for _, name := range names {
		base := strings.TrimSuffix(name, ".hex")
		_, err := os.Stat(filepath.Join("testdata", "libmbus-xml", base+".xml"))
		require.NoError(t, err, "missing reference for %s", name)
	}

	// The exclusion is real, not a typo covering an absent file.
	_, err := os.Stat(filepath.Join("testdata", "frames", libmbusUnreferencedFixture))
	require.NoError(t, err, "the excluded fixture must exist")

	refs, err := os.ReadDir(filepath.Join("testdata", "libmbus-xml"))
	require.NoError(t, err)
	for _, e := range refs {
		base := strings.TrimSuffix(e.Name(), ".xml")
		_, err := os.Stat(filepath.Join("testdata", "frames", base+".hex"))
		require.NoError(t, err, "orphaned reference %s", e.Name())
	}
}

// TestLibmbusExp covers the unit-text parser directly. It is load-bearing: it
// supplies the expected value for 187 exponent comparisons, so a bug in it
// would silently weaken or falsify all of them. The inputs are the real unit
// strings from the vendored corpus, and the expected exponents are read off
// libmbus's own notation (SI prefix, explicit "1e-n", or a literal multiplier).
func TestLibmbusExp(t *testing.T) {
	tests := []struct {
		unit string
		want float64
		ok   bool
	}{
		// Explicit 1e-n notation.
		{"Flow temperature (1e-1 deg C)", 1e-1, true},
		{"Return temperature (1e-2 deg C)", 1e-2, true},
		{"External temperature (1e-2  deg C)", 1e-2, true},
		{"Volume (1e-1  m^3)", 1e-1, true},
		{"Volume flow (1e-2  m^3/h)", 1e-2, true},
		{"1e-1  A", 1e-1, true},
		{"1e-1  V", 1e-1, true},
		{"1e-2  %RH", 1e-2, true},
		// SI prefixes, including the kilo libmbus writes into the unit itself.
		{"Volume (m m^3)", 1e-3, true},
		{"Volume (my m^3)", 1e-6, true},
		{"Volume flow (m m^3/h)", 1e-3, true},
		{"Temperature Difference (m deg C)", 1e-3, true},
		{"m A", 1e-3, true},
		{"Energy (kWh)", 1e3, true},
		{"Power (kW)", 1e3, true},
		// Literal multipliers, and a multiplier combined with the kilo.
		{"Energy (10 Wh)", 1e1, true},
		{"Energy (10 kWh)", 1e4, true},
		{"Power (10 W)", 1e1, true},
		{"Power (100 W)", 1e2, true},
		// No scale means 1e0, whether or not libmbus left a stray space.
		{"Energy (Wh)", 1e0, true},
		{"Power (W)", 1e0, true},
		{"Volume ( m^3)", 1e0, true},
		{"Volume flow ( m^3/h)", 1e0, true},
		{"Flow temperature (deg C)", 1e0, true},
		{"Temperature Difference ( deg C)", 1e0, true},
		{" V", 1e0, true},
		// Units with no decimal scale on a physical base unit. These must be
		// reported as such, never silently treated as 1e0, or a real exponent
		// mismatch could hide behind a default.
		{"Error flags", 0, false},
		{"Fabrication number", 0, false},
		{"Digital input (binary)", 0, false},
		{"Time Point (date)", 0, false},
		{"Units for H.C.A.", 0, false},
		{"Manufacturer specific", 0, false},
		{"cust. ID", 0, false},
		{"C", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.unit, func(t *testing.T) {
			got, ok := libmbusExp(tc.unit)
			require.Equal(t, tc.ok, ok, "scale present")
			if tc.ok {
				assert.InEpsilon(t, tc.want, got, 1e-9)
			}
		})
	}
}

// TestLibmbusSaysTimeBase covers the split between the two exponent skips. It
// decides whether a duration is reported as "we and libmbus measure this in
// different units" or as "there is no scale here", which is the difference
// between a tally that states a real semantic gap and one that hides it.
func TestLibmbusSaysTimeBase(t *testing.T) {
	tests := []struct {
		unit string
		want bool
	}{
		{"Operating time (days)", true},
		{"Operating time (hours)", true},
		{"Operating time (seconds)", true},
		{"On time (days)", true},
		{"On time (hours)", true},
		{"On time (seconds)", true},
		{"Averaging Duration (hours)", true},
		// A date is not a duration: it has no time base to disagree about, and
		// it is compared as a timestamp rather than skipped.
		{"Time Point (date)", false},
		{"Time Point (time & date)", false},
		// Dimensionless and physical units are the other skip and the compared
		// path respectively; neither is a time base.
		{"Error flags", false},
		{"Digital input (binary)", false},
		{"Volume (m m^3)", false},
		{"bat. time", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.unit, func(t *testing.T) {
			assert.Equal(t, tc.want, libmbusSaysTimeBase(tc.unit))
		})
	}
}

// TestLatin1Reader pins the CharsetReader's contract: accept the charset names
// the corpus declares, pass UTF-8 bytes through untouched, and reject anything
// that is not UTF-8 rather than silently mangling it.
func TestLatin1Reader(t *testing.T) {
	tests := []struct {
		name    string
		charset string
		input   []byte
		want    string
		wantErr bool
	}{
		{"ascii passes through", "ISO-8859-1", []byte("abc"), "abc", false},
		// The real corpus byte sequence: UTF-8 despite the ISO-8859-1 label.
		{"utf-8 degree sign passes through", "ISO-8859-1", []byte("30-90°C"), "30-90°C", false},
		{"empty", "ISO-8859-1", nil, "", false},
		{"charset name is case insensitive", "iso-8859-1", []byte("x"), "x", false},
		{"underscore charset form", "ISO_8859-1", []byte("x"), "x", false},
		{"latin1 alias", "latin1", []byte("x"), "x", false},
		// A lone high byte is valid ISO-8859-1 and invalid UTF-8. Rejecting it
		// is the point: a future re-vendor of a genuinely Latin-1 reference then
		// fails loudly instead of comparing replacement characters.
		{"genuine latin-1 high byte is rejected", "ISO-8859-1", []byte{0xB0, 'C'}, "", true},
		{"truncated utf-8 sequence is rejected", "ISO-8859-1", []byte{0xC2}, "", true},
		{"unsupported charset", "UTF-16", []byte("x"), "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := latin1Reader(tc.charset, bytes.NewReader(tc.input))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			got, err := io.ReadAll(r)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))
		})
	}
}
