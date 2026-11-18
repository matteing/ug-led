# Validation

## Completed before initial publication

- Go unit tests and race detector, plus `go vet` (including Linux amd64 code).
- Status decoder fuzzing: over 2.2 million generated cases in a 10-second run.
- Native Linux amd64 and macOS arm64 builds.
- Nix formatting and flake checks on macOS arm64 and Linux amd64.
- NixOS module evaluation, generated-config validation, and package tests on Linux.
- Offline inspection of official DXP2800 UGOS 1.17.0.0095; catalog checksum matched.
- Read-only sysfs hardware discovery on an Intel DXP2800, BIOS family
  `EM_DXP2800_SUB2_V1.0.27` and HT32F522 ACPI identity. The initial I801
  selection was corrected to the ACPI-linked DesignWare adapter in 0.2.1 below.
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

## Health-aware version 0.2.0

Validated on macOS arm64 and the original Intel DXP2800 running NixOS x86_64:

- Complete Go race suite and `go vet`; health parser fuzzing exercised more than
  798,000 generated inputs in the local 10-second run.
- `nix fmt` and `nix flake check` on macOS; Linux flake checks include both
  configuration parsers and evaluation of the hardened collector/LED services.
- Synthetic SMART reports cover healthy, overall failure, temperature thresholds,
  pending sectors, NVMe critical warnings, historical exit bits, command failure,
  actual smartctl power-mode object format, stale/future timestamps, missing
  expected devices and standby retaining a recent fault.
- Recorder tests preserve nonzero smartctl exit status and stdout/stderr, including
  when writing a snapshot fails. No additional SMART query is introduced.
- Synthetic Btrfs checks cover absent filesystems, missing members, error counters,
  and precedence of a known fault over an unreadable sibling. Renderer tests cover
  activity override, stale/missing snapshots and the critical night-brightness floor.

These tests do not establish physical LED behavior. Privileged MCU readback,
visible color/bay mapping, actual exporter recording under systemd, shutdown
restoration and reboot persistence remain acceptance steps on the target NAS.


## SUB2 discovery correction in 0.2.1

On an `EM_DXP2800_SUB2_V1.0.27` target, the ACPI-linked Intel DesignWare adapter
at vendor address 0x3a passed the required MCU ID check (0xc5b2) and returned
checksum-valid states for power, network, disk1 and disk2. I801/0x3a and the
DSDT-declared 0x74 endpoint did not work. See [firmware evidence](firmware.md#sub2-board-discovery).

Tests now cover bus renumbering with an unrelated I801 adapter present, exact
SUB2 BIOS/ACPI/PCI matching, MCU identity byte order/rejection, bounded checksum
rereads, immediate transport failure, and raw diagnostics preserving invalid
bytes without writes. Discovery and readback are validated; visible color/bay
mapping, actual write acknowledgements and lifecycle acceptance remain separate.

## Runtime acknowledgement correction in 0.2.2

The 0.2.1 deployment passed a dim-blue power LED write/readback check but later
stopped three times with zero ACK bytes: after approximately 24m42s, 8m17s and
2m34s of runtime. These failures invalidate any inference of long-term stability
from the earlier short samples. No automatic restart loop was enabled.

Regression tests cover settling after slow transfers, all five commands with
missing ACK but confirmed state, persistent mismatch, stale/nonconsecutive or
corrupt readback, mode/timing comparison, transport failures and unexpected ACKs,
cache invalidation and bounded diagnostics. The slow-transfer regression fails
against 0.2.1: one measured gap was 1.875 microseconds after a simulated 20 ms
transfer, rather than the intended 10 ms. The corrected implementation passes.
No hardware fault injection is used.

The corrected timing and readback fallback must still be tested on the affected
unit. Observe normal workload beyond the previous 25-minute failure window,
check missing-ACK diagnostics and kernel logs, and then conduct a longer soak.
A successful build or short run alone does not establish the underlying MCU
cause or long-term reliability. Physical bay mapping and reboot acceptance
remain separate checks.

## SUB2 read transaction correction in 0.2.3

On 2026-11-18, 0.2.2 still exited on invalid status checksums, including after
8m32s with at least 31 readback-confirmed missing ACKs. With the service stopped,
a read-only A/B/A comparison reproduced all-zero responses using combined SMBus
reads, eliminated them in the sampled split-read phase, and reproduced them
again when returning to SMBus. See [the measured counts](firmware.md#sub2-read-transaction-correction-023).

Linux regression tests cover the six allowed register selectors, exact response
lengths, one-byte selection with no setting payload, settling measured after a
slow selection, rejection of all other 250 selectors before I/O, and immediate
failure without retries on partial/failed transfers. Existing identity,
checksum, write-frame and bounded-recovery tests remain in place.

The split-read diagnostic completed 192 register reads with no invalid response;
this is not yet a long-duration daemon acceptance result. Observe the corrected
service beyond the prior failure windows, inspect both fatal and missing-ACK
logs, and verify physical colors and bay mapping separately. Do not weaken
validation or enable automatic restart loops to conceal recurring failures.
