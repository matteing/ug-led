//go:build linux && amd64

package led

import (
	"bytes"
	"errors"
	"testing"
	"time"
	"unsafe"
)

func TestLinuxIOCTLABI(t *testing.T) {
	var d ioctlData
	if unsafe.Sizeof(d) != 16 || unsafe.Offsetof(d.ReadWrite) != 0 || unsafe.Offsetof(d.Command) != 1 || unsafe.Offsetof(d.Size) != 4 || unsafe.Offsetof(d.Data) != 8 {
		t.Fatal("SMBus ioctl structure does not match Linux amd64 UAPI")
	}
}
func TestRawBusRejectsInvalidWritesBeforeIOCTL(t *testing.T) {
	b := &linuxBus{}
	if e := b.WriteFrame(4, [12]byte{}); e == nil {
		t.Fatal("invalid LED accepted")
	}
	frame, _ := Frame(0, 1, [4]byte{64})
	frame[11] ^= 1
	if e := b.WriteFrame(0, frame); e == nil {
		t.Fatal("invalid checksum accepted")
	}
	if _, e := b.ReadStatus(4); e == nil {
		t.Fatal("invalid status register accepted")
	}
}

func TestSUB2ChipIdentity(t *testing.T) {
	if e := validateChipID(0xb2, 0xc5); e != nil {
		t.Fatal(e)
	}
	for _, b := range [][2]byte{{0, 0}, {0xff, 0xff}, {0xc5, 0xb2}, {0xb3, 0xc5}} {
		if validateChipID(b[0], b[1]) == nil {
			t.Fatal("unknown MCU accepted", b)
		}
	}
}

type readRegisterDevice struct {
	response              []byte
	writes                [][]byte
	reads                 int
	writeErr, readErr     error
	shortWrite, shortRead bool
	writeDelay            time.Duration
	selectedAt, readAt    time.Time
}

func (d *readRegisterDevice) Write(p []byte) (int, error) {
	d.writes = append(d.writes, append([]byte(nil), p...))
	time.Sleep(d.writeDelay)
	d.selectedAt = time.Now()
	if d.writeErr != nil {
		return 0, d.writeErr
	}
	if d.shortWrite {
		return 0, nil
	}
	return len(p), nil
}
func (d *readRegisterDevice) Read(p []byte) (int, error) {
	d.reads++
	d.readAt = time.Now()
	if d.readErr != nil {
		return 0, d.readErr
	}
	n := copy(p, d.response)
	if d.shortRead {
		n--
	}
	return n, nil
}
func TestSUB2SplitReadSelectsOnlyOneByteAndWaitsAfterSelection(t *testing.T) {
	for _, reg := range []byte{0x5a, 0x80, 0x81, 0x82, 0x83, 0x84} {
		length, _, err := readRegisterFormat(reg)
		if err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, length)
		for i := range reply {
			reply[i] = byte(i + 1)
		}
		d := &readRegisterDevice{response: reply, writeDelay: 15 * time.Millisecond}
		got, err := readSplitRegister(d, reg)
		if err != nil || !bytes.Equal(got, reply) {
			t.Fatal(reg, got, err)
		}
		if len(d.writes) != 1 || !bytes.Equal(d.writes[0], []byte{reg}) || d.reads != 1 {
			t.Fatal(d.writes, d.reads)
		}
		if d.readAt.Sub(d.selectedAt) < 9*time.Millisecond {
			t.Fatal("read started before MCU settling interval")
		}
	}
}
func TestSUB2SplitReadRejectsOtherRegistersBeforeIO(t *testing.T) {
	allowed := map[byte]bool{0x5a: true, 0x80: true, 0x81: true, 0x82: true, 0x83: true, 0x84: true}
	for reg := 0; reg < 256; reg++ {
		if allowed[byte(reg)] {
			continue
		}
		// nil is intentional: forbidden selectors must be rejected before any I/O.
		if _, err := readSplitRegister(nil, byte(reg)); err == nil {
			t.Fatalf("accepted register 0x%02x", reg)
		}
	}
}
func TestSUB2SplitReadNeverRetriesPartialOrFailedTransfer(t *testing.T) {
	failure := errors.New("transfer failed")
	for _, tc := range []struct {
		name  string
		dev   readRegisterDevice
		reads int
	}{
		{"write error", readRegisterDevice{writeErr: failure}, 0},
		{"short selection", readRegisterDevice{shortWrite: true}, 0},
		{"read error", readRegisterDevice{readErr: failure}, 1},
		{"short response", readRegisterDevice{response: make([]byte, 11), shortRead: true}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readSplitRegister(&tc.dev, 0x81); err == nil {
				t.Fatal("invalid transfer accepted")
			}
			if len(tc.dev.writes) != 1 || tc.dev.reads != tc.reads {
				t.Fatal("unexpected retry", tc.dev.writes, tc.dev.reads)
			}
		})
	}
}
