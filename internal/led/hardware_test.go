package led

import (
	"os"
	"path/filepath"
	"testing"
)

func hardwareFixture(t *testing.T) string {
	r := t.TempDir()
	writeFixture(t, r, "class/dmi/id/product_name", "DXP2800\n")
	writeFixture(t, r, "class/dmi/id/bios_version", "EM_DXP2800_V1.0.0")
	writeFixture(t, r, "class/dmi/id/product_version", "EM_DXP2800_V1.0.0")
	writeFixture(t, r, "bus/i2c/devices/i2c-HT32F522:00/modalias", "acpi:HT32F522:HT32F522:\n")
	writeFixture(t, r, "class/i2c-dev/i2c-3/name", "SMBus I801 adapter at 0000:00:1f.4\n")
	return r
}
func TestHardwareGuards(t *testing.T) {
	r := hardwareFixture(t)
	h, e := Inspect(r)
	if e != nil || h.Device != "/dev/i2c-3" {
		t.Fatal(h, e)
	}
	writeFixture(t, r, "class/dmi/id/product_name", "DXP2800 GT")
	if _, e = Inspect(r); e == nil {
		t.Fatal("GT accepted")
	}
	r = hardwareFixture(t)
	writeFixture(t, r, "class/dmi/id/product_version", "DXP2800 V1.2 251215")
	if _, e = Inspect(r); e == nil {
		t.Fatal("V1.2 accepted")
	}
	r = hardwareFixture(t)
	writeFixture(t, r, "class/i2c-dev/i2c-4/name", "SMBus I801 adapter extra")
	if _, e = Inspect(r); e == nil {
		t.Fatal("ambiguous bus accepted")
	}
	r = hardwareFixture(t)
	if e = os.MkdirAll(filepath.Join(r, "module/leds_mcu_28a48"), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e = Inspect(r); e == nil {
		t.Fatal("conflicting module accepted")
	}
	r = hardwareFixture(t)
	if e = os.Symlink("/driver", filepath.Join(r, "bus/i2c/devices/i2c-HT32F522:00/driver")); e != nil {
		t.Fatal(e)
	}
	if _, e = Inspect(r); e == nil {
		t.Fatal("bound driver accepted")
	}
}

func sub2Fixture(t *testing.T) string {
	r := hardwareFixture(t)
	writeFixture(t, r, "class/dmi/id/bios_version", "EM_DXP2800_SUB2_V1.0.27")
	writeFixture(t, r, "class/dmi/id/product_version", "EM_DXP2800_SUB2_V1.0.27")
	// Deliberately use bus 7 and retain the unrelated I801 bus 3: numbers vary.
	adapter := filepath.Join(r, "devices/pci0000:00/0000:00:15.1/i2c_designware.1/i2c-7")
	dev := filepath.Join(adapter, "i2c-HT32F522:00")
	if e := os.MkdirAll(dev, 0755); e != nil {
		t.Fatal(e)
	}
	old := filepath.Join(r, "bus/i2c/devices/i2c-HT32F522:00")
	if e := os.RemoveAll(old); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(dev, old); e != nil {
		t.Fatal(e)
	}
	writeFixture(t, r, "bus/i2c/devices/i2c-HT32F522:00/modalias", "acpi:HT32F522:HT32F522:")
	writeFixture(t, r, "bus/acpi/devices/HT32F522:00/path", `\_SB_.PC00.I2C1.MCU0`)
	if e := os.Symlink(dev, filepath.Join(r, "bus/acpi/devices/HT32F522:00/physical_node")); e != nil {
		t.Fatal(e)
	}
	writeFixture(t, r, "devices/pci0000:00/0000:00:15.1/vendor", "0x8086")
	writeFixture(t, r, "devices/pci0000:00/0000:00:15.1/i2c_designware.1/i2c-7/name", "Synopsys DesignWare I2C adapter")
	writeFixture(t, r, "class/i2c-dev/i2c-7/name", "Synopsys DesignWare I2C adapter")
	if e := os.Symlink(adapter, filepath.Join(r, "class/i2c-dev/i2c-7/device")); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestSUB2UsesDeclaredBus(t *testing.T) {
	r := sub2Fixture(t)
	h, e := Inspect(r)
	if e != nil || h.Device != "/dev/i2c-7" || h.Address != 0x3a || h.Profile != "em-sub2-acpi" {
		t.Fatal(h, e)
	}
	for _, modify := range []func(string){
		func(r string) { writeFixture(t, r, "bus/acpi/devices/HT32F522:00/path", `\_SB_.PC00.I2C2.MCU0`) },
		func(r string) { writeFixture(t, r, "devices/pci0000:00/0000:00:15.1/vendor", "0x1022") },
		func(r string) { writeFixture(t, r, "class/dmi/id/bios_version", "EM_DXP2800_SUB2_V1.0.28") },
		func(r string) {
			if e := os.Remove(filepath.Join(r, "class/i2c-dev/i2c-7/device")); e != nil {
				t.Fatal(e)
			}
		},
		func(r string) {
			if e := os.Symlink("/driver", filepath.Join(r, "bus/i2c/devices/i2c-HT32F522:00/driver")); e != nil {
				t.Fatal(e)
			}
		},
	} {
		r := sub2Fixture(t)
		modify(r)
		if _, e := Inspect(r); e == nil {
			t.Fatal("unverified SUB2 topology accepted")
		}
	}
}
