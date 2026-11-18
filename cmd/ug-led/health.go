package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/matteing/ug-led/internal/health"
)

func record(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("record-smart", flag.ContinueOnError)
	f.SetOutput(stderr)
	executable := f.String("smartctl", "", "absolute smartctl executable")
	dir := f.String("cache-dir", "", "existing directory for timestamped observations")
	if e := f.Parse(args); e != nil {
		return 1
	}
	if e := health.ValidateRecorder(*executable, *dir); e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	return health.RecordSMART(*executable, *dir, f.Args(), stdout, stderr)
}
func collect(args []string, out io.Writer) error {
	f := flag.NewFlagSet("collect", flag.ContinueOnError)
	f.SetOutput(out)
	config := f.String("config", "", "collector JSON configuration")
	check := f.Bool("check", false, "validate configuration without reading health sources")
	output := f.String("output", "", "atomic health snapshot output; omit for stdout")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || *config == "" {
		return fmt.Errorf("collect requires --config and no positional arguments")
	}
	file, e := os.Open(*config)
	if e != nil {
		return e
	}
	defer file.Close()
	c, e := health.ParseConfig(file)
	if e != nil {
		return e
	}
	if *check {
		if *output != "" {
			return fmt.Errorf("--check and --output cannot be combined")
		}
		return writeJSON(out, c)
	}
	s := health.Collect(c, time.Now().UTC(), "/sys", "/dev")
	if *output != "" {
		return health.AtomicJSON(*output, s)
	}
	return writeJSON(out, s)
}
