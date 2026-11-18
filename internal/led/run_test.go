package led

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestDryRunCancellation(t *testing.T) {
	root := fixture(t)
	c := DefaultConfig()
	c.Interface = "eth0"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	err := Run(ctx, c, root, nil, &out)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("disk1 mode=on")) {
		t.Fatal(out.String())
	}
}
func TestRunRejectsInvalidConfigBeforeBus(t *testing.T) {
	c := DefaultConfig()
	c.PollMS = 0
	b := newFake()
	err := Run(context.Background(), c, t.TempDir(), NewController(b), &bytes.Buffer{})
	if err == nil || len(b.writes) != 0 || b.reads != 0 {
		t.Fatal("invalid config reached hardware")
	}
}
