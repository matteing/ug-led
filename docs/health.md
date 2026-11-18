# Health-aware LEDs

Health monitoring is opt-in. `run` still only reads sysfs and a bounded JSON
snapshot; it never runs SMART commands or makes network requests. A separate Go
`collect` process publishes a snapshot every 30 seconds under NixOS. The renderer
loads it once per second and checks observation freshness on every activity tick.

## Colors and priorities

| State | Enhanced preset | UGOS-inspired preset |
| --- | --- | --- |
| Healthy | Configured idle/activity color (default white) | Same |
| Confirmed standby with recent healthy evidence | Blue breathing | White breathing |
| Unknown, missing or stale health data | Purple steady | Purple steady |
| Warning | Amber steady | Amber 1-second on/off blink |
| Critical | Red 1-second on/off blink | Amber 1-second on/off blink |

Priority is critical, warning, unknown, confirmed standby, activity, idle. Health
overrides the configured LED mode, including `off`; remove a LED from `monitored`
to opt it out. Normal brightness follows the schedule. Critical faults have a
configurable minimum brightness (0–64, example 24), even during a zero-brightness
night window. Set the minimum to zero to respect complete darkness.

The `ugos` preset follows the factory white/amber convention for the documented
activity, standby and fault states. Purple for unknown data and an optional
critical brightness floor are our extensions. This is not a reproduction of
UGOS's entire private health engine or its thresholds. The factory convention is
documented in [UGREEN's LED guide](https://support.ugnas.com/detail/article/en-US/499);
[firmware research](firmware.md) describes the original DXP2800 implementation.

A healthy LED means the configured checks passed. It is not a prediction that a
disk cannot fail. No sleep state is inferred from low I/O. To identify a bay,
stop the daemon and use the existing `set --led disk1 --mode blink --color 4080ff`
command; normal service restart restores automatic policy.

## Reuse existing SMART checks

Some SMART exporters retain the last successful report after subsequent queries
fail. A fresh Prometheus scrape timestamp therefore does not prove fresh disk
health. See the upstream exporter's [cache implementation](https://github.com/prometheus-community/smartctl_exporter/blob/master/readjson.go).

`record-smart` wraps the monitor's **existing** smartctl invocation, forwards its
arguments/stdout/stderr/exit status, and atomically records the actual result and
time. It does not add disk queries. It records only commands whose last argument
is an explicit `/dev/disk/by-id/ID` path; scans/version checks are passed through.
Set the monitor's smartctl executable to a script like:

```sh
#!/bin/sh
exec /path/to/ug-led record-smart \
  --smartctl /path/to/real/smartctl --cache-dir /run/ug-led-smart -- "$@"
```

The directory must already exist and be writable by the monitor. A recording
failure is logged to stderr and does not change smartctl's exit status. Reports
are limited to 1 MiB and published by atomic rename. The reader needs directory
traversal and file-read access; reports include disk identity information. Keep
this runtime directory outside your checkout and never publish captured reports.

Configure the existing monitor with `--nocheck=standby` (smartctl_exporter:
`--smartctl.powermode-check=standby`) to avoid waking standby disks. This does not
prevent other software from waking them. A standby observation preserves the
previous health report: recent faults take priority, and an aged-out or absent
health report becomes unknown. A disk sleeping longer than the configured health
age limit will therefore eventually show purple rather than claim fresh health.

Start with [the collector example](../examples/health-collector.json), replace
IDs with your inventory and map them to verified physical bays. All configured
disks are expected: a missing stable device path is critical. Add Btrfs entries
such as `{"uuid":"11111111-2222-3333-4444-555555555555","devices":2}` only for
filesystems expected to be mounted whenever the collector runs.

```sh
ug-led collect --config collector.json --check
ug-led collect --config collector.json                 # inspect, stdout only
ug-led collect --config collector.json --output /run/ug-led/health.json
```

The collector reads cached SMART JSON, stats expected device paths without
opening them, reads Btrfs sysfs counters, and optionally reads Ethernet carrier.
It has no disk/I2C capabilities, subprocesses or network access. An upstream
capture failure, malformed data, incomplete required fields, timestamp more than
five seconds in the future, or expired health becomes unknown. Sources are
re-evaluated on every collection; even a continuing critical condition requires
fresh observations. If collection stops, the renderer shows unknown after the
snapshot age limit. It never converts a stale fault to healthy.

Rules (project policy, not claimed UGOS thresholds):

- SMART overall failure or a current prefail threshold breach: critical.
- Configured warning/critical temperature thresholds: amber/red. Missing
  temperature or overall-health fields: unknown.
- ATA raw attributes 5, 197, 198 or 199 nonzero: warning. Counts may be historical;
  a CRC count alone does not establish a failed disk.
- SMART historical threshold/error/self-test exit bits: warning. SMART command
  or checksum failure: unknown unless a stronger fault is present.
- NVMe critical-warning bits: critical. Media errors or percentage-used at least
  100: warning (an endurance estimate, not a guarantee of failure).
- Expected Btrfs filesystem/member absent, unexpected device count, or nonzero
  error counters: power LED critical. Counters are cumulative; investigate before
  manually clearing them. The collector never clears counters or repairs pools.
- System-disk and pool problems appear on power; bay-specific SMART problems
  also appear on that bay. Power summarizes configured storage health, not every
  possible system fault, CPU/fan state, scrub progress or service outage.
- Ethernet carrier down: network amber; up: ordinary white activity. This does
  **not** claim Internet reachability or probe external hosts.

## NixOS setup

Alongside the module's ordinary `interface`, `bays`, and schedule settings:

```nix
services.ug-led = {
  enable = true;
  settings.health = {
    file = "/run/ug-led/health.json";
    preset = "enhanced"; # or "ugos"
    max_age_seconds = 90;
    critical_brightness = 24;
    monitored = [ "power" "network" "disk1" "disk2" ];
  };
  collector = {
    enable = true;
    settings = builtins.fromJSON (builtins.readFile ./collector.json);
  };
};
```

The collector runs as a separate dynamic user with device access denied and
publishes `/run/ug-led/health.json`. The NixOS module does not automatically
modify another service's SMART invocation. For the NixOS smartctl exporter, add
`--smartctl.path=WRAPPER` to its `extraFlags`, provision a writable runtime
directory in its service, and ensure both the directory and recorded files are
readable by the collector. Keep the exporter on loopback. The wrapper needs no
permissions beyond those already used by smartctl plus its own runtime directory.

## External health providers

Other monitoring systems can atomically publish this versioned schema instead of
using the bundled collector. Only `monitored` names are rendered; a missing name
is unknown. Status values are `healthy`, `standby`, `unknown`, `warning`, and
`critical`. Each observation has its own RFC3339 timestamp and optional reason
(up to 512 bytes). Use the source's actual observation time, not a fake refresh.

```json
{
  "version": 1,
  "components": {
    "disk1": {
      "status": "warning",
      "observed_at": "2026-01-01T12:00:00Z",
      "reason": "pending sectors detected"
    }
  }
}
```

This interface can carry explicit Internet-probe results or other system alerts
without putting subprocesses/network polling in the LED loop. The entire snapshot
must be valid: unknown fields, invalid statuses or malformed observations make
the monitored LEDs unknown, never silently healthy.
