package led

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Run has one event loop. There are no subprocesses, disk device opens, or networking.
// Dry runs never open I2C and log state transitions only.
func Run(ctx context.Context, c Config, sysfs string, ctl *Controller, out io.Writer) error {
	if err := c.Validate(); err != nil {
		return err
	}
	sampler := NewSampler(sysfs, c)
	var previous [4]State
	known := false
	nextRefresh := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(time.Duration(c.PollMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		now := time.Now()
		if ctl != nil && !now.Before(nextRefresh) {
			if _, err := ctl.Refresh(ctx); err != nil {
				return err
			}
			nextRefresh = now.Add(30 * time.Second)
		}
		states, err := sampler.Sample(now)
		if err != nil {
			return err
		}
		for id, state := range states {
			if ctl != nil {
				if err := ctl.Apply(ctx, id, state); err != nil {
					return fmt.Errorf("%s: %w", Names[id], err)
				}
			} else if !known || state != previous[id] {
				fmt.Fprintf(out, "%s mode=%s brightness=%d rgb=%v on=%dms off=%dms\n", Names[id], state.Mode, state.Brightness, state.Color, state.OnMS, state.OffMS)
			}
		}
		previous = states
		known = true
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
