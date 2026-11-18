package led

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Night struct {
	Start      string `json:"start"`
	End        string `json:"end"`
	Timezone   string `json:"timezone"`
	Brightness int    `json:"brightness"`
	loc        *time.Location
	start, end int
}
type Weekly struct {
	Timezone string  `json:"timezone"`
	Percent  [][]int `json:"percent"` // Sunday first, 24 values per day.
	loc      *time.Location
}

type Policies struct {
	Power   State `json:"power"`
	Network State `json:"network"`
	Disk1   State `json:"disk1"`
	Disk2   State `json:"disk2"`
}

func (p Policies) Array() [4]State { return [4]State{p.Power, p.Network, p.Disk1, p.Disk2} }

type Config struct {
	Health    *HealthConfig `json:"health,omitempty"`
	Weekly    *Weekly       `json:"weekly,omitempty"`
	PollMS    int           `json:"poll_ms"`
	HoldMS    int           `json:"hold_ms"`
	Bays      []string      `json:"bays"`
	Interface string        `json:"interface"`
	LEDs      Policies      `json:"leds"`
	Night     *Night        `json:"night,omitempty"`
}

func DefaultConfig() Config {
	on := State{Mode: "on", Brightness: 64, Color: [3]int{255, 255, 255}, OnMS: 100, OffMS: 100}
	active := on
	active.Mode = "activity"
	return Config{PollMS: 200, HoldMS: 400, Bays: []string{"ata1", "ata2"}, LEDs: Policies{on, active, active, active}}
}
func ParseConfig(r io.Reader) (Config, error) {
	c := DefaultConfig()
	raw, err := io.ReadAll(io.LimitReader(r, 65537))
	if err != nil {
		return c, err
	}
	if len(raw) > 65536 {
		return c, errors.New("configuration exceeds 64 KiB")
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return c, errors.New("configuration must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return c, errors.New("configuration must contain exactly one JSON object")
	}
	return c, c.Validate()
}
func clockMinute(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, errors.New("schedule time must be HH:MM")
	}
	h, e1 := strconv.Atoi(s[:2])
	m, e2 := strconv.Atoi(s[3:])
	if e1 != nil || e2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, errors.New("invalid schedule time")
	}
	return h*60 + m, nil
}
func (c *Config) Validate() error {
	if c.Health != nil {
		if e := c.Health.validate(); e != nil {
			return e
		}
	}
	if c.PollMS < 100 || c.PollMS > 5000 {
		return errors.New("poll_ms must be 100..5000")
	}
	if c.HoldMS < c.PollMS || c.HoldMS > 10000 {
		return errors.New("hold_ms must be >= poll_ms and <=10000")
	}
	if len(c.Bays) != 2 {
		return errors.New("exactly two bay ports are required")
	}
	for _, b := range c.Bays {
		if !strings.HasPrefix(b, "ata") {
			return errors.New("bays must be ATA port names, for example ata1")
		}
		n, e := strconv.Atoi(strings.TrimPrefix(b, "ata"))
		if e != nil || n < 1 || n > 32 || b != fmt.Sprintf("ata%d", n) {
			return errors.New("invalid ATA port")
		}
	}
	if c.Bays[0] == c.Bays[1] {
		return errors.New("bay ports must be distinct")
	}
	if strings.ContainsAny(c.Interface, "/\x00 \t\n") || c.Interface == "." || c.Interface == ".." || len(c.Interface) > 15 {
		return errors.New("invalid network interface")
	}
	for id, s := range c.LEDs.Array() {
		if s.Mode == "activity" {
			if id == 0 {
				return errors.New("power LED cannot use activity mode")
			}
			s.Mode = "blink"
		}
		if err := s.Validate(); err != nil {
			return fmt.Errorf("%s: %w", Names[id], err)
		}
	}
	if c.LEDs.Network.Mode == "activity" && c.Interface == "" {
		return errors.New("set interface explicitly for network activity, or set network mode to off")
	}
	if c.Weekly != nil {
		if c.Night != nil {
			return errors.New("choose night or weekly schedule, not both")
		}
		w := c.Weekly
		if w.Timezone == "" || w.Timezone == "Local" {
			return errors.New("weekly timezone must be explicit")
		}
		var err error
		w.loc, err = time.LoadLocation(w.Timezone)
		if err != nil {
			return err
		}
		if len(w.Percent) != 7 {
			return errors.New("weekly schedule requires seven days, Sunday first")
		}
		for _, day := range w.Percent {
			if len(day) != 24 {
				return errors.New("weekly schedule requires 24 hours per day")
			}
			for _, v := range day {
				if v < 0 || v > 100 {
					return errors.New("weekly percentages must be 0..100")
				}
			}
		}
	}
	if c.Night != nil {
		n := c.Night
		if n.Brightness < 0 || n.Brightness > 255 {
			return errors.New("night brightness must be 0..255")
		}
		var err error
		if n.start, err = clockMinute(n.Start); err != nil {
			return err
		}
		if n.end, err = clockMinute(n.End); err != nil {
			return err
		}
		if n.start == n.end {
			return errors.New("night start and end must differ")
		}
		if n.Timezone == "" || n.Timezone == "Local" {
			return errors.New("night timezone must be explicit, for example Etc/UTC")
		}
		if n.loc, err = time.LoadLocation(n.Timezone); err != nil {
			return err
		}
	}
	return nil
}
func (n *Night) Active(t time.Time) bool {
	if n == nil {
		return false
	}
	t = t.In(n.loc)
	m := t.Hour()*60 + t.Minute()
	if n.start < n.end {
		return m >= n.start && m < n.end
	}
	return m >= n.start || m < n.end
}

func (c Config) BrightnessCap(t time.Time) int {
	if c.Weekly != nil {
		w := c.Weekly
		t = t.In(w.loc)
		return w.Percent[int(t.Weekday())][t.Hour()] * 255 / 100
	}
	if c.Night.Active(t) {
		return c.Night.Brightness
	}
	return 255
}
