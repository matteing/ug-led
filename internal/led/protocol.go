// Package led implements the original Intel DXP2800 front-panel protocol.
package led

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var Names = [4]string{"power", "network", "disk1", "disk2"}

type RGB [3]int

func (c *RGB) UnmarshalJSON(b []byte) error {
	var values []int
	if err := json.Unmarshal(b, &values); err != nil {
		return err
	}
	if len(values) != 3 {
		return errors.New("color requires exactly three RGB components")
	}
	for i, v := range values {
		if v < 0 || v > 255 {
			return errors.New("RGB components must be 0..255")
		}
		c[i] = v
	}
	return nil
}

type State struct {
	Mode       string `json:"mode"`
	Brightness int    `json:"brightness"`
	Color      RGB    `json:"color"`
	OnMS       int    `json:"on_ms,omitempty"`
	OffMS      int    `json:"off_ms,omitempty"`
}

func (s State) Validate() error {
	if s.Brightness < 0 || s.Brightness > 255 {
		return errors.New("brightness must be 0..255")
	}
	for _, v := range s.Color {
		if v < 0 || v > 255 {
			return errors.New("RGB components must be 0..255")
		}
	}
	switch s.Mode {
	case "off", "on":
	case "blink", "breath":
		// Intentionally narrower than the wire format; no zero-length or rapid cycles.
		if s.OnMS < 100 || s.OffMS < 100 || s.OnMS > 60000 || s.OffMS > 60000 || s.OnMS+s.OffMS > 65535 {
			return errors.New("blink/breath phases must be >=100ms and total <=65535ms")
		}
	default:
		return fmt.Errorf("unknown LED mode %q", s.Mode)
	}
	return nil
}

// Frame only admits the five verified LED commands, four LEDs, and validated parameters.
// The checksum excludes the LED id. No reset, flash, fan or raw register API is exposed.
func Frame(id byte, cmd byte, p [4]byte) ([12]byte, error) {
	var b [12]byte
	if id > 3 {
		return b, errors.New("LED id must be 0..3")
	}
	switch cmd {
	case 1:
		if p[1] != 0 || p[2] != 0 || p[3] != 0 {
			return b, errors.New("invalid brightness payload")
		}
	case 2:
		if p[3] != 0 {
			return b, errors.New("invalid RGB payload")
		}
	case 3:
		if p[0] > 1 || p[1] != 0 || p[2] != 0 || p[3] != 0 {
			return b, errors.New("invalid on/off payload")
		}
	case 4, 5:
		total, on := int(p[0])<<8|int(p[1]), int(p[2])<<8|int(p[3])
		if on < 100 || total-on < 100 || on > 60000 || total-on > 60000 {
			return b, errors.New("invalid timing payload")
		}
	default:
		return b, errors.New("command is not an allowed LED operation")
	}
	b = [12]byte{id, 0xa0, 1, 0, 0, cmd, p[0], p[1], p[2], p[3]}
	sum := 0
	for _, v := range b[1:10] {
		sum += int(v)
	}
	b[10], b[11] = byte(sum>>8), byte(sum)
	return b, nil
}

var errStatusChecksum = errors.New("invalid LED status checksum")

func DecodeStatus(b []byte) (State, error) {
	var s State
	if len(b) != 11 {
		return s, errors.New("LED status must contain 11 bytes")
	}
	sum := 0
	for _, v := range b[:9] {
		sum += int(v)
	}
	if sum == 0 || sum != int(b[9])<<8|int(b[10]) {
		return s, errStatusChecksum
	}
	if b[0] > 3 {
		return s, errors.New("unknown LED status mode")
	}
	s = State{Mode: []string{"off", "on", "blink", "breath"}[b[0]], Brightness: int(b[1]), Color: [3]int{int(b[2]), int(b[3]), int(b[4])}}
	if b[0] >= 2 {
		total, on := int(b[5])<<8|int(b[6]), int(b[7])<<8|int(b[8])
		if on == 0 || total <= on {
			return State{}, errors.New("invalid LED status timing")
		}
		s.OnMS, s.OffMS = on, total-on
	}
	return s, nil
}

type Bus interface {
	WriteFrame(byte, [12]byte) error
	ReadAck() (byte, error)
	ReadStatus(byte) ([]byte, error)
	Close() error
}

type Controller struct {
	bus    Bus
	states [4]State
	known  [4]bool
	next   time.Time
}

func NewController(b Bus) *Controller { return &Controller{bus: b} }
func sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Pace every bus transaction, including retries and status reads, to at most 100/s.
func (c *Controller) pace(ctx context.Context) error {
	if err := sleep(ctx, time.Until(c.next)); err != nil {
		return err
	}
	c.next = time.Now().Add(10 * time.Millisecond)
	return nil
}
func (c *Controller) Refresh(ctx context.Context) ([4]State, error) {
	var states [4]State
	for id := range states {
		for attempt := 0; attempt < 3; attempt++ {
			if err := c.pace(ctx); err != nil {
				return states, err
			}
			b, err := c.bus.ReadStatus(byte(id))
			if err != nil {
				return states, fmt.Errorf("%s status: %w", Names[id], err)
			}
			states[id], err = DecodeStatus(b)
			if err == nil {
				break
			}
			// Only a bad checksum gets a bounded reread. Never accept corrupt bytes,
			// retry transport errors, or normalize unsupported modes/timings.
			if !errors.Is(err, errStatusChecksum) || attempt == 2 {
				return states, fmt.Errorf("%s: %w", Names[id], err)
			}
		}
	}

	c.states = states
	c.known = [4]bool{true, true, true, true}
	return states, nil
}
func (c *Controller) command(ctx context.Context, id, cmd byte, p [4]byte) error {
	frame, err := Frame(id, cmd, p)
	if err != nil {
		return err
	}
	// Retry an explicit NACK only. Transport errors have unknown effects: stop immediately.
	for attempt := 0; attempt < 3; attempt++ {
		if err = c.pace(ctx); err != nil {
			return err
		}
		if err = c.bus.WriteFrame(id, frame); err != nil {
			return err
		}
		if err = c.pace(ctx); err != nil {
			return err
		}
		ack, e := c.bus.ReadAck()
		if e != nil {
			return e
		}
		if ack == 1 {
			return nil
		}
		if ack != 0 {
			return fmt.Errorf("unexpected controller acknowledgement %#x", ack)
		}
		if err = sleep(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("LED controller rejected command after three attempts")
}
func (c *Controller) Apply(ctx context.Context, id int, s State) error {
	if id < 0 || id >= len(Names) {
		return errors.New("invalid LED id")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	old, known := c.states[id], c.known[id]
	// Invalidate before a sequence so partial failure can never be cached as success.
	c.known[id] = false
	send := func(cmd byte, p [4]byte) error { return c.command(ctx, byte(id), cmd, p) }
	if !known || old.Brightness != s.Brightness {
		if err := send(1, [4]byte{byte(s.Brightness)}); err != nil {
			return err
		}
	}
	if !known || old.Color != s.Color {
		if err := send(2, [4]byte{byte(s.Color[0]), byte(s.Color[1]), byte(s.Color[2])}); err != nil {
			return err
		}
	}
	if !known || old.Mode != s.Mode || ((s.Mode == "blink" || s.Mode == "breath") && (old.OnMS != s.OnMS || old.OffMS != s.OffMS)) {
		cmd, p := byte(3), [4]byte{}
		switch s.Mode {
		case "on":
			p[0] = 1
		case "blink", "breath":
			cmd = 4
			if s.Mode == "breath" {
				cmd = 5
			}
			total := s.OnMS + s.OffMS
			p = [4]byte{byte(total >> 8), byte(total), byte(s.OnMS >> 8), byte(s.OnMS)}
		}
		if err := send(cmd, p); err != nil {
			return err
		}
	}
	c.states[id], c.known[id] = s, true
	return nil
}

// StatusReport preserves bytes on decode failure without treating them as a
// safe state for writes/restoration. Diagnose only reads the four known LEDs.
type StatusReport struct {
	LED   string `json:"led"`
	Raw   string `json:"raw"`
	State *State `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}

func (c *Controller) Diagnose(ctx context.Context) ([]StatusReport, error) {
	reports := make([]StatusReport, 0, 4)
	invalid := false
	for id, name := range Names {
		if e := c.pace(ctx); e != nil {
			return reports, e
		}
		b, e := c.bus.ReadStatus(byte(id))
		if e != nil {
			return reports, fmt.Errorf("%s status read: %w", name, e)
		}
		r := StatusReport{LED: name, Raw: fmt.Sprintf("% x", b)}
		s, e := DecodeStatus(b)
		if e != nil {
			r.Error = e.Error()
			invalid = true
		} else {
			r.State = &s
		}
		reports = append(reports, r)
	}
	if invalid {
		return reports, errors.New("one or more LED states failed validation; no writes performed")
	}
	return reports, nil
}
