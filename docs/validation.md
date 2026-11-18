# Validation

## Completed before initial publication

- Go unit tests and race detector, plus `go vet` (including Linux amd64 code).
- Status decoder fuzzing: over 2.2 million generated cases in a 10-second run.
- Native Linux amd64 and macOS arm64 builds.
- Nix formatting and flake checks on macOS arm64 and Linux amd64.
- NixOS module evaluation, generated-config validation, and package tests on Linux.
- Offline inspection of official DXP2800 UGOS 1.17.0.0095; catalog checksum matched.
- Read-only sysfs hardware discovery on an Intel DXP2800, BIOS family
  `EM_DXP2800_SUB2_V1.0.27`, HT32F522 ACPI identity and Intel I801 SMBus.
- Unprivileged daemon dry run using the actual two SATA disks and Ethernet counters.

## Performance measurements

An initial 30-second sample on the Intel N100 DXP2800 at the default 200 ms
interval used **0.08 seconds of CPU**, approximately **0.27% of one core**, with
**3400 KiB resident memory**. This was a dry run: no I²C descriptor was opened.
A Nix build was running concurrently. CPU accounting used the difference in
`utime + stime` from `/proc/PID/stat` and the system clock-tick frequency; memory
came from `VmRSS`. Short measurements have limited resolution.

The parser benchmark on an Apple M1 Pro was approximately **79 ns/op**, with
**zero allocations**. A fixture benchmark sampling two disks and a NIC took
approximately **103 microseconds/op**, with 1624 bytes and 19 allocations per
sample. These filesystem fixtures are not measurements of Linux sysfs or I²C.

On the Intel N100 itself, the Linux fixture benchmark measured approximately
**43 microseconds/sample** for two disks and one NIC, with 1256 bytes and
19 allocations per sample. Parsing alone took **167 ns**, with zero allocations.
These were filesystem fixtures too; the daemon CPU measurement above used the
actual sysfs counters.

The important operational bound is five sampling cycles per second by default,
not one operation per disk I/O. Constant activity does not cause continuous LED
writes. Brightness/color/mode commands are cached, hardware generates blinking,
and read-only reconciliation runs every 30 seconds. Actual MCU command time,
idle-package power impact and long-duration behavior remain to be measured.

## Hardware acceptance still required

Root-only `/dev/i2c-*` access was not available without interactive sudo during
initial development. Consequently, this release does **not** claim verified
physical LED writes, colors or brightness on the test unit.

After reviewing the code and ensuring no other controller is running:

1. Run `ug-led inspect` and `sudo ug-led status`. All four status checks must pass.
2. Save the status output. Test one LED at brightness 16 or 32, then RGB primaries,
   steady/off, and slow blink/breath. Verify readback and visible behavior.
3. Verify disk1 and disk2 correspond to physical bays using a known existing
   workload, or an explicitly chosen read-only workload. Do not write raw disks.
4. Run the daemon briefly with `--duration 30s`. Confirm activity, idle behavior,
   and restoration of the original LED state on normal exit.
5. Validate night/week schedule boundaries, service stop/restart, and reboot
   persistence after deliberately enabling the NixOS module.
6. Observe kernel logs for I²C errors. Confirm sleeping disks stay asleep using
   an independently validated method; the daemon itself never asks drives for
   power state. Measure longer-term CPU, memory and idle power if relevant.

Fault tests use the fake transport in unit tests; do not disconnect components
or inject arbitrary I²C commands on a live NAS. If controller access fails,
the service stops. Inspect the cause before restarting it.
