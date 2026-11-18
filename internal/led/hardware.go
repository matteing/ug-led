package led

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Hardware struct {
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
	bus := strings.TrimPrefix(filepath.Base(h.Device), "i2c-")
	for _, name := range []string{bus + "-003a", "i2c-HT32F522:00"} {
		if _, err := os.Lstat(filepath.Join(root, "bus/i2c/devices", name, "driver")); err == nil {
			return h, errors.New("LED device already bound to a kernel driver")
		}
	}
	return h, nil
}
