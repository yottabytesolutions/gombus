package wmbus

import "testing"

// FuzzParse feeds arbitrary bytes to the wireless decoder. The input comes off
// the air and nothing about it is trustworthy, so the contract under fuzz is
// the same narrow one the wired package holds: every entry point returns a
// result or an error, and never panics.
func FuzzParse(f *testing.F) {
	plain := append(shortHeader(0x2A, 0x00, 0x0000), testRecords...)

	seeds := [][]byte{
		buildFormatA(testAddress, plain),
		buildFormatB(testAddress, plain),
		buildStripped(testAddress, plain),
		buildFormatA(testAddress, append([]byte{CINoHeader}, testRecords...)),
		buildFormatA(testAddress, []byte{CIExtendedLinkII, 0x00, 0x2A, 0, 0, 0, 0}),
		{},
		{0x00},
		{0xFF, 0x44, 0x2D, 0x2C},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(
		func(_ *testing.T, data []byte) {
			for _, parse := range []func([]byte) (*Frame, error){Parse, ParseFormatA, ParseFormatB, ParseWithoutCRC} {
				frame, err := parse(data)
				if err != nil {
					continue
				}
				// The accessors and both decode paths are part of the same
				// untrusted input surface.
				_ = frame.Manufacturer()
				_, _ = frame.SerialNumber()
				_, _ = frame.Decode(nil)
				_, _ = frame.Decode(testKey)
				_, _ = frame.DecodeWithKeyRing(KeyRing{78563412: testKey})
			}
		},
	)
}
