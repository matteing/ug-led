package led

import (
	"strings"
	"testing"
	"time"
)

func TestConfigDefaultsAndColor(t *testing.T) {
	c, e := ParseConfig(strings.NewReader(`{"interface":"eth0","leds":{"disk1":{"color":[20,40,60]}}}`))
	if e != nil {
		t.Fatal(e)
	}
	if c.PollMS != 200 || c.LEDs.Disk1.Brightness != 64 || c.LEDs.Disk1.Color != [3]int{20, 40, 60} || c.LEDs.Disk1.Mode != "activity" {
		t.Fatal(c)
	}
}
func TestRejectConfig(t *testing.T) {
	for _, s := range []string{
		`{}`, `null`, `{"interface":"eth0","bays":["ata1","ata2","ata3"]}`, `{"interface":"eth0","leds":{"disk1":{"color":[1,2]}}}`, `{"interface":"eth0","leds":{"disk1":{"color":[1,2,3,4]}}}`, `{"interface":"eth0","poll_ms":1}`, `{"interface":"eth0","hold_ms":1}`, `{"interface":"../bad"}`, `{"interface":"eth0","typo":2}`, `{"interface":"eth0"}{}`, `{"interface":"eth0","bays":["ata1","ata1"]}`, `{"interface":"eth0","leds":{"disk1":{"brightness":256}}}`, `{"interface":"eth0","leds":{"disk1":{"color":[-1,0,0]}}}`, `{"interface":"eth0","leds":{"disk1":{"mode":"blink","on_ms":0}}}`, `{"interface":"eth0","leds":{"power":{"mode":"activity"}}}`, `{"interface":"eth0","night":{"start":"22:00","end":"22:00","timezone":"Etc/UTC"}}`,
	} {
		if _, e := ParseConfig(strings.NewReader(s)); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
func TestNightSchedule(t *testing.T) {
	c, e := ParseConfig(strings.NewReader(`{"interface":"eth0","night":{"start":"22:00","end":"07:00","timezone":"Etc/UTC","brightness":16}}`))
	if e != nil {
		t.Fatal(e)
	}
	for _, tt := range []struct {
		hour int
		want int
	}{{0, 16}, {6, 16}, {7, 255}, {21, 255}, {22, 16}} {
		got := c.BrightnessCap(time.Date(2026, 1, 1, tt.hour, 0, 0, 0, time.UTC))
		if got != tt.want {
			t.Fatal(tt, got)
		}
	}
}
func TestWeeklySchedule(t *testing.T) {
	c := DefaultConfig()
	c.Interface = "eth0"
	c.Weekly = &Weekly{Timezone: "Etc/UTC", Percent: make([][]int, 7)}
	for i := range c.Weekly.Percent {
		c.Weekly.Percent[i] = make([]int, 24)
		for h := range c.Weekly.Percent[i] {
			c.Weekly.Percent[i][h] = 100
		}
	}
	c.Weekly.Percent[0][12] = 25
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if got := c.BrightnessCap(time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)); got != 63 {
		t.Fatal(got)
	}
}
