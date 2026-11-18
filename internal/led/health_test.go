package led

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/matteing/ug-led/internal/health"
)

func TestHealthPriorityAndDimming(t *testing.T) {
	now := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	c := DefaultConfig()
	c.Interface = "eth0"
	c.Night = &Night{Start: "22:00", End: "07:00", Timezone: "Etc/UTC", Brightness: 0}
	c.Health = &HealthConfig{File: filepath.Join(t.TempDir(), "health.json"), Preset: "enhanced", MaxAgeSeconds: 90, CriticalBrightness: 24, Monitored: []string{"disk1", "disk2"}}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	s := health.Snapshot{Version: 1, Components: map[string]health.Observation{"disk1": {Status: "critical", ObservedAt: now}, "disk2": {Status: "warning", ObservedAt: now}}}
	if e := health.AtomicJSON(c.Health.File, s); e != nil {
		t.Fatal(e)
	}
	var r healthReader
	states := c.LEDs.Array()
	r.apply(now, c, &states)
	if states[2].Mode != "blink" || states[2].Color != (RGB{255, 0, 0}) || states[2].Brightness != 24 {
		t.Fatal(states[2])
	}
	if states[3].Mode != "on" || states[3].Brightness != 0 {
		t.Fatal(states[3])
	}
	c.Night = nil
	r.apply(now.Add(2*time.Minute), c, &states)
	if states[2].Color != (RGB{160, 64, 255}) {
		t.Fatal("stale failure must show unknown, not healthy", states[2])
	}
	c.Health.Preset = "ugos"
	r.next = now.Add(time.Hour)
	r.apply(now, c, &states)
	if states[2].Color != (RGB{255, 128, 0}) || states[2].Mode != "blink" {
		t.Fatal(states[2])
	}
}
func TestMissingHealthIsUnknown(t *testing.T) {
	c := DefaultConfig()
	c.Health = &HealthConfig{File: filepath.Join(t.TempDir(), "missing"), Preset: "enhanced", MaxAgeSeconds: 90, Monitored: []string{"power"}}
	var r healthReader
	s := c.LEDs.Array()
	r.apply(time.Now(), c, &s)
	if s[0].Color != (RGB{160, 64, 255}) {
		t.Fatal(s)
	}
	if s[1] != c.LEDs.Network {
		t.Fatal("unmonitored LED changed")
	}
}
