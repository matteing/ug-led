package health

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func reading(report string, code int) Reading { return Reading{now, code, json.RawMessage(report)} }

const good = `{"power_mode":{"name":"ACTIVE or IDLE","ata_value":255},"smart_status":{"passed":true},"temperature":{"current":40}}`

var disk = Disk{ID: "ata-test", Component: "disk1", WarningC: 50, CriticalC: 60}

func TestSMARTStates(t *testing.T) {
	cases := []struct {
		name, report string
		code         int
		want         string
	}{
		{"good", good, 0, "healthy"},
		{"failed", `{"smart_status":{"passed":false},"temperature":{"current":40}}`, 8, "critical"},
		{"hot", `{"smart_status":{"passed":true},"temperature":{"current":60}}`, 0, "critical"},
		{"warm", `{"smart_status":{"passed":true},"temperature":{"current":50}}`, 0, "warning"},
		{"pending", `{"smart_status":{"passed":true},"temperature":{"current":40},"ata_smart_attributes":{"table":[{"id":197,"raw":{"value":1}}]}}`, 0, "warning"},
		{"nvme", `{"smart_status":{"passed":true},"temperature":{"current":40},"nvme_smart_health_information_log":{"critical_warning":1}}`, 0, "critical"},
		{"missing temp", `{"smart_status":{"passed":true}}`, 0, "unknown"},
		{"no data", `{}`, 0, "unknown"},
		{"open failed", good, 2, "unknown"},
		{"command failed", good, 4, "unknown"},
		{"prefail", good, 16, "critical"},
		{"historical", good, 64, "warning"},
		{"standby", `{"power_mode":{"name":"STANDBY","ata_value":0}}`, 2, "standby"},
		{"sleep", `{"power_mode":{"name":"SLEEP","ata_value":-1}}`, 2, "standby"},
		{"not confirmed", `{}`, 2, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := assess(reading(c.report, c.code), disk, now, time.Minute); got.Status != c.want {
				t.Fatalf("%+v want %s", got, c.want)
			}
		})
	}
	for _, delta := range []time.Duration{-2 * time.Minute, time.Minute} {
		r := reading(good, 0)
		r.At = now.Add(delta)
		if assess(r, disk, now, time.Minute).Status != "unknown" {
			t.Fatal("stale/future result accepted")
		}
	}
}
func put(t *testing.T, path, contents string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(contents), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestCollectAndStandby(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if e := os.Mkdir(cache, 0755); e != nil {
		t.Fatal(e)
	}
	dev := filepath.Join(root, "dev")
	put(t, filepath.Join(dev, "disk/by-id", disk.ID), "")
	c := Config{SmartDir: cache, MaxAgeSeconds: 600, Disks: []Disk{disk}}
	save := func(r Reading) {
		t.Helper()
		if e := SaveReading(cache, disk.ID, r); e != nil {
			t.Fatal(e)
		}
	}
	status := func(at time.Time) string { return Collect(c, at, root, dev).Components["disk1"].Status }
	if status(now) != "unknown" {
		t.Fatal("missing data must be unknown")
	}
	save(reading(good, 0))
	if status(now) != "healthy" {
		t.Fatal("expected healthy")
	}
	save(reading(`{"power_mode":{"name":"STANDBY","ata_value":0}}`, 2))
	if status(now) != "standby" {
		t.Fatal("confirmed standby with recent healthy result")
	}
	if status(now.Add(11*time.Minute)) != "unknown" {
		t.Fatal("stale health must not appear asleep/healthy")
	}
	save(reading(`{"smart_status":{"passed":false},"temperature":{"current":40}}`, 8))
	save(reading(`{"power_mode":{"name":"STANDBY","ata_value":0}}`, 2))
	if status(now) != "critical" {
		t.Fatal("standby hid fault")
	}
	save(reading(good, 0))
	if status(now) != "healthy" {
		t.Fatal("fresh healthy result must clear old fault")
	}
	if e := os.Remove(filepath.Join(dev, "disk/by-id", disk.ID)); e != nil {
		t.Fatal(e)
	}
	s := Collect(c, now, root, dev)
	if s.Components["power"].Status != "critical" || s.Components["disk1"].Status != "critical" {
		t.Fatal(s)
	}
}
func TestBtrfs(t *testing.T) {
	root := t.TempDir()
	p := Pool{UUID: "11111111-2222-3333-4444-555555555555", Devices: 1}
	if poolHealth(p, now, root).Status != "critical" {
		t.Fatal("missing pool")
	}
	base := filepath.Join(root, "fs/btrfs", p.UUID, "devinfo/1")
	put(t, base+"/missing", "0\n")
	put(t, base+"/error_stats", "write_errs 0\nread_errs 0\nflush_errs 0\ncorruption_errs 0\ngeneration_errs 0\n")
	if poolHealth(p, now, root).Status != "healthy" {
		t.Fatal("clean pool")
	}
	put(t, base+"/error_stats", "write_errs 0\nread_errs 1\nflush_errs 0\ncorruption_errs 0\ngeneration_errs 0\n")
	if poolHealth(p, now, root).Status != "critical" {
		t.Fatal("error counter")
	}
	put(t, base+"/missing", "1")
	if poolHealth(p, now, root).Status != "critical" {
		t.Fatal("missing member")
	}
}
func TestSnapshotValidation(t *testing.T) {
	for _, s := range []string{`null`, `{}`, `{"version":2}`, `{"version":1,"typo":true}`, `{"version":1,"components":{"oops":{}}}`, `{"version":1} {}`} {
		if _, e := ParseSnapshot(strings.NewReader(s)); e == nil {
			t.Fatal(s)
		}
	}
	o := Observation{Status: "healthy", ObservedAt: now}
	if o.FreshStatus(now.Add(time.Minute), time.Second) != "unknown" {
		t.Fatal("stale snapshot")
	}
	if e := SaveReading(t.TempDir(), "../escape", reading(good, 0)); e == nil {
		t.Fatal("path traversal")
	}
}
func TestRecorderPreservesOutputAndExit(t *testing.T) {
	root := t.TempDir()
	fake := filepath.Join(root, "smartctl")
	put(t, fake, "#!/bin/sh\nprintf '%s' '{\"smart_status\":{\"passed\":false},\"temperature\":{\"current\":40}}'\nprintf '%s' 'diagnostic' >&2\nexit 8\n")
	if e := os.Chmod(fake, 0755); e != nil {
		t.Fatal(e)
	}
	var out, stderr bytes.Buffer
	if code := RecordSMART(fake, root, []string{"--json", "/dev/disk/by-id/ata-test"}, &out, &stderr); code != 8 {
		t.Fatal(code)
	}
	if !json.Valid(out.Bytes()) || stderr.String() != "diagnostic" {
		t.Fatal(out.String(), stderr.String())
	}
	var r Record
	if e := readJSON(filepath.Join(root, "ata-test.json"), &r, true); e != nil {
		t.Fatal(e)
	}
	if r.Current.ExitCode != 8 || r.LastHealth == nil {
		t.Fatal(r)
	}
	// Failure to save cannot alter the original monitoring command's result.
	if code := RecordSMART(fake, root+"/absent", []string{"/dev/disk/by-id/ata-test"}, &out, &stderr); code != 8 {
		t.Fatal(code)
	}
}
func FuzzSnapshot(f *testing.F) {
	f.Add(`{"version":1,"components":{}}`)
	f.Fuzz(func(t *testing.T, s string) { _, _ = ParseSnapshot(strings.NewReader(s)) })
}

func TestNoHealthHistoryAndPoolPriority(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if e := os.Mkdir(cache, 0755); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(root, "dev/disk/by-id", disk.ID), "")
	if e := SaveReading(cache, disk.ID, reading(`{"power_mode":{"name":"STANDBY","ata_value":0}}`, 2)); e != nil {
		t.Fatal(e)
	}
	c := Config{SmartDir: cache, MaxAgeSeconds: 600, Disks: []Disk{disk}}
	if got := Collect(c, now, root, filepath.Join(root, "dev")).Components["disk1"].Status; got != "unknown" {
		t.Fatal(got)
	}
	p := Pool{UUID: "11111111-2222-3333-4444-555555555555", Devices: 2}
	base := filepath.Join(root, "fs/btrfs", p.UUID, "devinfo")
	put(t, base+"/1/missing", "1")
	put(t, base+"/2/missing", "garbage")
	if got := poolHealth(p, now, root).Status; got != "critical" {
		t.Fatal("unreadable member hid known fault", got)
	}
}
