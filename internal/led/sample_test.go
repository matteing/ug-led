package led

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var sample = []byte("10 0 2048 5 20 0 4096 10 0 15 20 3 0 0 0 4 0\n")

func TestParseStats(t *testing.T) {
	s, e := ParseStats(sample)
	if e != nil || s.Reads != 10 || s.Writes != 20 || s.Discards != 3 || s.Flushes != 4 {
		t.Fatal(s, e)
	}
	for _, bad := range []string{"", "1 2", "1x 0 0 0 0 0 0 0 0 0 0", "18446744073709551616 0 0 0 0 0 0 0 0 0 0"} {
		if _, e = ParseStats([]byte(bad)); e == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, e = ParseStats([]byte("1 0 0 0 0 0 0 0 0 0 0")); e != nil {
		t.Fatal("old kernel stats rejected", e)
	}
}
func TestActivityBetweenSamplesAndReset(t *testing.T) {
	var a activity
	now := time.Unix(0, 0)
	hold := 400 * time.Millisecond
	s := DiskStats{Reads: 100}
	if a.update(s, now, hold) {
		t.Fatal("baseline blinked")
	}
	s.Reads++
	if !a.update(s, now.Add(200*time.Millisecond), hold) {
		t.Fatal("completed burst missed")
	}
	if !a.update(s, now.Add(400*time.Millisecond), hold) {
		t.Fatal("hold too short")
	}
	if a.update(s, now.Add(700*time.Millisecond), hold) {
		t.Fatal("hold never expired")
	}
	s.InFlight = 1
	if !a.update(s, now.Add(time.Second), hold) {
		t.Fatal("inflight ignored")
	}
	s = DiskStats{Reads: 1}
	if a.update(s, now.Add(1200*time.Millisecond), hold) {
		t.Fatal("counter reset caused activity")
	}
}
func writeFixture(t testing.TB, root, path, content string) {
	t.Helper()
	p := filepath.Join(root, path)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(content), 0644); e != nil {
		t.Fatal(e)
	}
}
func fixture(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	for _, pair := range [][2]string{{"sda", "ata1"}, {"sdb", "ata2"}} {
		writeFixture(t, root, "devices/pci/"+pair[1]+"/block/"+pair[0]+"/stat", string(sample))
		if e := os.MkdirAll(filepath.Join(root, "block"), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(filepath.Join(root, "devices/pci", pair[1], "block", pair[0]), filepath.Join(root, "block", pair[0])); e != nil {
			t.Fatal(e)
		}
	}
	for _, pair := range [][2]string{{"carrier", "1"}, {"statistics/rx_bytes", "100"}, {"statistics/tx_bytes", "200"}} {
		writeFixture(t, root, "class/net/eth0/"+pair[0], pair[1])
	}
	return root
}
func TestSamplerRemovalAndReappearance(t *testing.T) {
	root := fixture(t)
	c := DefaultConfig()
	c.Interface = "eth0"
	s := NewSampler(root, c)
	now := time.Now()
	states, e := s.Sample(now)
	if e != nil || states[2].Mode != "on" || states[3].Mode != "on" {
		t.Fatal(states, e)
	}
	writeFixture(t, root, "devices/pci/ata1/block/sda/stat", "11 0 2049 5 20 0 4096 10 0 15 20 3 0 0 0 4 0\n")
	states, e = s.Sample(now.Add(200 * time.Millisecond))
	if e != nil || states[2].Mode != "blink" || states[3].Mode != "on" {
		t.Fatal(states, e)
	}
	if e = os.Remove(filepath.Join(root, "block/sda")); e != nil {
		t.Fatal(e)
	}
	states, e = s.Sample(now.Add(400 * time.Millisecond))
	if e != nil || states[2].Mode != "off" {
		t.Fatal(states, e)
	}
	if e = os.Symlink(filepath.Join(root, "devices/pci/ata1/block/sda"), filepath.Join(root, "block/sda")); e != nil {
		t.Fatal(e)
	}
	states, e = s.Sample(now.Add(6 * time.Second))
	if e != nil || states[2].Mode != "on" {
		t.Fatal(states, e)
	}
}
func BenchmarkParseStats(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, e := ParseStats(sample); e != nil {
			b.Fatal(e)
		}
	}
}
func BenchmarkSampleTwoDisksAndNIC(b *testing.B) {
	root := fixture(b)
	c := DefaultConfig()
	c.Interface = "eth0"
	s := NewSampler(root, c)
	now := time.Now()
	if _, e := s.Sample(now); e != nil {
		b.Fatal(e)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, e := s.Sample(now); e != nil {
			b.Fatal(e)
		}
	}
}
