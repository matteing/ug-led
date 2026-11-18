# ug-led

A small Go utility for the **original Intel UGREEN DXP2800** front-panel LEDs.
Independent RGB color, brightness, on/off, blink and breathing control, plus a
native NixOS service for disk/network activity, health colors and scheduled dimming.

Uses Linux's stock I801/DesignWare drivers and `i2c-dev` interface. No external kernel module,
CGO, Go dependencies, Docker or network listener. The LED daemon issues no SMART
queries or disk-device reads; optional health collection reuses existing monitoring.

**Experimental:** software tests, firmware analysis, hardware identification and
unprivileged activity sampling have been checked. SUB2 MCU identity and all four
LED states have been read successfully. Visible colors, physical bay order,
and restart/reboot behavior still need hardware acceptance. Do not interpret successful compilation as hardware certification.
Only Linux amd64 can access the controller; macOS builds support development and
dry runs. GT, V1.2 and other models are not claimed supported.

## Build

```sh
nix build github:matteing/ug-led
./result/bin/ug-led version
```

Or with Go 1.23 or newer:

```sh
go build ./cmd/ug-led
```

## First checks

On the NAS, with the standard I²C modules loaded:

```sh
ug-led inspect                       # sysfs only; no controller access
ug-led check --config config.json    # validate and show effective settings
ug-led run --config config.json --dry-run --duration 30s
sudo ug-led status                   # controller reads only, no LED-state writes
sudo ug-led status --raw             # raw bytes plus per-LED decode diagnostics
```

`inspect` requires the `DXP2800` DMI identity, `EM_DXP2800_` system-version and BIOS families,
HT32F522 ACPI device and a recognized hardware profile. Legacy EM boards use
the Intel SMBus adapter. The exact `EM_DXP2800_SUB2_V1.0.27` profile follows the
MCU's ACPI parent to its Intel DesignWare adapter and requires chip ID `0xc5b2`.
Both profiles use vendor address `0x3a`; other SUB2 revisions are rejected.
See [the ACPI/address discrepancy](docs/firmware.md#sub2-board-discovery).
`status` additionally
checks SMBus capabilities and the checksum/format of all four LED responses.
These checks reduce the chance of using the wrong device; they cannot establish
electrical compatibility for every hardware revision.

## Color and manual control

Stop the daemon before issuing manual hardware commands. Both commands lock the
adapter against other ug-led instances. Other software must not control the same
MCU concurrently.

```sh
# Preview without requiring hardware or root.
ug-led set --led disk1 --mode on --color 00ff80 --brightness 32 --dry-run

# Apply after the read-only checks pass.
sudo ug-led set --led disk1 --mode on --color 00ff80 --brightness 32
sudo ug-led set --led power --mode breath --color 80aaff --brightness 32 \
  --on-ms 1000 --off-ms 1000
sudo ug-led set --led all --mode off
```

LED names: `power`, `network`, `disk1`, `disk2`, or `all` for manual control.
Manual `set` specifies a complete state: omitted color is white, brightness is
64, mode is on, and blink/breath phases are 100 ms. It is **not** a partial patch
of the previous state. Successful commands are followed by status readback.
`status` returns four entries in the LED order above.

RGB channels and brightness each range from 0 to 255. Brightness scales the
selected color. The initial default is 64/255, not full output. Blink/breath
phases must each be at least 100 ms and their sum at most 65535 ms. The MCU
handles the animation; ug-led does not toggle the LED every animation frame.

## Activity and scheduling

Start from [examples/dxp2800.json](examples/dxp2800.json), verify your ATA bay
mapping, and set the actual Ethernet interface and desired timezone.

```json
{
  "interface": "enp2s0",
  "bays": ["ata1", "ata2"],
  "poll_ms": 200,
  "hold_ms": 400,
  "leds": {
    "power": {"mode": "on", "color": [128, 170, 255], "brightness": 32},
    "disk1": {"mode": "activity", "color": [0, 255, 128], "brightness": 64},
    "disk2": {"mode": "activity", "color": [255, 160, 0], "brightness": 64}
  },
  "night": {
    "start": "22:00", "end": "07:00",
    "timezone": "Etc/UTC", "brightness": 16
  }
}
```

Defaults merge into omitted fields. Unknown fields, invalid colors, unsafe timing,
and invalid schedules are rejected before hardware access. Each LED supports
`off`, `on`, `blink`, or `breath`. Network and disks also support `activity`:
steady when present/link-up, blinking during activity, off when absent/unreadable
or link-down. The configured color applies to both steady and activity states.
Power defaults to on; the other three LEDs default to activity; all default to
white at brightness 64. Set network mode to `off` if no interface should be read.

- Disk activity uses cumulative kernel counters and in-flight I/O. A completed
  burst between polls is still detected; counter resets reseed the baseline.
- Bays are assigned by ATA port topology, never by assumptions about `sda`/`sdb`.
  Discovery runs every five seconds. Verify the physical mapping on your unit.
- Network activity uses carrier and byte counters. It does not claim Internet
  connectivity or issue pings.
- Optional [health-aware colors](docs/health.md) consume fresh SMART observations
  and Btrfs status. Idle alone never proves standby or disk health. Without health
  configuration, these remain activity indicators only.
- Polling defaults to 200 ms (range 100–5000); the activity hold defaults to 400 ms
  (must be at least the polling interval and at most 10000 ms).
- A night window is start-inclusive, end-exclusive, and may cross midnight. Its
  brightness is a ceiling on each LED's configured brightness; zero dims to dark.
  Timezone must be explicit. DST follows that timezone's local wall clock.

For UGOS-style weekly scheduling, replace `night` with `weekly`:

```nix
# NixOS settings example: seven days, Sunday first, 24 hourly percentages each.
weekly = {
  timezone = "Etc/UTC";
  percent = builtins.genList (_day:
    builtins.genList (hour: if hour >= 22 || hour < 7 then 5 else 100) 24
  ) 7;
};
```

In JSON, `percent` is an array of seven arrays, each containing 24 integers from
0 to 100. Percentages map to `floor(percent * 255 / 100)` and cap each LED's
brightness. Set a LED's configured brightness to 255 if the schedule should use
the full range. `night` and `weekly` are mutually exclusive. Configuration is
read at startup; restart the service after changing it.

## Health colors

Enable the enhanced or UGOS-inspired preset to prioritize SMART failures,
temperature warnings, missing expected disks and Btrfs errors over activity.
Unknown/stale data is purple; enhanced warnings are amber and critical faults
blink red. See [setup, state rules and data freshness](docs/health.md).

## NixOS

Add the input and import the module:

```nix
inputs.ug-led.url = "github:matteing/ug-led";
# In the host's modules:
imports = [ inputs.ug-led.nixosModules.default ];
services.ug-led = {
  enable = true;
  settings = {
    interface = "enp2s0";
    bays = [ "ata1" "ata2" ];
    leds.disk1.color = [ 0 255 128 ];
    leds.disk2.color = [ 255 160 0 ];
    night = {
      start = "22:00";
      end = "07:00";
      timezone = "Etc/UTC";
      brightness = 16;
    };
  };
};
```

The module builds the binary, loads stock modules, generates
`/etc/ug-led/config.json`, validates it before start, and supplies a hardened
systemd service. Root is used to open the root-owned I²C node; capabilities are
dropped and the filesystem is read-only. Device access is restricted to I²C
character devices, with the application selecting only the verified Intel
I801 or ACPI-linked DesignWare adapter and address `0x3a`. This is not a kernel-enforced per-address sandbox.

Version 0.2.2 waits 10 ms after each bus transfer completes. A zero command
acknowledgement triggers bounded status reads: two consecutive valid responses
must confirm the requested setting before continuing without a duplicate write.
Confirmed missing acknowledgements are logged at most every 30 seconds with a
cumulative count. Unconfirmed commands still stop after at most three writes;
transport errors and unexpected responses stop immediately. See the
[runtime correction and remaining acceptance](docs/validation.md#runtime-acknowledgement-correction-in-022).

On controller failure the service stops and requires an operator restart;
it does not enter an automatic hardware retry loop. A normal SIGTERM attempts
to restore the initial validated LED states within three seconds. A kill,
power loss, or bus failure cannot guarantee restoration; LEDs may retain their
last MCU state until reconfigured. The utility does not reset or flash the MCU.

```sh
systemctl status ug-led
journalctl -u ug-led -b
sudo systemctl stop ug-led
```

## Safety and performance

The code exposes only the five documented LED commands and IDs 0–3. It never
writes arbitrary registers, scans the bus, uses `I2C_SLAVE_FORCE`, rebinds drivers,
or touches fan control, voltages, firmware, disk devices or EEPROM programming.
Brightness is constrained to the same byte range accepted by the inspected UGOS
driver. There is no software evidence that normal LED commands alter electrical
current limits, but this is reverse-engineered software, not a guarantee against
hardware damage. See [the firmware findings](docs/firmware.md).

One loop samples two disks and one NIC five times per second by default. Writes
occur only on state changes. Read-only reconciliation every 30 seconds detects
controller state drift. Checksum failures get at most three paced reads; corrupt
data is never accepted. Transport errors and unsupported states stop immediately.
Every bus transaction is spaced at least 10 ms apart;
negative acknowledgements get at most three attempts with an additional 50 ms
backoff. Transport errors abort immediately. Continuous disk activity needs one
blink command at the beginning and one steady command at the end, assuming
unchanged color/brightness and no controller reset.

See [validation and measured performance](docs/validation.md) for the exact
conditions and remaining hardware checks.

## Development

```sh
nix develop
go test -race ./...
go vet ./...
go test ./internal/led -run '^$' -fuzz FuzzDecodeStatus -fuzztime 10s
go test ./internal/led -run '^$' -bench . -benchmem
nix fmt
nix flake check
```

Tests cover known protocol vectors, checksum rejection, command bounds, retries,
cache behavior and reconciliation, hardware guards, RGB configuration, schedules,
activity bursts/counter resets, disk removal/reappearance and CLI dry runs.

MIT licensed. Protocol research credit: [miskcoo/ugreen_leds_controller](https://github.com/miskcoo/ugreen_leds_controller).
See [NOTICE](NOTICE) for attribution and source boundaries.
