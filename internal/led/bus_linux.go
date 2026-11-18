//go:build linux && amd64

package led

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// Linux UAPI: linux/i2c-dev.h and linux/i2c.h. amd64 only, matching DXP2800.
const (
	i2cSlave          = 0x0703
	i2cFuncs          = 0x0705
	i2cSMBus          = 0x0720
	smbusByteData     = 2
	smbusWordData     = 3
	smbusI2CBlock     = 8
	requiredFunctions = 0x00080000 | 0x04000000 | 0x08000000
)

type ioctlData struct {
	ReadWrite uint8
	Command   uint8
	_         [2]byte
	Size      uint32
	Data      *[34]byte
}
type linuxBus struct {
	f          *os.File
	splitReads bool
}

func ioctlPointer(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	runtime.KeepAlive(arg)
	if errno != 0 {
		return errno
	}
	return nil
}
func OpenBus(h Hardware) (Bus, error) {
	// Never accept caller-supplied paths or force-address access.
	actual, err := Inspect("/sys")
	if err != nil {
		return nil, err
	}
	if h != actual {
		return nil, errors.New("hardware identity changed")
	}
	f, err := os.OpenFile(h.Device, os.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (Bus, error) { f.Close(); return nil, e }
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if st.Mode()&os.ModeCharDevice == 0 {
		return fail(errors.New("adapter is not a character device"))
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("another ug-led process owns the adapter: %w", err))
	}
	var funcs uint64
	if err = ioctlPointer(f.Fd(), i2cFuncs, unsafe.Pointer(&funcs)); err != nil {
		return fail(err)
	}
	if funcs&requiredFunctions != requiredFunctions {
		return fail(errors.New("adapter lacks required SMBus operations"))
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), i2cSlave, uintptr(h.Address)); errno != 0 {
		return fail(errno)
	}
	bus := &linuxBus{f: f, splitReads: h.Profile == "em-sub2-acpi"}
	if h.Profile == "em-sub2-acpi" {
		if funcs&1 == 0 {
			return fail(errors.New("SUB2 adapter lacks plain I2C reads"))
		}
		data, readErr := bus.readRegister(0x5a)
		if readErr != nil {
			return fail(fmt.Errorf("SUB2 MCU identity at %s/0x3a: %w", h.Device, readErr))
		}
		if err = validateChipID(data[0], data[1]); err != nil {
			return fail(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return bus, nil
}
func (b *linuxBus) transfer(read, reg byte, size uint32, data *[34]byte) error {
	args := ioctlData{ReadWrite: read, Command: reg, Size: size, Data: data}
	err := ioctlPointer(b.f.Fd(), i2cSMBus, unsafe.Pointer(&args))
	runtime.KeepAlive(data)
	runtime.KeepAlive(&args)
	runtime.KeepAlive(b)
	return err
}
func (b *linuxBus) WriteFrame(id byte, frame [12]byte) error {
	if id > 3 || frame[0] != id {
		return errors.New("invalid LED id")
	}
	checked, err := Frame(id, frame[5], [4]byte{frame[6], frame[7], frame[8], frame[9]})
	if err != nil || checked != frame {
		return errors.New("refusing invalid command frame")
	}
	var data [34]byte
	data[0] = 12
	copy(data[1:], frame[:])
	return b.transfer(0, id, smbusI2CBlock, &data)
}

// readRegister admits only the MCU identity, acknowledgement and four LED
// status registers. Legacy I801 hardware retains its original SMBus reads.
func (b *linuxBus) readRegister(reg byte) ([]byte, error) {
	length, size, err := readRegisterFormat(reg)
	if err != nil {
		return nil, err
	}
	if b.splitReads {
		return readSplitRegister(b.f, reg)
	}
	var data [34]byte
	if length == 11 {
		data[0] = 11
	}
	if err := b.transfer(1, reg, size, &data); err != nil {
		return nil, err
	}
	if length == 11 {
		if data[0] != 11 {
			return nil, errors.New("short LED status response")
		}
		return data[1:12], nil
	}
	return data[:length], nil
}

func readRegisterFormat(reg byte) (int, uint32, error) {
	switch {
	case reg == 0x5a:
		return 2, smbusWordData, nil
	case reg == 0x80:
		return 1, smbusByteData, nil
	case reg >= 0x81 && reg <= 0x84:
		return 11, smbusI2CBlock, nil
	default:
		return 0, 0, errors.New("not an allowed MCU read register")
	}
}

// On the verified SUB2 MCU, combined SMBus reads intermittently return all
// zeroes, including for chip ID. A read-only A/B/A comparison isolated this to
// the combined read sequence. Separate register selection (one byte, STOP)
// from the read by 10 ms. These are NOT LED-setting frames. Keep the adapter
// flock for both transfers, never retry partial transfers, and never use
// io.ReadFull: each extra Read would be a separate hardware transaction.
func readSplitRegister(dev io.ReadWriter, reg byte) ([]byte, error) {
	length, _, err := readRegisterFormat(reg)
	if err != nil {
		return nil, err
	}
	n, err := dev.Write([]byte{reg})
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, errors.New("short MCU register selection")
	}
	time.Sleep(10 * time.Millisecond)
	data := make([]byte, length)
	n, err = dev.Read(data)
	if err != nil {
		return nil, err
	}
	if n != length {
		return nil, errors.New("short MCU register read")
	}
	return data, nil
}

func (b *linuxBus) ReadAck() (byte, error) {
	data, err := b.readRegister(0x80)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}
func (b *linuxBus) ReadStatus(id byte) ([]byte, error) {
	if id > 3 {
		return nil, errors.New("invalid LED id")
	}
	return b.readRegister(0x81 + id)
}
func (b *linuxBus) Close() error { return b.f.Close() }

func validateChipID(low, high byte) error {
	id := uint16(low) | uint16(high)<<8
	if id != 0xc5b2 {
		return fmt.Errorf("unexpected SUB2 MCU identity 0x%04x; refusing access", id)
	}
	return nil
}
