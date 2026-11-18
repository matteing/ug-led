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

The initial analysis used 1.17; the SUB2 investigation below also checks 1.21.
This is not an exhaustive audit of every UGOS release or board revision.
The images contain several platform drivers;
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

- Intel SMBus or Synopsys DesignWare, vendor slave address `0x3a`.
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


## SUB2 board discovery

A target with BIOS and product-version `EM_DXP2800_SUB2_V1.0.27` exposed its
HT32F522 ACPI device under `\_SB_.PC00.I2C1.MCU0`, on the Intel PCI controller
`0000:00:15.1`. Linux linked it to a Synopsys DesignWare adapter, separate from
the I801 SMBus. A read on I801/0x3a failed with ENXIO, matching the independently
reported [upstream issue 133](https://github.com/miskcoo/ugreen_leds_controller/issues/133).

The board's DSDT `_CRS` says `I2cSerialBusV2(0x0074, ..., AddressingMode7Bit, ...)`
at 100 kHz, but a status read at that declared address failed with remote I/O.
The vendor driver supplies the decisive distinction: both UGOS 1.17.0.0095 and
**1.21.1.0016** enumerate adapter names beginning `SMBus` or `Synopsys`, create an
I2C client with **7-bit address 0x3a**, and check SMBus word register `0x5a` for
chip identity **0xc5b2**. They do not use the ACPI-declared address for this client.

In the 1.21 driver, `leds_ht32f52231_init` is at `.init.text+0x10`; name checks
reference `.rodata.str1.1+0xb3` (SMBus) and `+0xb9` (Synopsys). The client
address is `.data+0x16` (`3a 00`), the chip read is at `.init.text+0xca`, and the
identity comparison at `+0xe0`. LED reads remain 11-byte I2C-block reads and
writes remain 12-byte I2C-block writes using the documented frames.

The 1.21 image is official catalog id 2551, MD5
`389fe6f2a432e0a82763da040a519660` (verified), with kernel 6.18.15. Its extracted
`leds-mcu-28a48.ko` SHA-256 is
`4b589ae88ec1dfb23d20cf872c66738120533c37b70e95a057573b419d09a4a1`.
Only offline disassembly was used; no vendor code or firmware was loaded.

Using the ACPI-linked DesignWare adapter **and the vendor address 0x3a** passed
the MCU identity check and returned valid checksums for all four LEDs on the
SUB2 target. One earlier disk response failed checksum validation; a later raw
read succeeded. `Refresh` therefore permits at most three paced checksum reads,
while transport errors/unsupported states still abort. Raw diagnostics preserve
the original response and report failures without writes or retries.

ug-led's SUB2 profile requires the exact BIOS/product-version, expected ACPI
path and physical-node link, matching i2c-dev parent, Intel PCI vendor/location,
DesignWare adapter name and MCU identity. It does not hardcode a Linux bus
number, scan other buses/addresses, or assume every DesignWare/EM board is safe.
The raw DSDT and machine-specific dump remain outside the public repository.

## Acknowledgement handling and settling time (0.2.2)

A deployed SUB2 unit stopped three times on 2026-11-18 after three zero-valued
ACK bytes. These were successful I2C transfers returning `0x00`, not transport
NACK errors. Version 0.2.1 logged this as a rejected command but did not capture
the command or subsequent state, so those logs cannot establish whether the
setting actually took effect. The underlying cause remains unconfirmed.

Both inspected vendor drivers wait **after** each write returns before reading
ACK: UGOS 1.17 at `.text+0x1500` and 1.21 at `.text+0x14bc` request
`usleep_range_state(1500, 2500, 2)` in the blink path; unsuccessful ACKs cause a
30 ms delay before another write. Our former start-to-start 10 ms pacing could
leave no settling interval after an ioctl taking 10 ms or longer. Version 0.2.2
waits at least 10 ms after every transfer finishes, including status reads.
This is a verified software timing defect, not proof it caused all three stops.

A zero ACK now causes a 50 ms settling wait and at most three read-only status
checks. Two consecutive checksum-valid responses must match the specific
brightness, RGB or mode/timing requested. Only then is the command considered
applied without resending it. One matching or corrupt response is insufficient.
A mismatch can cause another attempt, still capped at three writes total.
Transport errors, unexpected ACK bytes and invalid status formats stop control.
No new command types or firmware operations are introduced, and service
restarts remain manual. Readback-confirmed missing ACKs are logged with a
cumulative count at most every 30 seconds; failures include command, parameters
and available readback. The change requires live validation on the affected NAS.

## SUB2 read transaction correction (0.2.3)

Live 0.2.2 logs showed that a requested setting could be confirmed despite an
ACK byte of zero, but periodic status reads still failed checksum validation.
A stopped-service capture also obtained MCU identity `0x0000` after a successful
read. Increasing write settling time had not resolved the read-side fault.

An independent Python/ctypes probe on the same exact SUB2 profile compared three
phases on one exclusively locked adapter. Each phase read the identity, ACK and
four LED states 32 times. It used no LED-setting commands. Transport errors would
abort the probe; none occurred. Here are the observed invalid responses:

| Method | Invalid LED statuses | Invalid chip IDs | Zero ACK bytes |
| --- | --- | --- | --- |
| Existing combined SMBus reads, before | 4 / 128 | 1 / 32 | 1 / 32 |
| Register selection, STOP, 10 ms wait, plain read | 0 / 128 | 0 / 32 | 0 / 32 |
| Existing combined SMBus reads, after | 15 / 128 | 3 / 32 | 0 / 32 |

Every invalid status was eleven zero bytes; every invalid ID was two zero
bytes. All valid IDs were `b2 c5` (little-endian `0xc5b2`). The comparison took
8.789 seconds. Reappearance when switching back implicates the combined read
transaction on this board/MCU; it does not prove the MCU's internal mechanism,
which part of STOP/delay is essential, or a universal requirement for all boards.

Version 0.2.3 therefore uses the successful sequence **only** for the exact
`em-sub2-acpi` profile, including the initial identity check. Each selection is
one byte naming an allowed read register: `0x5a`, `0x80`, or `0x81..0x84`.
It sends no value/LED-setting payload. Linux `write()` ends that selection with
STOP; after 10 ms, one `read()` requests the exact response length. The existing
adapter lock spans both transfers; as before, unrelated I2C software must not
access the MCU concurrently. Plain-I2C capability is required. Short transfers
and transport errors abort without retrying; checksums and chip identity are
still mandatory. No device address, bus speed, LED command frame, brightness
limit or retry budget changes. Legacy I801 profiles retain SMBus reads.

Linux documents the distinction between plain and combined transactions in
[the i2c-dev interface](https://docs.kernel.org/i2c/dev-interface.html).
The successful sample is evidence for this targeted correction, not an
endurance test or electrical certification. Longer live acceptance is required.
