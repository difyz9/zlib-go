package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestProgressKnownTotalRendersBarAndPercent verifies the interactive line:
// a gauge, a percentage, and human-readable byte counts.
func TestProgressKnownTotalRendersBarAndPercent(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, "Downloading")
	// Bypass the frame throttle the way a real multi-chunk transfer would: the
	// first paint is immediate, later ones need 100ms to elapse.
	p.Progress(250_000, 1_000_000)
	out := buf.String()
	if !strings.HasPrefix(out, "\rDownloading") {
		t.Errorf("line did not start at column 0 with the label: %q", out)
	}
	if !strings.Contains(out, "[") || !strings.Contains(out, "25%") {
		t.Errorf("gauge or percentage missing: %q", out)
	}
	if !strings.Contains(out, "244.1 KB / 976.6 KB") {
		t.Errorf("byte counts missing: %q", out)
	}
}

// TestProgressUnknownTotalOmitsPercentage covers servers that stream with
// chunked encoding and no Content-Length.
func TestProgressUnknownTotalOmitsPercentage(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, "Downloading")
	p.Progress(1_048_576, 0)
	if strings.Contains(buf.String(), "%") {
		t.Errorf("percentage shown for an unknown total: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "1.0 MB") {
		t.Errorf("byte count missing: %q", buf.String())
	}
}

// TestProgressDoneClearsTheLine: the completion call must erase the transient
// rendering so the caller's summary starts on a clean line.
func TestProgressDoneClearsTheLine(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, "Downloading")
	p.Progress(1, 10)
	p.ProgressDone(10, 10)
	out := buf.String()
	if !strings.HasSuffix(out, "\r") {
		t.Errorf("line was not cleared back to column 0: %q", out)
	}
}

// TestProgressThrottlesRepeatedUpdates: painting on every chunk would burn CPU
// on fast transfers, so updates within the frame interval are dropped.
func TestProgressThrottlesRepeatedUpdates(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, "Downloading")
	p.Progress(1, 100)
	first := buf.Len()
	p.Progress(50, 100)
	if buf.Len() != first {
		t.Error("an update inside the frame interval was painted anyway")
	}
	p.last = p.last.Add(-200 * time.Millisecond)
	p.Progress(50, 100)
	if buf.Len() == first {
		t.Error("an update outside the frame interval was dropped")
	}
}
