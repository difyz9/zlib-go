package ui

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Progress renders a single-line download progress indicator.
//
// It implements fetch.ProgressReporter, so the HTTP layer can drive it without
// knowing anything about terminals. Rendering goes to one line that is
// rewritten with \r, so output is usable both interactively and (as a degraded
// single final update) in captured logs.
type Progress struct {
	w      io.Writer
	label  string
	start  time.Time
	last   time.Time // when the line was last painted, for throttling
	drawn  bool      // whether a line is currently on screen
	closed bool
}

// NewProgress builds a progress indicator for one transfer.
//
// label is shown verbatim; keep it short because the bar shares the line with
// byte counts and a rate.
func NewProgress(w io.Writer, label string) *Progress {
	return &Progress{w: w, label: label, start: time.Now()}
}

// Progress paints the line, throttled to roughly ten frames per second.
//
// A known total draws a bar with a percentage; an unknown total (chunked
// transfer encoding) draws bytes and a rate only. A zero-delta update is
// skipped so a stalled transfer does not spin the rate downward.
func (p *Progress) Progress(written, total int64) {
	if p == nil || p.closed {
		return
	}
	now := time.Now()
	if p.drawn && now.Sub(p.last) < 100*time.Millisecond {
		return
	}
	p.last = now
	p.drawn = true

	var line string
	if total > 0 {
		pct := float64(written) / float64(total)
		if pct > 1 {
			pct = 1
		}
		line = p.label + "  " + bar(pct) + fmt.Sprintf("  %3.0f%%  %s / %s  %s",
			pct*100, humanProgressBytes(written), humanProgressBytes(total), p.rate(written))
	} else {
		line = p.label + "  " + humanProgressBytes(written) + "  " + p.rate(written)
	}
	fmt.Fprintf(p.w, "\r%s", padToWidth(line, 80))
}

// ProgressDone erases the indicator and leaves a clean line behind. The
// completion summary ("Downloaded: path") is printed by the caller, so this
// only clears the transient rendering.
func (p *Progress) ProgressDone(written, total int64) {
	if p == nil {
		return
	}
	p.closed = true
	if p.drawn {
		fmt.Fprintf(p.w, "\r%s\r", strings.Repeat(" ", 80))
	}
}

// rate renders the transfer speed so far.
func (p *Progress) rate(written int64) string {
	elapsed := time.Since(p.start)
	if elapsed < 200*time.Millisecond || written == 0 {
		return ""
	}
	return humanProgressBytes(int64(float64(written)/elapsed.Seconds())) + "/s"
}

// bar renders a 24-column gauge.
func bar(frac float64) string {
	const width = 24
	filled := int(frac * width)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", width-filled) + "]"
}

// padToWidth pads a line out so a shrinking rendering fully overwrites the
// previous frame's tail.
func padToWidth(s string, width int) string {
	if gap := width - DisplayWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// humanProgressBytes renders a size without the package-level FileSize's
// dependency on the fetch package.
func humanProgressBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
