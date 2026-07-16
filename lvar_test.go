package gombus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeLVARRanges exercises every documented LVAR range so that
// decodeLVAR's branches are individually covered.
func TestDecodeLVARRanges(t *testing.T) {
	t.Run("ASCII string", func(t *testing.T) {
		var rec DecodedDataRecord
		// LVAR=4, "ABCD" follows; decodeASCII reverses to "DCBA".
		size, err := decodeLVAR([]byte{0x04, 'A', 'B', 'C', 'D'}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 4, size)
		assert.Equal(t, "DCBA", rec.ValueString)
	})

	t.Run("positive BCD", func(t *testing.T) {
		var rec DecodedDataRecord
		// LVAR=0xC2 → 2 bytes holding 4 BCD digits. 78 56 in BCD-LE = 5678.
		// The return is the byte count the caller must skip, not the digit
		// count: returning 4 here made the caller eat the next record.
		size, err := decodeLVAR([]byte{0xC2, 0x78, 0x56}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 2, size)
		assert.InDelta(t, 5678.0, rec.RawValue, 0)
	})

	t.Run("negative BCD", func(t *testing.T) {
		var rec DecodedDataRecord
		// LVAR=0xD2 → 2 bytes, 4 digits, negative. The sign comes from the
		// LVAR byte, so the payload nibbles are pure magnitude.
		size, err := decodeLVAR([]byte{0xD2, 0x78, 0x56}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 2, size)
		assert.InDelta(t, -5678.0, rec.RawValue, 0)
	})

	t.Run("BCD with illegal nibble", func(t *testing.T) {
		var rec DecodedDataRecord
		// bcdMagnitude rejects A-F in any nibble; LVAR BCD has no sign nibble.
		// The width is known, so this reports ValueErr and still consumes the
		// field rather than failing the frame.
		size, err := decodeLVAR([]byte{0xC2, 0x78, 0xF6}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 2, size)
		require.Error(t, rec.ValueErr)
		assert.Contains(t, rec.ValueErr.Error(), "invalid BCD")
		assert.InDelta(t, 0.0, rec.RawValue, 0)
	})

	t.Run("binary 1 byte", func(t *testing.T) {
		var rec DecodedDataRecord
		size, err := decodeLVAR([]byte{0xE1, 0x42}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 1, size)
		assert.Equal(t, 0x42, int(rec.RawValue))
	})

	t.Run("binary 2 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		size, err := decodeLVAR([]byte{0xE2, 0x34, 0x12}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 2, size)
		assert.Equal(t, 0x1234, int(rec.RawValue))
	})

	t.Run("binary 3 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		size, err := decodeLVAR([]byte{0xE3, 0x56, 0x34, 0x12}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 3, size)
		assert.Equal(t, 0x123456, int(rec.RawValue))
	})

	t.Run("binary 4 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		size, err := decodeLVAR([]byte{0xE4, 0x78, 0x56, 0x34, 0x12}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 4, size)
		assert.Equal(t, 0x12345678, int(rec.RawValue))
	})

	t.Run("binary 6 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		size, err := decodeLVAR([]byte{0xE6, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 6, size)
		assert.Equal(t, int64(0x060504030201), int64(rec.RawValue))
	})

	t.Run("binary 8 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		// Pick a value that fits inside the 52-bit mantissa so float64
		// roundtrip is exact: 0x000123456789ABCD = 320255973501389.
		size, err := decodeLVAR(
			[]byte{0xE8, 0xCD, 0xAB, 0x89, 0x67, 0x45, 0x23, 0x01, 0x00},
			0, &rec,
		)
		require.NoError(t, err)
		assert.Equal(t, 8, size)
		assert.Equal(t, int64(0x000123456789ABCD), int64(rec.RawValue))
	})

	t.Run("binary 7 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		// LVAR 0xE0-0xEF is "binary number with (LVAR - 0xE0) bytes", so 0xE7 is
		// a legal 7-byte field. It used to be rejected: the old width-keyed
		// helper set had no 7-byte variant to call, so the switch simply had no
		// arm for it and a valid field errored. A gap in the helpers had become
		// a gap in protocol support. The width-agnostic primitives read any
		// width up to 8, which closes it.
		//
		// 0x01020304050607 fits the float64 mantissa exactly.
		size, err := decodeLVAR([]byte{0xE7, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 7, size)
		assert.Equal(t, int64(0x01020304050607), int64(rec.RawValue))
	})

	t.Run("binary 5 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		size, err := decodeLVAR([]byte{0xE5, 0x05, 0x04, 0x03, 0x02, 0x01}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 5, size)
		assert.Equal(t, int64(0x0102030405), int64(rec.RawValue))
	})

	t.Run("binary unsupported size 0", func(t *testing.T) {
		var rec DecodedDataRecord
		_, err := decodeLVAR([]byte{0xE0}, 0, &rec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported binary size: 0")
	})

	t.Run("binary unsupported size 9", func(t *testing.T) {
		var rec DecodedDataRecord
		_, err := decodeLVAR([]byte{0xE9, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 0, &rec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported binary size: 9")
	})

	t.Run("binary sign-extends", func(t *testing.T) {
		var rec DecodedDataRecord
		// EN 13757-3 type B: an all-ones 3-byte binary LVAR is -1, not 16777215.
		_, err := decodeLVAR([]byte{0xE3, 0xFF, 0xFF, 0xFF}, 0, &rec)
		require.NoError(t, err)
		assert.InDelta(t, -1.0, rec.RawValue, 0)
	})

	t.Run("float 4 bytes", func(t *testing.T) {
		var rec DecodedDataRecord
		// 1.0 little-endian = 00 00 80 3F.
		size, err := decodeLVAR([]byte{0xF4, 0x00, 0x00, 0x80, 0x3F}, 0, &rec)
		require.NoError(t, err)
		assert.Equal(t, 4, size)
		assert.InDelta(t, 1.0, rec.RawValue, 0)
	})

	t.Run("float unsupported size", func(t *testing.T) {
		var rec DecodedDataRecord
		_, err := decodeLVAR([]byte{0xF8, 0, 0, 0, 0, 0, 0, 0, 0}, 0, &rec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported float size: 8")
	})

	t.Run("reserved LVAR", func(t *testing.T) {
		var rec DecodedDataRecord
		_, err := decodeLVAR([]byte{0xFB, 0x00}, 0, &rec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reserved LVAR value")
	})
}
