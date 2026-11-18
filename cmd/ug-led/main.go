package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/matteing/ug-led/internal/led"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "record-smart" {
		os.Exit(record(os.Args[2:], os.Stdout, os.Stderr))
	}
	if err := execute(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ug-led:", err)
		os.Exit(1)
	}
}
func execute(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "collect" {
		return collect(args[1:], out)
	}
	if len(args) == 0 {
		fmt.Fprintln(out, "ug-led: inspect | status | check | run | set | collect | record-smart | version\nUse ug-led <command> --help for options. Hardware access: original Intel DXP2800 only.")
		return nil
	}
	if args[0] == "version" {
		fmt.Fprintln(out, version)
		return nil
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(out)
	rawStatus := f.Bool("raw", false, "include raw controller status bytes for diagnostics (status)")
	config := f.String("config", "", "JSON configuration (required for check/run)")
	dry := f.Bool("dry-run", false, "print intended states without opening I2C (run/set)")
	duration := f.Duration("duration", 0, "stop after this duration (run)")
	name := f.String("led", "", "power, network, disk1, disk2, or all (set)")
	mode := f.String("mode", "on", "off, on, blink, or breath (set)")
	brightness := f.Int("brightness", 64, "brightness 0..255 (set)")
	color := f.String("color", "ffffff", "six-digit RGB hex (set)")
	on := f.Int("on-ms", 100, "blink/breath on phase in milliseconds (set)")
	off := f.Int("off-ms", 100, "blink/breath off phase in milliseconds (set)")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	// Reject options irrelevant to a command, rather than silently ignoring them.
	allowed := map[string]string{"inspect": "", "status": "raw", "check": "config", "run": "config dry-run duration", "set": "led mode brightness color on-ms off-ms dry-run"}
	options, ok := allowed[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}
	var flagErr error
	f.Visit(func(v *flag.Flag) {
		found := false
		for _, key := range splitOptions(options) {
			if key == v.Name {
				found = true
			}
		}
		if !found {
			flagErr = fmt.Errorf("--%s is not valid for %s", v.Name, args[0])
		}
	})
	if flagErr != nil {
		return flagErr
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration < 0 {
		return errors.New("duration must not be negative")
	}
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	encode := func(v any) error { e := json.NewEncoder(out); e.SetIndent("", "  "); return e.Encode(v) }
	var c led.Config
	var target led.State
	var ids []int
	switch args[0] {
	case "check", "run":
		if *config == "" {
			return errors.New("--config is required")
		}
		file, err := os.Open(*config)
		if err != nil {
			return err
		}
		c, err = led.ParseConfig(file)
		file.Close()
		if err != nil {
			return err
		}
		if args[0] == "check" {
			return encode(c)
		}
		if *dry {
			return cleanExit(led.Run(ctx, c, "/sys", nil, out))
		}
	case "set":
		rgb, err := hex.DecodeString(*color)
		if err != nil || len(rgb) != 3 {
			return errors.New("color must be six hex digits")
		}
		target = led.State{Mode: *mode, Brightness: *brightness, Color: [3]int{int(rgb[0]), int(rgb[1]), int(rgb[2])}, OnMS: *on, OffMS: *off}
		if err = target.Validate(); err != nil {
			return err
		}
		for id, n := range led.Names {
			if *name == n || *name == "all" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return errors.New("--led must name power, network, disk1, disk2, or all")
		}
		if *dry {
			for _, id := range ids {
				if err = encode(struct {
					LED   string    `json:"led"`
					State led.State `json:"state"`
				}{led.Names[id], target}); err != nil {
					return err
				}
			}
			return nil
		}
	}
	h, err := led.Inspect("/sys")
	if err != nil {
		return err
	}
	if args[0] == "inspect" {
		return encode(h)
	}
	bus, err := led.OpenBus(h)
	if err != nil {
		return err
	}
	defer bus.Close()
	ctl := led.NewController(bus)
	if args[0] == "status" && *rawStatus {
		reports, e := ctl.Diagnose(ctx)
		if outputErr := encode(reports); outputErr != nil {
			return outputErr
		}
		return e
	}
	initial, err := ctl.Refresh(ctx)
	if err != nil {
		return err
	}
	if args[0] == "status" {
		return encode(initial)
	}
	if args[0] == "set" {
		for _, id := range ids {
			if err = ctl.Apply(ctx, id, target); err != nil {
				return err
			}
		}
		actual, err := ctl.Refresh(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !sameState(actual[id], target) {
				return fmt.Errorf("%s readback differs from requested state", led.Names[id])
			}
		}
		return encode(actual)
	}
	for id, s := range initial {
		if e := s.Validate(); e != nil {
			return fmt.Errorf("cannot safely restore initial %s state: %w", led.Names[id], e)
		}
	}
	err = led.Run(ctx, c, "/sys", ctl, out)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// A normal stop restores the validated initial LED state when representable.
		// On controller/transport failure we do not attempt more writes.
		restore, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for id, s := range initial {
			if e := s.Validate(); e == nil {
				if e = ctl.Apply(restore, id, s); e != nil {
					return fmt.Errorf("restore %s: %w", led.Names[id], e)
				}
			}
		}
	}
	return cleanExit(err)
}
func sameState(a, b led.State) bool {
	return a.Mode == b.Mode && a.Brightness == b.Brightness && a.Color == b.Color && (a.Mode == "off" || a.Mode == "on" || (a.OnMS == b.OnMS && a.OffMS == b.OffMS))
}
func splitOptions(s string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	return result
}
func cleanExit(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func writeJSON(out io.Writer, v any) error {
	e := json.NewEncoder(out)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
