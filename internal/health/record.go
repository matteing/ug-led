package health

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type boundedCapture struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedCapture) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 1048576 - b.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

// RecordSMART transparently forwards one existing monitor invocation. It does not
// add probes or change arguments. Recording failures are logged, while the exact
// smartctl exit code and output are preserved for the original caller.
func RecordSMART(executable, dir string, args []string, stdout, stderr io.Writer) int {
	observedAt := time.Now().UTC()
	var capture boundedCapture
	cmd := exec.Command(executable, args...)
	cmd.Stdout = io.MultiWriter(stdout, &capture)
	cmd.Stderr = stderr
	code := 0
	if e := cmd.Run(); e != nil {
		if ee, ok := e.(*exec.ExitError); ok {
			code = ee.ExitCode()
			if code < 0 {
				code = 1
			}
		} else {
			fmt.Fprintln(stderr, "ug-led record-smart:", e)
			code = 1
		}
	}
	// Only explicitly supplied stable device paths are recorded. Scan/version
	// commands and devices outside by-id are forwarded without creating records.
	if len(args) > 0 && strings.HasPrefix(args[len(args)-1], "/dev/disk/by-id/") {
		id := strings.TrimPrefix(args[len(args)-1], "/dev/disk/by-id/")
		raw := json.RawMessage(capture.Bytes())
		if capture.overflow || !json.Valid(raw) {
			raw = json.RawMessage(`{}`)
		}
		if e := SaveReading(dir, id, Reading{At: observedAt, ExitCode: code, Report: raw}); e != nil {
			fmt.Fprintln(stderr, "ug-led record-smart: cannot record observation:", e)
		}
	}
	return code
}
func ValidateRecorder(executable, dir string) error {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(dir) {
		return fmt.Errorf("smartctl and cache-dir must be absolute paths")
	}
	return nil
}
