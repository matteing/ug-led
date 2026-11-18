//go:build linux && amd64

package led

import (
	"testing"
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
