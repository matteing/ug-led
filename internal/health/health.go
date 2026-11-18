// Package health interprets observations; it never opens disk devices or I2C.
package health

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Observation struct {
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
	Reason     string    `json:"reason"`
}
type Snapshot struct {
	Version    int                    `json:"version"`
	Components map[string]Observation `json:"components"`
}

func Component(s string) bool { return s == "power" || s == "network" || s == "disk1" || s == "disk2" }
func rank(s string) int {
	switch s {
	case "healthy":
		return 0
	case "standby":
		return 1
	case "unknown":
		return 2
	case "warning":
		return 3
	case "critical":
		return 4
	}
	return -1
}
func (o Observation) FreshStatus(now time.Time, maxAge time.Duration) string {
	if o.ObservedAt.IsZero() || o.ObservedAt.After(now.Add(5*time.Second)) || now.Sub(o.ObservedAt) > maxAge || rank(o.Status) < 0 {
		return "unknown"
	}
	return o.Status
}
func decode(r io.Reader, v any, strict bool) error {
	b, e := io.ReadAll(io.LimitReader(r, 1048577))
	if e != nil {
		return e
	}
	if len(b) > 1048576 {
		return errors.New("JSON exceeds 1 MiB")
	}
	if len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return errors.New("expected JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if strict {
		d.DisallowUnknownFields()
	}
	if e = d.Decode(v); e != nil {
		return e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}
func readJSON(path string, v any, strict bool) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil {
		return e
	}
	if !s.Mode().IsRegular() {
		return errors.New("expected regular JSON file")
	}
	return decode(f, v, strict)
}
func ParseSnapshot(r io.Reader) (Snapshot, error) {
	var s Snapshot
	if e := decode(r, &s, true); e != nil {
		return Snapshot{}, e
	}
	if s.Version != 1 {
		return Snapshot{}, errors.New("unsupported health version")
	}
	for n, o := range s.Components {
		if !Component(n) || rank(o.Status) < 0 || o.ObservedAt.IsZero() || len(o.Reason) > 512 {
			return Snapshot{}, errors.New("invalid health observation")
		}
	}
	return s, nil
}

// AtomicJSON publishes a complete file on the same filesystem. Directory creation
// and permissions belong to the caller/systemd, not an arbitrary path in a report.
func AtomicJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".ug-led-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(append(b, '\n')); e != nil {
		return e
	}
	if e = f.Chmod(0644); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

type Reading struct {
	At       time.Time       `json:"at"`
	ExitCode int             `json:"exit_code"`
	Report   json.RawMessage `json:"report"`
}
type Record struct {
	Current    Reading  `json:"current"`
	LastHealth *Reading `json:"last_health,omitempty"`
}
type smartReport struct {
	SmartStatus *struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current *int `json:"current"`
	} `json:"temperature"`
	PowerMode struct {
		Name string `json:"name"`
	} `json:"power_mode"`
	ATA struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value uint64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMe *struct {
		CriticalWarning uint64 `json:"critical_warning"`
		MediaErrors     uint64 `json:"media_errors"`
		PercentageUsed  uint64 `json:"percentage_used"`
	} `json:"nvme_smart_health_information_log"`
}

func parseReport(r Reading) (smartReport, error) {
	var s smartReport
	e := decode(bytes.NewReader(r.Report), &s, false)
	return s, e
}
func hasHealth(s smartReport) bool { return s.SmartStatus != nil && s.SmartStatus.Passed != nil }
func SaveReading(dir, id string, r Reading) error {
	if !validID(id) {
		return errors.New("invalid device id")
	}
	path := filepath.Join(dir, id+".json")
	var old Record
	_ = readJSON(path, &old, true)
	next := Record{Current: r, LastHealth: old.LastHealth}
	if s, e := parseReport(r); e == nil && hasHealth(s) && r.ExitCode >= 0 && r.ExitCode&3 == 0 {
		copy := r
		next.LastHealth = &copy
	}
	return AtomicJSON(path, next)
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:+-]{0,239}$`)
var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validID(s string) bool { return identifier.MatchString(s) }

type Disk struct {
	ID        string `json:"id"`
	Component string `json:"component"`
	WarningC  int    `json:"warning_c"`
	CriticalC int    `json:"critical_c"`
}
type Pool struct {
	UUID    string `json:"uuid"`
	Devices int    `json:"devices"`
}
type Config struct {
	SmartDir      string `json:"smart_dir"`
	MaxAgeSeconds int    `json:"max_age_seconds"`
	Disks         []Disk `json:"disks"`
	Pools         []Pool `json:"btrfs"`
	Interface     string `json:"interface,omitempty"`
}

func ParseConfig(r io.Reader) (Config, error) {
	var c Config
	if e := decode(r, &c, true); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if !filepath.IsAbs(c.SmartDir) || c.MaxAgeSeconds < 30 || c.MaxAgeSeconds > 3600 {
		return errors.New("absolute smart_dir and max_age_seconds 30..3600 required")
	}
	ids := map[string]bool{}
	bays := map[string]bool{}
	for _, d := range c.Disks {
		if !validID(d.ID) || ids[d.ID] || !Component(d.Component) || d.Component == "network" || d.WarningC < 1 || d.CriticalC <= d.WarningC || d.CriticalC > 100 {
			return errors.New("invalid or duplicate disk configuration")
		}
		ids[d.ID] = true
		if d.Component != "power" {
			if bays[d.Component] {
				return errors.New("duplicate disk component")
			}
			bays[d.Component] = true
		}
	}
	pools := map[string]bool{}
	for _, p := range c.Pools {
		if !uuid.MatchString(p.UUID) || p.Devices < 1 || p.Devices > 64 || pools[p.UUID] {
			return errors.New("invalid or duplicate Btrfs pool")
		}
		pools[p.UUID] = true
	}
	if c.Interface != "" && (!validID(c.Interface) || len(c.Interface) > 15) {
		return errors.New("invalid interface")
	}
	if len(c.Disks)+len(c.Pools) == 0 {
		return errors.New("at least one health source required")
	}
	return nil
}
func obs(now time.Time, status, reason string) Observation { return Observation{status, now, reason} }
func worse(a, b Observation) Observation {
	if rank(b.Status) > rank(a.Status) {
		return b
	}
	return a
}
func assess(r Reading, d Disk, now time.Time, age time.Duration) Observation {
	unknown := obs(now, "unknown", "SMART reading unavailable or stale")
	if r.At.IsZero() || r.At.After(now.Add(5*time.Second)) || now.Sub(r.At) > age {
		return unknown
	}
	s, e := parseReport(r)
	if e != nil {
		return unknown
	}
	if (strings.EqualFold(s.PowerMode.Name, "standby") || strings.EqualFold(s.PowerMode.Name, "standby_y") || strings.EqualFold(s.PowerMode.Name, "sleep")) && r.ExitCode == 2 {
		return obs(now, "standby", "SMART confirmed low-power mode")
	}
	if r.ExitCode < 0 || r.ExitCode > 255 || r.ExitCode&3 != 0 {
		return unknown
	}
	o := obs(now, "healthy", "SMART checks passed")
	if !hasHealth(s) {
		o = unknown
	} else if !*s.SmartStatus.Passed {
		o = obs(now, "critical", "SMART overall health failed")
	}
	if r.ExitCode&24 != 0 {
		o = worse(o, obs(now, "critical", "SMART failure or current prefail threshold"))
	}
	if r.ExitCode&224 != 0 {
		o = worse(o, obs(now, "warning", "SMART historical error or self-test failure"))
	}
	if r.ExitCode&4 != 0 {
		o = worse(o, obs(now, "unknown", "SMART command or checksum error"))
	}
	if s.Temperature == nil || s.Temperature.Current == nil {
		o = worse(o, obs(now, "unknown", "temperature unavailable"))
	} else {
		t := *s.Temperature.Current
		if t < 0 || t > 150 {
			o = worse(o, unknown)
		} else if t >= d.CriticalC {
			o = worse(o, obs(now, "critical", "critical drive temperature"))
		} else if t >= d.WarningC {
			o = worse(o, obs(now, "warning", "high drive temperature"))
		}
	}
	for _, a := range s.ATA.Table {
		if a.Raw.Value > 0 && (a.ID == 5 || a.ID == 197 || a.ID == 198 || a.ID == 199) {
			o = worse(o, obs(now, "warning", fmt.Sprintf("ATA attribute %d is nonzero (may be historical)", a.ID)))
		}
	}
	if s.NVMe != nil {
		if s.NVMe.CriticalWarning != 0 {
			o = worse(o, obs(now, "critical", "NVMe critical warning"))
		}
		if s.NVMe.MediaErrors > 0 || s.NVMe.PercentageUsed >= 100 {
			o = worse(o, obs(now, "warning", "NVMe media errors or endurance estimate exhausted"))
		}
	}
	return o
}
func diskHealth(c Config, d Disk, now time.Time, devRoot string) Observation {
	if _, e := os.Stat(filepath.Join(devRoot, "disk/by-id", d.ID)); e != nil {
		if os.IsNotExist(e) {
			return obs(now, "critical", "expected disk missing")
		}
		return obs(now, "unknown", "cannot inspect expected disk")
	}
	var r Record
	if e := readJSON(filepath.Join(c.SmartDir, d.ID+".json"), &r, true); e != nil {
		return obs(now, "unknown", "SMART record unavailable")
	}
	age := time.Duration(c.MaxAgeSeconds) * time.Second
	o := assess(r.Current, d, now, age)
	if o.Status == "standby" && r.LastHealth == nil {
		return obs(now, "unknown", "standby observed without a recent health result")
	}
	if o.Status != "healthy" && r.LastHealth != nil {
		previous := assess(*r.LastHealth, d, now, age)
		// Standby never erases a recent fault. Once health ages out, show unknown
		// even when low-power mode was observed recently.
		o = worse(o, previous)
	}
	return o
}
func poolHealth(p Pool, now time.Time, sysfs string) Observation {
	root := filepath.Join(sysfs, "fs/btrfs", p.UUID, "devinfo")
	entries, e := os.ReadDir(root)
	if e != nil {
		if os.IsNotExist(e) {
			return obs(now, "critical", "expected Btrfs filesystem unavailable")
		}
		return obs(now, "unknown", "Btrfs status unreadable")
	}
	if len(entries) != p.Devices {
		return obs(now, "critical", "Btrfs device count differs from expected")
	}
	result := obs(now, "healthy", "Btrfs counters clear")
	for _, entry := range entries {
		result = worse(result, memberHealth(filepath.Join(root, entry.Name()), now))
	}
	return result
}
func memberHealth(base string, now time.Time) Observation {
	result := obs(now, "healthy", "Btrfs member counters clear")
	missing, e := os.ReadFile(filepath.Join(base, "missing"))
	if e != nil {
		result = obs(now, "unknown", "Btrfs missing-device status unreadable")
	} else {
		switch strings.TrimSpace(string(missing)) {
		case "1":
			return obs(now, "critical", "Btrfs member missing")
		case "0":
		default:
			result = obs(now, "unknown", "invalid Btrfs missing-device status")
		}
	}
	b, e := os.ReadFile(filepath.Join(base, "error_stats"))
	if e != nil {
		return worse(result, obs(now, "unknown", "Btrfs counters unreadable"))
	}
	found := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			result = worse(result, obs(now, "unknown", "invalid Btrfs counters"))
			continue
		}
		v, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil || found[f[0]] {
			result = worse(result, obs(now, "unknown", "invalid Btrfs counter"))
			continue
		}
		found[f[0]] = true
		if v > 0 {
			result = obs(now, "critical", "Btrfs error counters nonzero; investigate before clearing")
		}
	}
	for _, key := range []string{"write_errs", "read_errs", "flush_errs", "corruption_errs", "generation_errs"} {
		if !found[key] {
			result = worse(result, obs(now, "unknown", "incomplete Btrfs counters"))
		}
	}
	return result
}

func Collect(c Config, now time.Time, sysfs, devRoot string) Snapshot {
	s := Snapshot{Version: 1, Components: map[string]Observation{}}
	power := obs(now, "healthy", "configured storage checks passed")
	for _, d := range c.Disks {
		o := diskHealth(c, d, now, devRoot)
		if d.Component != "power" {
			s.Components[d.Component] = o
		}
		if o.Status == "standby" {
			o.Status = "healthy"
		}
		power = worse(power, o)
	}
	for _, p := range c.Pools {
		power = worse(power, poolHealth(p, now, sysfs))
	}
	s.Components["power"] = power
	if c.Interface != "" {
		o := obs(now, "unknown", "Ethernet carrier unreadable")
		b, e := os.ReadFile(filepath.Join(sysfs, "class/net", c.Interface, "carrier"))
		if e == nil {
			switch strings.TrimSpace(string(b)) {
			case "0":
				o = obs(now, "warning", "Ethernet link down")
			case "1":
				o = obs(now, "healthy", "Ethernet link up; Internet not probed")
			}
		}
		s.Components["network"] = o
	}
	return s
}
