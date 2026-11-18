package led

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Hardware struct {
	Address       uint16 `json:"address"`
	Profile       string `json:"profile"`
	Model         string `json:"model"`
	BIOS          string `json:"bios"`
	SystemVersion string `json:"system_version"`
	Adapter       string `json:"adapter"`
	Device        string `json:"device"`
}

// Inspect reads sysfs only: no bus scan, address probing or device open.
func Inspect(root string) (Hardware, error) {
	var h Hardware
	b, err := os.ReadFile(filepath.Join(root, "class/dmi/id/product_name"))
	if err != nil {
		return h, err
	}
	h.Model = strings.TrimSpace(string(b))
	if h.Model != "DXP2800" {
		return h, fmt.Errorf("unsupported model %q (only original Intel DXP2800)", h.Model)
	}
	b, err = os.ReadFile(filepath.Join(root, "class/dmi/id/product_version"))
	if err != nil {
		return h, err
	}
	h.SystemVersion = strings.TrimSpace(string(b))
	if !strings.HasPrefix(h.SystemVersion, "EM_DXP2800_") {
		return h, errors.New("unsupported DXP2800 system version (V1.2 uses a different write protocol)")
	}
	b, err = os.ReadFile(filepath.Join(root, "class/dmi/id/bios_version"))
	if err != nil {
		return h, err
	}
	h.BIOS = strings.TrimSpace(string(b))
	if !strings.HasPrefix(h.BIOS, "EM_DXP2800_") {
		return h, errors.New("unsupported DXP2800 BIOS family; refusing hardware access")
	}
	b, err = os.ReadFile(filepath.Join(root, "bus/i2c/devices/i2c-HT32F522:00/modalias"))
	if err != nil || !strings.Contains(string(b), "acpi:HT32F522:") {
		return h, errors.New("expected HT32F522 ACPI device absent; refusing hardware access")
	}
	for _, name := range []string{"led_ugreen", "leds_mcu", "leds_mcu_28a48", "leds_mcu_68a88"} {
		if _, err := os.Stat(filepath.Join(root, "module", name)); err == nil {
			return h, fmt.Errorf("conflicting %s module loaded", name)
		}
	}
	// The SUB2 board declares an ACPI-routed DesignWare endpoint; the UGOS driver uses 0x3a.
	// Its separately enumerated I801 bus is not the LED controller's bus.
	if strings.HasPrefix(h.SystemVersion, "EM_DXP2800_SUB2_") || strings.HasPrefix(h.BIOS, "EM_DXP2800_SUB2_") {
		if h.SystemVersion != "EM_DXP2800_SUB2_V1.0.27" || h.BIOS != "EM_DXP2800_SUB2_V1.0.27" {
			return h, errors.New("unverified SUB2 revision; refusing hardware access")
		}
		if err := inspectSUB2(root, &h); err != nil {
			return h, err
		}
	} else {
		h.Address = 0x3a
		h.Profile = "em-legacy-i801"
		entries, err := filepath.Glob(filepath.Join(root, "class/i2c-dev/i2c-*"))
		if err != nil {
			return h, err
		}
		for _, path := range entries {
			b, err := os.ReadFile(filepath.Join(path, "name"))
			if err != nil {
				b, err = os.ReadFile(filepath.Join(path, "device/name"))
			}
			if err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "SMBus I801 adapter") {
				if h.Device != "" {
					return h, errors.New("multiple Intel SMBus adapters; refusing ambiguous access")
				}
				h.Adapter = strings.TrimSpace(string(b))
				h.Device = "/dev/" + filepath.Base(path)
			}
		}
		if h.Device == "" {
			return h, errors.New("no Intel SMBus i2c-dev adapter; load i2c-i801 and i2c-dev")
		}
	}
	bus := strings.TrimPrefix(filepath.Base(h.Device), "i2c-")
	for _, name := range []string{fmt.Sprintf("%s-%04x", bus, h.Address), "i2c-HT32F522:00"} {
		if _, err := os.Lstat(filepath.Join(root, "bus/i2c/devices", name, "driver")); err == nil {
			return h, errors.New("LED device already bound to a kernel driver")
		}
	}
	return h, nil
}

// This explicit profile comes from the target board's _CRS: HT32F522 at
// I2C1.MCU0. Its _CRS says 0x74, but both inspected UGOS drivers explicitly
// instantiate a client at 7-bit 0x3a on Synopsys adapters. Use that vendor
// address and require the vendor chip ID. Never scan buses or addresses.
func inspectSUB2(root string, h *Hardware) error {
	acpi := filepath.Join(root, "bus/acpi/devices/HT32F522:00")
	b, e := os.ReadFile(filepath.Join(acpi, "path"))
	if e != nil || strings.TrimSpace(string(b)) != `\_SB_.PC00.I2C1.MCU0` {
		return errors.New("SUB2 ACPI MCU path does not match verified profile")
	}
	device, e := filepath.EvalSymlinks(filepath.Join(root, "bus/i2c/devices/i2c-HT32F522:00"))
	if e != nil {
		return e
	}
	physical, e := filepath.EvalSymlinks(filepath.Join(acpi, "physical_node"))
	if e != nil || physical != device {
		return errors.New("ACPI physical node differs from I2C MCU")
	}
	adapter := filepath.Dir(device)
	name := filepath.Base(adapter)
	n, e := strconv.Atoi(strings.TrimPrefix(name, "i2c-"))
	if e != nil || n < 0 || name != fmt.Sprintf("i2c-%d", n) {
		return errors.New("invalid ACPI parent adapter")
	}
	// The i2c-dev node must belong to that exact adapter, independent of bus number.
	actual, e := filepath.EvalSymlinks(filepath.Join(root, "class/i2c-dev", name, "device"))
	if e != nil || actual != adapter {
		return errors.New("ACPI adapter has no matching i2c-dev node")
	}
	b, e = os.ReadFile(filepath.Join(adapter, "name"))
	if e != nil || strings.TrimSpace(string(b)) != "Synopsys DesignWare I2C adapter" {
		return errors.New("unexpected SUB2 adapter type")
	}
	h.Adapter = strings.TrimSpace(string(b))
	pci := filepath.Dir(filepath.Dir(adapter))
	b, e = os.ReadFile(filepath.Join(pci, "vendor"))
	if e != nil || strings.TrimSpace(string(b)) != "0x8086" {
		return errors.New("SUB2 adapter is not on the expected Intel PCI controller")
	}
	if filepath.Base(pci) != "0000:00:15.1" {
		return errors.New("unexpected SUB2 PCI controller location")
	}
	h.Device = "/dev/" + name
	h.Address = 0x3a
	h.Profile = "em-sub2-acpi"
	return nil
}
