package led

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/matteing/ug-led/internal/health"
)

// Health policy is opt-in. Sources must explicitly report sleep; inactivity alone
// is not evidence of standby. The file lives on tmpfs in the NixOS module.
type HealthConfig struct {
	File               string   `json:"file"`
	Preset             string   `json:"preset"`
	MaxAgeSeconds      int      `json:"max_age_seconds"`
	CriticalBrightness int      `json:"critical_brightness"`
	Monitored          []string `json:"monitored"`
}

func (h *HealthConfig) validate() error {
	if !filepath.IsAbs(h.File) {
		return errors.New("health.file must be absolute")
	}
	if h.Preset != "enhanced" && h.Preset != "ugos" {
		return errors.New("health.preset must be enhanced or ugos")
	}
	if h.MaxAgeSeconds < 10 || h.MaxAgeSeconds > 3600 {
		return errors.New("health.max_age_seconds must be 10..3600")
	}
	if h.CriticalBrightness < 0 || h.CriticalBrightness > 64 {
		return errors.New("health.critical_brightness must be 0..64")
	}
	seen := map[string]bool{}
	for _, n := range h.Monitored {
		if !health.Component(n) || seen[n] {
			return errors.New("health.monitored must contain distinct LED names")
		}
		seen[n] = true
	}
	if len(seen) == 0 {
		return errors.New("health.monitored must not be empty")
	}
	return nil
}

type healthReader struct {
	next     time.Time
	snapshot health.Snapshot
}

func (r *healthReader) apply(now time.Time, c Config, states *[4]State) {
	h := c.Health
	if h == nil {
		return
	}
	if !now.Before(r.next) {
		r.snapshot = health.Snapshot{}
		if f, err := os.Open(h.File); err == nil {
			r.snapshot, _ = health.ParseSnapshot(f)
			f.Close()
		}
		r.next = now.Add(time.Second)
	}
	cap := c.BrightnessCap(now)
	for _, n := range h.Monitored {
		var id int
		for i, name := range Names {
			if name == n {
				id = i
			}
		}
		o := r.snapshot.Components[n]
		status := o.FreshStatus(now, time.Duration(h.MaxAgeSeconds)*time.Second)
		if status == "healthy" {
			continue
		}
		s := State{Mode: "on", Brightness: 32, Color: RGB{160, 64, 255}, OnMS: 1000, OffMS: 1000}
		switch status {
		case "critical":
			s.Mode = "blink"
			s.Brightness = 64
			s.Color = RGB{255, 0, 0}
		case "warning":
			s.Brightness = 48
			s.Color = RGB{255, 128, 0}
		case "standby":
			s.Mode = "breath"
			s.Brightness = 24
			s.Color = RGB{64, 128, 255}
		}
		if h.Preset == "ugos" {
			if status == "critical" || status == "warning" {
				s.Mode = "blink"
				s.Color = RGB{255, 128, 0}
			}
			if status == "standby" {
				s.Color = RGB{255, 255, 255}
			}
		}
		s.Brightness = min(s.Brightness, cap)
		if status == "critical" {
			s.Brightness = max(s.Brightness, h.CriticalBrightness)
		}
		states[id] = s
	}
}
