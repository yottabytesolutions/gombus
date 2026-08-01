// Package gombus is a Go implementation of the wired M-Bus (Meter-Bus)
// protocol per EN 13757-3, with helpers for both TCP and serial transports.
//
// # Scope
//
// gombus is master-side: it builds the short and long frames a master sends
// on the bus, reads the slave's variable-data response (CI=0x72) or fixed-data
// response (CI=0x73), and decodes either into structured data records. A fixed
// response yields one record per counter, so the same queries work on both.
// Other CI fields are not interpreted; the package returns [ErrUnsupportedCI]
// for those.
//
// Manufacturer-specific data following a 0x0F or 0x1F DIF byte is exposed
// only as a sentinel record and is not parsed further.
//
// # Quick start
//
// Open a transport, wrap it in a [Client], request a frame, and inspect the
// records:
//
//	conn, err := gombus.DialTCP("192.168.1.10:10001")
//	if err != nil { /* ... */ }
//
//	client := gombus.NewClient(conn)
//	defer client.Close()
//
//	frame, err := client.ReadSingleFrame(ctx, 1)
//	if err != nil { /* ... */ }
//	for _, r := range frame.DataRecords {
//	    fmt.Printf("%s\t%v %s\n", r.Function, r.Value, r.Unit.Unit)
//	}
//
// For multi-frame slaves use [Client.ReadAllFrames], which walks the FCB bit
// until the slave reports no more records.
//
// Keep the Client for the lifetime of the transport rather than building one
// per read: it owns the bytes that arrive after a frame's stop byte, which is
// what keeps consecutive frames aligned on the stream.
//
// Every read and write is bounded by the earlier of the caller's context
// deadline and this package's own frame timeout, so a cancelled context ends an
// exchange instead of waiting it out.
//
// # Transports
//
// The [Conn] interface is the transport seam and it is deliberately net.Conn's
// shape, so every net.Conn satisfies it with no adapter: TCP, TLS, unix
// sockets, UDP. [DialTCP] and [DialSerial] are convenience helpers, not the
// contract. Anything that moves bytes with a deadline can implement Conn: a
// radio gateway, a USB dongle, a replay file, or an in-memory fake for tests.
//
// # Frame layout
//
// Long frames have the structure
//
//	68h LL LL 68h | C A CI | <user data> | CRC 16h
//
// where L is the user-data length repeated. Variable-data responses carry
// a 12-byte device identification block (ID/manufacturer/version/medium/
// access#/status/signature) followed by data records.
//
// Short frames are 5 bytes: 10h C A CRC 16h.
//
// # Specifications
//
// The reference is EN 13757-3 (also published online at
// https://m-bus.com/documentation-wired/06-application-layer). The Kamstrup
// Logger Profiles document is included in the repository for context on
// register-level semantics from one of the major meter vendors.
package gombus
