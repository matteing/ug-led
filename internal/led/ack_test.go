package led

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type ackTestBus struct {
	*fakeBus
	apply    bool
	ackErr   error
	readErr  error
	replies  [][]byte
	ackReads int
}

func (b *ackTestBus) WriteFrame(id byte, f [12]byte) error {
	if err := b.fakeBus.WriteFrame(id, f); err != nil {
		return err
	}
	if b.apply {
		s := &b.states[id]
		switch f[5] {
		case 1:
			s.Brightness = int(f[6])
		case 2:
			s.Color = RGB{int(f[6]), int(f[7]), int(f[8])}
		case 3:
			s.Mode = "off"
			if f[6] == 1 {
				s.Mode = "on"
			}
		case 4, 5:
			s.Mode = "blink"
			if f[5] == 5 {
				s.Mode = "breath"
			}
			s.OnMS = int(f[8])<<8 | int(f[9])
			s.OffMS = (int(f[6])<<8 | int(f[7])) - s.OnMS
		}
	}
	return nil
}
func (b *ackTestBus) ReadAck() (byte, error) { b.ackReads++; return b.ack, b.ackErr }
func (b *ackTestBus) ReadStatus(id byte) ([]byte, error) {
	b.reads++
	if b.readErr != nil {
		return nil, b.readErr
	}
	if len(b.replies) > 0 {
		r := b.replies[0]
		b.replies = b.replies[1:]
		return r, nil
	}
	return statusBytes(b.states[id]), nil
}
func zeroACKBus() *ackTestBus {
	b := &ackTestBus{fakeBus: newFake(), apply: true}
	b.ack = 0
	return b
}
func knownController(b *ackTestBus) *Controller {
	c := NewController(b)
	c.states = b.states
	c.known = [4]bool{true, true, true, true}
	return c
}
func TestZeroACKRequiresReadbackForEveryCommand(t *testing.T) {
	for _, cmd := range []byte{1, 2, 3, 4, 5} {
		t.Run(string(rune('0'+cmd)), func(t *testing.T) {
			b := zeroACKBus()
			c := knownController(b)
			s := b.states[2]
			switch cmd {
			case 1:
				s.Brightness = 32
			case 2:
				s.Color = RGB{64, 128, 255}
			case 3:
				s.Mode = "off"
			case 4, 5:
				s.Mode = "blink"
				if cmd == 5 {
					s.Mode = "breath"
				}
				s.OnMS = 100
				s.OffMS = 200
			}
			var log bytes.Buffer
			c.SetDiagnosticWriter(&log)
			if err := c.Apply(context.Background(), 2, s); err != nil {
				t.Fatal(err)
			}
			if len(b.writes) != 1 || b.writes[0][5] != cmd || b.reads != 2 || !c.known[2] || c.states[2] != s {
				t.Fatalf("incorrect confirmation: writes=%v reads=%d cached=%+v", b.writes, b.reads, c.states[2])
			}
			if !strings.Contains(log.String(), "two status reads confirmed") {
				t.Fatal(log.String())
			}
		})
	}
}
func TestZeroACKPersistentMismatchStopsAndInvalidatesCache(t *testing.T) {
	b := zeroACKBus()
	b.apply = false
	c := knownController(b)
	s := b.states[1]
	s.Color = RGB{255, 0, 0}
	err := c.Apply(context.Background(), 1, s)
	if err == nil || !strings.Contains(err.Error(), "command=0x02") || !strings.Contains(err.Error(), "last readback=") || len(b.writes) != 3 || b.reads != 9 || c.known[1] {
		t.Fatalf("err=%v writes=%d reads=%d known=%v", err, len(b.writes), b.reads, c.known[1])
	}
}
func TestZeroACKDoesNotAcceptOneMatchingOrCorruptResponse(t *testing.T) {
	s := newFake().states[0]
	desired := s
	desired.Brightness = 32
	corrupt := statusBytes(desired)
	corrupt[10] ^= 1
	for _, tc := range []struct {
		name    string
		replies [][]byte
		want    bool
		wantErr bool
	}{
		{"single-stale-match", [][]byte{statusBytes(desired), statusBytes(s), statusBytes(s)}, false, false},
		{"nonconsecutive", [][]byte{statusBytes(desired), statusBytes(s), statusBytes(desired)}, false, false},
		{"settles", [][]byte{statusBytes(s), statusBytes(desired), statusBytes(desired)}, true, false},
		{"corrupt-then-valid", [][]byte{corrupt, statusBytes(desired), statusBytes(desired)}, true, false},
		{"all-corrupt", [][]byte{corrupt, corrupt, corrupt}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := zeroACKBus()
			b.replies = tc.replies
			got, _, err := NewController(b).confirmCommand(context.Background(), 0, 1, [4]byte{32})
			if got != tc.want || (err != nil) != tc.wantErr || b.reads != 3 || len(b.writes) != 0 {
				t.Fatal(got, err, b.reads, b.writes)
			}
		})
	}
}
func TestZeroACKTransportAndUnexpectedACKNeverRetry(t *testing.T) {
	sentinel := errors.New("transport failed")
	for _, stage := range []string{"write", "ack", "status", "unexpected"} {
		t.Run(stage, func(t *testing.T) {
			b := zeroACKBus()
			switch stage {
			case "write":
				b.fail = sentinel
			case "ack":
				b.ackErr = sentinel
			case "status":
				b.readErr = sentinel
			case "unexpected":
				b.ack = 2
			}
			c := knownController(b)
			s := b.states[0]
			s.Brightness = 32
			err := c.Apply(context.Background(), 0, s)
			if err == nil || len(b.writes) != 1 || c.known[0] {
				t.Fatal(err, len(b.writes), c.known[0])
			}
			if stage != "unexpected" && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if stage == "unexpected" && b.reads != 0 {
				t.Fatal("unexpected ACK must not use fallback")
			}
		})
	}
}
func TestCommandReadbackIncludesModeAndBothTimings(t *testing.T) {
	p := [4]byte{0, 200, 0, 100}
	for _, s := range []State{
		{Mode: "on", OnMS: 100, OffMS: 100},
		{Mode: "breath", OnMS: 100, OffMS: 100},
		{Mode: "blink", OnMS: 100, OffMS: 200},
		{Mode: "blink", OnMS: 200, OffMS: 100},
	} {
		if commandMatches(s, 4, p) {
			t.Fatalf("wrong mode/timing accepted: %+v", s)
		}
	}
}
func TestZeroACKDiagnosticsAreRateLimited(t *testing.T) {
	b := zeroACKBus()
	c := knownController(b)
	var log bytes.Buffer
	c.SetDiagnosticWriter(&log)
	for _, brightness := range []int{32, 16, 8} {
		s := b.states[0]
		s.Brightness = brightness
		if err := c.Apply(context.Background(), 0, s); err != nil {
			t.Fatal(err)
		}
	}
	if c.verifiedWithoutACK != 3 || strings.Count(log.String(), "readback recoveries=") != 1 {
		t.Fatal(log.String(), c.verifiedWithoutACK)
	}
	c.nextDiagnostic = time.Time{}
	s := b.states[0]
	s.Brightness = 32
	if err := c.Apply(context.Background(), 0, s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "recoveries=4") {
		t.Fatal(log.String())
	}
}

type slowBus struct {
	*fakeBus
	completed time.Time
	gaps      []time.Duration
}

func (b *slowBus) transfer() {
	if !b.completed.IsZero() {
		b.gaps = append(b.gaps, time.Since(b.completed))
	}
	time.Sleep(20 * time.Millisecond) // More than the entire pacing interval.
	b.completed = time.Now()
}
func (b *slowBus) WriteFrame(id byte, f [12]byte) error {
	b.transfer()
	return b.fakeBus.WriteFrame(id, f)
}
func (b *slowBus) ReadAck() (byte, error)             { b.transfer(); return b.fakeBus.ReadAck() }
func (b *slowBus) ReadStatus(id byte) ([]byte, error) { b.transfer(); return b.fakeBus.ReadStatus(id) }
func TestSettlingStartsAfterSlowTransferCompletes(t *testing.T) {
	b := &slowBus{fakeBus: newFake()}
	c := NewController(b)
	ctx := context.Background()
	if _, err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	s := b.states[0]
	s.Color = RGB{255, 0, 0}
	s.Brightness = 32
	if err := c.Apply(ctx, 0, s); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Diagnose(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.gaps) != 11 {
		t.Fatal(len(b.gaps))
	}
	for _, gap := range b.gaps {
		if gap < 9*time.Millisecond {
			t.Fatalf("only %s settling after slow transfer", gap)
		}
	}
}
