package led

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFrames(t *testing.T) {
	tests := []struct {
		id, cmd byte
		p       [4]byte
		want    [12]byte
	}{
		{2, 1, [4]byte{64}, [12]byte{2, 0xa0, 1, 0, 0, 1, 64, 0, 0, 0, 0, 0xe2}},
		{3, 2, [4]byte{255, 255, 255}, [12]byte{3, 0xa0, 1, 0, 0, 2, 255, 255, 255, 0, 3, 0xa0}},
		{0, 3, [4]byte{1}, [12]byte{0, 0xa0, 1, 0, 0, 3, 1, 0, 0, 0, 0, 0xa5}},
		{1, 4, [4]byte{0, 200, 0, 100}, [12]byte{1, 0xa0, 1, 0, 0, 4, 0, 200, 0, 100, 1, 0xd1}},
		{1, 5, [4]byte{7, 208, 3, 232}, [12]byte{1, 0xa0, 1, 0, 0, 5, 7, 208, 3, 232, 2, 0x68}},
	}
	for _, tt := range tests {
		got, err := Frame(tt.id, tt.cmd, tt.p)
		if err != nil || got != tt.want {
			t.Fatalf("cmd %d: %x %v, want %x", tt.cmd, got, err, tt.want)
		}
	}
	for _, tt := range []struct {
		id, cmd byte
		p       [4]byte
	}{{4, 1, [4]byte{}}, {0, 0xff, [4]byte{}}, {0, 3, [4]byte{2}}, {0, 4, [4]byte{0, 100, 0, 100}}, {0, 2, [4]byte{1, 2, 3, 4}}} {
		if _, err := Frame(tt.id, tt.cmd, tt.p); err == nil {
			t.Fatal("unsafe command accepted", tt)
		}
	}
}
func statusBytes(s State) []byte {
	mode := byte(0)
	switch s.Mode {
	case "on":
		mode = 1
	case "blink":
		mode = 2
	case "breath":
		mode = 3
	}
	total := s.OnMS + s.OffMS
	b := []byte{mode, byte(s.Brightness), byte(s.Color[0]), byte(s.Color[1]), byte(s.Color[2]), byte(total >> 8), byte(total), byte(s.OnMS >> 8), byte(s.OnMS), 0, 0}
	sum := 0
	for _, v := range b[:9] {
		sum += int(v)
	}
	b[9], b[10] = byte(sum>>8), byte(sum)
	return b
}
func TestStatusValidation(t *testing.T) {
	expected := State{Mode: "breath", Brightness: 64, Color: [3]int{12, 34, 56}, OnMS: 1000, OffMS: 1500}
	b := statusBytes(expected)
	got, err := DecodeStatus(b)
	if err != nil || got != expected {
		t.Fatal(got, err)
	}
	for _, bad := range [][]byte{nil, b[:10], make([]byte, 11), append([]byte{9}, b[1:]...)} {
		if _, err := DecodeStatus(bad); err == nil {
			t.Fatal("bad status accepted")
		}
	}
	b[3] ^= 1
	if _, err := DecodeStatus(b); err == nil {
		t.Fatal("bad checksum accepted")
	}
}
func FuzzDecodeStatus(f *testing.F) {
	f.Add(statusBytes(State{Mode: "on", Brightness: 64, Color: [3]int{255, 255, 255}}))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		s, err := DecodeStatus(b)
		if err == nil && (s.Brightness < 0 || s.Brightness > 255 || len(b) != 11) {
			t.Fatal("invalid result")
		}
	})
}

type fakeBus struct {
	states [4]State
	writes [][12]byte
	ack    byte
	fail   error
	reads  int
}

func newFake() *fakeBus {
	b := &fakeBus{ack: 1}
	for i := range b.states {
		b.states[i] = State{Mode: "on", Brightness: 64, Color: [3]int{255, 255, 255}}
	}
	return b
}
func (b *fakeBus) WriteFrame(_ byte, f [12]byte) error { b.writes = append(b.writes, f); return b.fail }
func (b *fakeBus) ReadAck() (byte, error)              { return b.ack, nil }
func (b *fakeBus) ReadStatus(id byte) ([]byte, error) {
	b.reads++
	return statusBytes(b.states[id]), nil
}
func (b *fakeBus) Close() error { return nil }
func TestControllerCachesAndReconciles(t *testing.T) {
	b := newFake()
	c := NewController(b)
	ctx := context.Background()
	if _, err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	s := b.states[2]
	for i := 0; i < 1000; i++ {
		if err := c.Apply(ctx, 2, s); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.writes) != 0 {
		t.Fatal("unchanged state generated writes")
	}
	s.Color = [3]int{12, 34, 56}
	if err := c.Apply(ctx, 2, s); err != nil {
		t.Fatal(err)
	}
	if len(b.writes) != 1 || b.writes[0][5] != 2 {
		t.Fatal("color should require exactly one command")
	}
	if err := c.Apply(ctx, 2, s); err != nil {
		t.Fatal(err)
	}
	if len(b.writes) != 1 {
		t.Fatal("color not cached")
	}
	// Simulate controller reset: readback disagrees with cache, requiring recovery.
	if _, err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, 2, s); err != nil {
		t.Fatal(err)
	}
	if len(b.writes) != 2 {
		t.Fatal("reset was not reconciled")
	}
}
func TestControllerFailsClosed(t *testing.T) {
	b := newFake()
	c := NewController(b)
	ctx := context.Background()
	_, _ = c.Refresh(ctx)
	s := b.states[0]
	s.Color = [3]int{255, 0, 0}
	b.fail = errors.New("bus failed")
	if err := c.Apply(ctx, 0, s); err == nil {
		t.Fatal("transport failure ignored")
	}
	if len(b.writes) != 1 || c.known[0] {
		t.Fatal("failed write retried or cached")
	}
	b = newFake()
	b.ack = 0
	c = NewController(b)
	_, _ = c.Refresh(ctx)
	if err := c.Apply(ctx, 0, s); err == nil || len(b.writes) != 3 {
		t.Fatalf("unbounded retries: %d %v", len(b.writes), err)
	}
}
func TestCanceledControllerNeverWrites(t *testing.T) {
	b := newFake()
	c := NewController(b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Apply(ctx, 0, b.states[0]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(b.writes) != 0 {
		t.Fatal("wrote after cancellation")
	}
}
func TestBusPacing(t *testing.T) {
	c := NewController(newFake())
	ctx := context.Background()
	if err := c.pace(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := c.pace(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 9*time.Millisecond {
		t.Fatal("bus pacing too short")
	}
}
