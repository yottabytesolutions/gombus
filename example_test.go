package gombus_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/yottabytesolutions/gombus"
)

// heatMeterResponse is a variable data response (CI=0x72) from a heat meter:
// current and previous-period energy, volume, power, the two temperatures, the
// meter's clock and the billing date. It stands in for the bytes a real slave
// puts on the bus.
const heatMeterResponse = "68393968080172785634122d2c1b042a000000" +
	"04063930000044069d2a00000413a0860100042d64000000" +
	"025b4a00025f2d00046d0a0ccd13426cbf1cad16"

// mustDecodeFrame turns the hex fixture above into a frame. Real callers get
// these bytes from Client.ReadSingleFrame instead.
func mustDecodeFrame() *gombus.DecodedFrame {
	raw, err := hex.DecodeString(heatMeterResponse)
	if err != nil {
		panic(err)
	}
	frame, err := gombus.LongFrame(raw).Decode()
	if err != nil {
		panic(err)
	}
	return frame
}

// Decoding a response yields the meter's identity and its data records. A
// record holds either a number in Value or a timestamp in Time, and Unit.Date
// says which.
func ExampleLongFrame_Decode() {
	raw, err := hex.DecodeString(heatMeterResponse)
	if err != nil {
		log.Fatal(err)
	}

	frame, err := gombus.LongFrame(raw).Decode()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s %d (%s)\n", frame.Manufacturer, frame.SerialNumber, frame.DeviceType)
	for _, r := range frame.DataRecords {
		if r.Unit.Date != gombus.DateNone {
			fmt.Printf("storage %d  %s\n", r.StorageNumber, r.Time.Format(time.DateTime))
			continue
		}
		fmt.Printf("storage %d  %12.3f %s\n", r.StorageNumber, r.Value, r.Unit.Unit)
	}

	// Output:
	// KAM 12345678 (Heat: Outlet)
	// storage 0  12345000.000 WH
	// storage 1  10909000.000 WH
	// storage 0       100.000 m^3
	// storage 0     10000.000 W
	// storage 0        74.000 C
	// storage 0        45.000 C
	// storage 0  2014-03-13 12:10:00
	// storage 1  2013-12-31 00:00:00
}

// Value reads one current reading by VIF type and function. A missing record
// is an error, never a silent zero.
func ExampleDecodedFrame_Value() {
	frame := mustDecodeFrame()

	energy, err := frame.Value(gombus.VIFEnergyWh, gombus.FunctionInstantaneous)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("energy %.0f Wh\n", energy)

	// This meter reports no mass, so the lookup fails instead of returning 0.
	_, err = frame.Value(gombus.VIFMass, gombus.FunctionInstantaneous)
	fmt.Println("mass present:", !errors.Is(err, gombus.ErrNoRecord))

	// Output:
	// energy 12345000 Wh
	// mass present: false
}

// Find selects the first record matching every matcher. Value and Record only
// reach current readings, so historic and tariff registers are found this way.
func ExampleDecodedFrame_Find() {
	frame := mustDecodeFrame()

	previous, ok := frame.Find(
		gombus.MatchType(gombus.VIFEnergyWh),
		gombus.MatchStorage(1),
	)
	if !ok {
		log.Fatal("no previous-period energy in this frame")
	}
	fmt.Printf("previous period %.0f %s\n", previous.Value, previous.Unit.Unit)

	billing, ok := frame.Find(gombus.MatchTimestamp(), gombus.MatchStorage(1))
	if !ok {
		log.Fatal("no billing date in this frame")
	}
	fmt.Println("billing date", billing.Time.Format(time.DateOnly))

	// Output:
	// previous period 10909000 WH
	// billing date 2013-12-31
}

// FindAll returns every matching record in frame order. It is how a meter's
// storage or tariff registers are read out together.
func ExampleDecodedFrame_FindAll() {
	frame := mustDecodeFrame()

	for _, r := range frame.FindAll(gombus.MatchType(gombus.VIFEnergyWh)) {
		fmt.Printf("storage %d  %.0f %s\n", r.StorageNumber, r.Value, r.Unit.Unit)
	}

	// A matcher is an ordinary predicate, so a closure covers what the
	// provided matchers do not.
	above50 := frame.FindAll(func(r gombus.DecodedDataRecord) bool {
		return r.Unit.Unit == "C" && r.Value > 50
	})
	fmt.Println("temperatures above 50 C:", len(above50))

	// Output:
	// storage 0  12345000 WH
	// storage 1  10909000 WH
	// temperatures above 50 C: 1
}

// Timestamp returns the meter's own clock. It is the meter's wall clock with
// no zone, so do not convert it between zones and expect meaning.
func ExampleDecodedFrame_Timestamp() {
	frame := mustDecodeFrame()

	clock, ok := frame.Timestamp()
	if !ok {
		log.Fatal("this meter does not report its clock")
	}
	fmt.Println(clock.Format(time.DateTime))

	// Output:
	// 2014-03-13 12:10:00
}

// Reading a meter over a TCP to M-Bus gateway. Keep one Client for the
// lifetime of the transport so consecutive frames stay aligned on the stream.
func ExampleClient_ReadSingleFrame() {
	conn, err := gombus.DialTCP("192.168.1.10:10001")
	if err != nil {
		log.Fatal(err)
	}

	client := gombus.NewClient(conn)
	defer func() {
		if err := client.Close(); err != nil {
			log.Print(err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	frame, err := client.ReadSingleFrame(ctx, 1)
	if err != nil {
		log.Print(err)
		return
	}

	power, err := frame.Value(gombus.VIFPowerW, gombus.FunctionInstantaneous)
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Printf("%s %d: %.0f W\n", frame.Manufacturer, frame.SerialNumber, power)
}

// Finding the meters on an RS-485 segment by primary address. Silence at an
// address is ordinary during a scan and is not an error, so a sweep of a wide
// range costs one answer window per empty address.
func ExampleClient_ScanPrimary() {
	conn, err := gombus.DialSerial("/dev/ttyUSB0")
	if err != nil {
		log.Fatal(err)
	}

	client := gombus.NewClient(conn)
	defer func() {
		if err := client.Close(); err != nil {
			log.Print(err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	addresses, err := client.ScanPrimary(ctx, 1, 20)
	if err != nil {
		log.Print(err)
		return
	}
	for _, addr := range addresses {
		fmt.Println("slave at primary address", addr)
	}
}

// Reading a meter whose primary address is unassigned or unknown. The search
// fixes the identification number one BCD digit at a time, so a bus of a few
// meters takes tens of selections rather than a sweep of the number space.
func ExampleClient_ScanSecondary() {
	conn, err := gombus.DialSerial("/dev/ttyUSB0")
	if err != nil {
		log.Fatal(err)
	}

	client := gombus.NewClient(conn)
	defer func() {
		if err := client.Close(); err != nil {
			log.Print(err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	slaves, err := client.ScanSecondary(ctx)
	if err != nil {
		log.Print(err)
		return
	}

	for _, sec := range slaves {
		frame, err := client.ReadBySecondary(ctx, sec)
		if err != nil {
			log.Printf("reading %s %s: %v", sec.Manufacturer, sec.Mask(), err)
			continue
		}
		fmt.Printf("%s %s (%s)\n", sec.Manufacturer, sec.Mask(), frame.DeviceType)
	}
}

// Reading one known meter by its secondary address, without scanning. Version
// and medium are wildcarded, so only the number and the manufacturer have to
// be right.
func ExampleClient_ReadBySecondary() {
	conn, err := gombus.DialSerial("/dev/ttyUSB0")
	if err != nil {
		log.Fatal(err)
	}

	client := gombus.NewClient(conn)
	defer func() {
		if err := client.Close(); err != nil {
			log.Print(err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	frame, err := client.ReadBySecondary(ctx, gombus.SecondaryAddress{
		ID:           12345678,
		Manufacturer: "KAM",
	})
	if err != nil {
		log.Print(err)
		return
	}

	volume, err := frame.Value(gombus.VIFVolume, gombus.FunctionInstantaneous)
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Printf("%.3f m^3\n", volume)
}
