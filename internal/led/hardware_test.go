package led

import (
	"os"
	"path/filepath"
	"testing"
)

func hardwareFixture(t *testing.T) string {
	r := t.TempDir()
	writeFixture(t, r, "class/dmi/id/product_name", "DXP2800\n")
	writeFixture(t, r, "class/dmi/id/bios_version", "EM_DXP2800_SUB2_V1.0.27")
	writeFixture(t, r, "class/dmi/id/product_version", "EM_DXP2800_SUB2_V1.0.27")
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
