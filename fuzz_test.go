package gombus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzDecode feeds arbitrary bytes to the long-frame decoder. The decoder
// parses attacker-controllable input from the bus, so the contract under fuzz
// is narrow: Decode returns a frame or an error, and never panics.
func FuzzDecode(f *testing.F) {
	for _, dir := range []string{"frames", "error-frames"} {
		entries, err := os.ReadDir(filepath.Join("testdata", dir))
		if err != nil {
			f.Fatalf("reading seed corpus %s: %v", dir, err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".hex") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join("testdata", dir, e.Name()))
			if err != nil {
				f.Fatalf("reading seed %s: %v", e.Name(), err)
			}
			f.Add(hexToBytes(string(raw)))
		}
	}

	f.Fuzz(func(_ *testing.T, data []byte) {
		lf := LongFrame(data)

		// DecodeManufacturer is exported and reachable on a raw LongFrame
		// without going through Decode, so it needs its own coverage. Targeting
		// only Decode is why its panic on a short frame went unnoticed.
		_, _ = lf.DecodeManufacturer()

		df, err := lf.Decode()
		if err != nil {
			return
		}
		// Exercise the accessors on a successfully decoded frame: they are
		// part of the same untrusted-input surface.
		if df != nil {
			_ = df.HasMoreRecords()
			_ = df.SecondaryAddressString()
			_ = df.ReadableStatus()
		}
	})
}
