# DXP2800 firmware research

These are observations from offline inspection, not claims of vendor support.
No firmware was installed, executed, flashed, or copied into this repository.

## Source and reproducibility

The public [UGREEN DXP2800 catalog](https://api-us.ugnas.com/api/system/v2/sa/official/getFirmwareList?modelId=2)
identifies model 2, firmware **1.17.0.0095**, download id **2213**, kernel
**6.12.30**. The official download resolver is
`https://api-us.ugnas.com/api/system/v1/ua/temp/link?appType=firmware&id=2213&model=2`.
Its JSON `data.linkData.tempUrl` provides the temporary download URL. Temporary
URLs are not stored in the repository. The image is a GNU tar archive, despite
the `.img` extension.

Catalog MD5 (verified): `06f220ef1306af590c32b2017c535cd9`.
Computed image SHA-256: `7bb2746324ac852475727cf23f7016517996b91dfb293d2292d531c1e71581b0`.
MD5 establishes agreement with the catalog, not an independent authenticity proof.

Extract only the relevant archive members into a separate scratch directory:

- `rootfs-base.squashfs`: `etc/led.conf`, `usr/sbin/hwmonitor`
- `kernel.squashfs`: `usr/lib/modules/6.12.30+/kernel/drivers/ugreen/leds-mcu-28a48.ko`

Use `unsquashfs` for those paths and `llvm-objdump -dr --no-show-raw-insn` for
static analysis. `strings` and the ELF symbol table help locate functions.
Do not run vendor executables on the host.

| Extracted file | SHA-256 |
| --- | --- |
| `leds-mcu-28a48.ko` | `28f2f0571437cf98bcc1d3bcc9cdce9f640f8ec8b263458f0935e63b242a04ff` |
| `hwmonitor` | `e3a2891a20fcdbab1c90374eff0ba9b5d6840634021bb3d43e681831e89b03be` |
| `led.conf` | `590ba00467ae7daf25bc009460d5dfeabd91ea373c803e473ef602e8af5a6ff8` |

This was an inspection of one official image, not an exhaustive audit of every
UGOS release or board revision. The image contains several platform drivers;
this report focuses on the HT32F52231 28/48-family implementation and cross-checks
its wire format against the community userspace implementation.

## What the vendor implementation shows

### Brightness and schedule

`etc/led.conf` contains seven rows of 24 hourly values, initially 100.
In `hwmonitor`, `get_current_user_brightness` at `0xdbaf` calls `localtime`,
indexes `led_conf` by `weekday * 24 + hour`, and converts percentage to a byte
with integer `value * 255 / 100`. It special-cases 1 percent to byte value 2,
which agrees with that integer formula for valid inputs.

The kernel driver's `leds_brightness_set` at `.text+0x1250` rejects values above
`0xff`. The service also uses `state`, `trigger`, `mode`, `color`, `brightness`,
`delay_on`, `delay_off`, `device_name`, `link`, `tx`, and `rx` sysfs attributes.
Strings reference `normal`, `breath`, `timer2`, and `netdev2` triggers.

ug-led exposes per-LED RGB and brightness, on/off, timed blinking and breathing,
and a seven-by-24 weekly percentage schedule. Its night window is an additional
convenience. Scheduled values cap the configured per-LED brightness, providing
a deliberate ceiling rather than always replacing it with the scheduled value.

UGOS also distinguishes disk standby and health, and checks outside-network
reachability. ug-led does not reproduce those policies: it does not infer sleep
from inactivity or health from counters, issue ATA commands, or ping hosts.

### LED protocol

The original community research is
[miskcoo/ugreen_leds_controller](https://github.com/miskcoo/ugreen_leds_controller),
reviewed at revision `f0fcd8192abdd92244aba9f6f0d10572f5561380`.
Its MIT-licensed CLI and the official binary agree on the following operations:

- Intel SMBus, slave address `0x3a`.
- Four supported front-panel IDs here: power 0, network 1, disk1 2, disk2 3.
- SMBus I²C-block write, register equal to LED id, 12-byte payload:
  `[id, 0xa0, 0x01, 0, 0, command, p0, p1, p2, p3, sum_hi, sum_lo]`.
- The checksum sums bytes 1–9, excluding the LED id.
- Command 1: brightness; 2: RGB; 3: off/on; 4: blink; 5: breath.
- Blink/breath parameters encode total cycle and on-time as two big-endian
  16-bit values. The second phase is total minus on-time.
- Byte-data acknowledgement at register `0x80`, with 1 indicating success.
- 11-byte status at `0x81 + id`: mode, brightness, RGB, cycle, on-time,
  and a two-byte big-endian sum of the preceding nine bytes.

Evidence locations in the official 28a48 driver:

- `led_init_read_state`, `.text+0x310`: reads 11 bytes from `0x81 + id`,
  sums the first nine, and validates the nonzero checksum.
- `led_blink_or_breath.isra.0`, `.text+0x13e0`: frame construction,
  big-endian timings, checksum, 12-byte `i2c_smbus_write_i2c_block_data`,
  and acknowledgement read at `0x80`.
- `color_set`, `.text+0x1af0`: color command handling.
- `led_set_on_off.isra.0`, `.text+0x1eb0`: on/off command handling.
- `mcu_work`, `.text+0x2150`: brightness command and cached-state handling.

The vendor write path uses short delays before/after writes and retries following
an unsuccessful acknowledgement. ug-led deliberately spaces all transactions by
at least 10 ms, limits negative-ack retries to three, and stops on transport or
unexpected-response errors. It exposes no raw-register, bootloader, firmware
update, reset, EEPROM or voltage/current-setting command.

## Limits of the safety argument

These are ordinary LED-state commands within the inspected driver's accepted
range. RGB and brightness select outputs; they do not constitute a documented
way to set LED current limits. This reduces risk substantially compared with
unrestricted I²C writes, but cannot prove that every board or MCU firmware
revision behaves identically. DMI/BIOS/ACPI guards and valid readback are
necessary checks, not electrical certification.

[Upstream issue #128](https://github.com/miskcoo/ugreen_leds_controller/issues/128)
confirms that `DXP2800 V1.2 251215` system-version units use a different write
protocol even though reads work. This is why ug-led checks `product_version`
as well as the model and BIOS family before opening the adapter.

A model name alone is insufficient. V1.2, GT and other variants require their
own investigation. There is no `--force` escape hatch. Add support only with
verified identities, protocol evidence and tests.

Only findings and original source code are published. Vendor binaries,
disassemblies, firmware images, temporary signed links, and machine-specific
private configuration remain outside the public repository.
