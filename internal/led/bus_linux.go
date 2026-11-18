//go:build linux && amd64

package led

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// Linux UAPI: linux/i2c-dev.h and linux/i2c.h. amd64 only, matching DXP2800.
const (
	i2cSlave          = 0x0703
	i2cFuncs          = 0x0705
	i2cSMBus          = 0x0720
	smbusByteData     = 2
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
type linuxBus struct{ f *os.File }

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
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), i2cSlave, 0x3a); errno != 0 {
		return fail(errno)
	}
	return &linuxBus{f}, nil
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
func (b *linuxBus) ReadAck() (byte, error) {
	var data [34]byte
	err := b.transfer(1, 0x80, smbusByteData, &data)
	return data[0], err
}
func (b *linuxBus) ReadStatus(id byte) ([]byte, error) {
	if id > 3 {
		return nil, errors.New("invalid LED id")
	}
	var data [34]byte
	data[0] = 11
	if err := b.transfer(1, 0x81+id, smbusI2CBlock, &data); err != nil {
		return nil, err
	}
	if data[0] != 11 {
		return nil, errors.New("short LED status response")
	}
	return data[1:12], nil
}
func (b *linuxBus) Close() error { return b.f.Close() }
