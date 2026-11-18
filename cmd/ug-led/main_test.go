package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSetDryRunDoesNotNeedHardware(t *testing.T) {
	var out bytes.Buffer
	e := execute([]string{"set", "--dry-run", "--led", "disk1", "--color", "00ff80", "--mode", "breath", "--on-ms", "1000", "--off-ms", "1000"}, &out)
	if e != nil || !strings.Contains(out.String(), "disk1") {
		t.Fatal(out.String(), e)
	}
}
func TestInvalidCLI(t *testing.T) {
	for _, args := range [][]string{{"status", "--dry-run"}, {"run"}, {"set", "--led", "fan"}, {"set", "--led", "disk1", "--color", "ffffff00"}, {"set", "--led", "disk1", "--brightness", "999"}, {"run", "--duration", "-1s"}, {"check", "unexpected"}} {
		var out bytes.Buffer
		if e := execute(args, &out); e == nil {
			t.Fatal("accepted", args)
		}
	}
}
