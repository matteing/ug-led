package led

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DiskStats struct{ Reads, Writes, ReadSectors, WriteSectors, Discards, Flushes, InFlight uint64 }

// ParseStats reads numeric fields without allocating a slice of strings.
func ParseStats(b []byte) (DiskStats, error) {
	var values [17]uint64
	n := 0
	for i := 0; i < len(b); {
		if b[i] == ' ' || b[i] == '\t' || b[i] == '\n' {
			i++
			continue
		}
		var v uint64
		digits := 0
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			d := uint64(b[i] - '0')
			if v > (^uint64(0)-d)/10 {
				return DiskStats{}, errors.New("counter overflow")
			}
			v = v*10 + d
			i++
			digits++
		}
		if digits == 0 || (i < len(b) && b[i] != ' ' && b[i] != '\t' && b[i] != '\n') {
			return DiskStats{}, errors.New("invalid counter")
		}
		if n < len(values) {
			values[n] = v
		}
		n++
	}
	if n < 11 {
		return DiskStats{}, errors.New("short disk stats")
	}
	return DiskStats{Reads: values[0], Writes: values[4], ReadSectors: values[2], WriteSectors: values[6], Discards: values[11], Flushes: values[15], InFlight: values[8]}, nil
}
func readSmall(path string, buf []byte) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n == len(buf) {
		return nil, errors.New("sysfs value exceeds buffer")
	}
	return buf[:n], nil
}
func ReadDiskStats(path string) (DiskStats, error) {
	var b [1024]byte
	v, e := readSmall(path, b[:])
	if e != nil {
		return DiskStats{}, e
	}
	return ParseStats(v)
}
func readNumber(path string) (uint64, error) {
	var buf [64]byte
	b, e := readSmall(path, buf[:])
	if e != nil {
		return 0, e
	}
	var v uint64
	n := 0
	for _, c := range b {
		if c == '\n' {
			break
		}
		if c < '0' || c > '9' {
			return 0, errors.New("invalid sysfs integer")
		}
		d := uint64(c - '0')
		if v > (^uint64(0)-d)/10 {
			return 0, errors.New("sysfs integer overflow")
		}
		v = v*10 + d
		n++
	}
	if n == 0 {
		return 0, errors.New("empty sysfs integer")
	}
	return v, nil
}

type activity struct {
	previous DiskStats
	valid    bool
	until    time.Time
}

func (a *activity) update(s DiskStats, now time.Time, hold time.Duration) bool {
	if a.valid {
		reset := s.Reads < a.previous.Reads || s.Writes < a.previous.Writes || s.ReadSectors < a.previous.ReadSectors || s.WriteSectors < a.previous.WriteSectors || s.Discards < a.previous.Discards || s.Flushes < a.previous.Flushes
		changed := s.Reads != a.previous.Reads || s.Writes != a.previous.Writes || s.ReadSectors != a.previous.ReadSectors || s.WriteSectors != a.previous.WriteSectors || s.Discards != a.previous.Discards || s.Flushes != a.previous.Flushes
		if reset {
			a.until = time.Time{}
		} else if changed || s.InFlight > 0 {
			a.until = now.Add(hold)
		}
	} else if s.InFlight > 0 {
		a.until = now.Add(hold)
	}
	a.previous = s
	a.valid = true
	return now.Before(a.until)
}

type Sampler struct {
	root     string
	config   Config
	disks    [2]string
	stats    [2]activity
	network  activity
	nextScan time.Time
}

func NewSampler(root string, c Config) *Sampler { return &Sampler{root: root, config: c} }
func (s *Sampler) discover() error {
	var disks [2]string
	entries, err := filepath.Glob(filepath.Join(s.root, "block/sd*"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		real, err := filepath.EvalSymlinks(entry)
		if err != nil {
			continue
		}
		for id, port := range s.config.Bays {
			if strings.Contains(real, "/"+port+"/") {
				if disks[id] != "" {
					return fmt.Errorf("multiple disks on %s", port)
				}
				disks[id] = filepath.Join(entry, "stat")
			}
		}
	}
	for id := range disks {
		if disks[id] != s.disks[id] {
			s.stats[id] = activity{}
		}
	}
	s.disks = disks
	return nil
}
func (s *Sampler) Sample(now time.Time) ([4]State, error) {
	if !now.Before(s.nextScan) {
		if err := s.discover(); err != nil {
			return [4]State{}, err
		}
		s.nextScan = now.Add(5 * time.Second)
	}
	result := s.config.LEDs.Array()
	hold := time.Duration(s.config.HoldMS) * time.Millisecond
	for id := range result {
		if result[id].Mode != "activity" {
			continue
		}
		present, active := false, false
		if id >= 2 {
			i := id - 2
			if s.disks[i] != "" {
				stat, err := ReadDiskStats(s.disks[i])
				if err == nil {
					present = true
					active = s.stats[i].update(stat, now, hold)
				} else {
					s.stats[i] = activity{}
					s.nextScan = time.Time{}
				}
			}
		} else {
			base := filepath.Join(s.root, "class/net", s.config.Interface)
			carrier, e := readNumber(filepath.Join(base, "carrier"))
			rx, e1 := readNumber(filepath.Join(base, "statistics/rx_bytes"))
			tx, e2 := readNumber(filepath.Join(base, "statistics/tx_bytes"))
			if e == nil && e1 == nil && e2 == nil && carrier == 1 {
				present = true
				active = s.network.update(DiskStats{Reads: rx, Writes: tx}, now, hold)
			} else {
				s.network = activity{}
			}
		}
		result[id].Mode = "off"
		if present {
			result[id].Mode = "on"
		}
		if active {
			result[id].Mode = "blink"
		}
	}
	cap := s.config.BrightnessCap(now)
	for id := range result {
		result[id].Brightness = min(result[id].Brightness, cap)
	}
	return result, nil
}
