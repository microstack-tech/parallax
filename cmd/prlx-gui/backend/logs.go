// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ParallaxProtocol/parallax/log"
)

// LogTail captures a ring buffer of recent log entries and (optionally)
// streams them live to a frontend emitter. It is installed as the global
// log handler so it picks up everything written by both the GUI binary and
// the embedded node.
type LogTail struct {
	cap int

	mu      sync.RWMutex
	buf     []LogLine
	emitter func(LogLine)
	glogger *log.GlogHandler
}

// NewLogTail constructs a tail with the given capacity.
func NewLogTail(capacity int) *LogTail {
	if capacity <= 0 {
		capacity = 1024
	}
	return &LogTail{cap: capacity, buf: make([]LogLine, 0, capacity)}
}

// Install replaces the root log handler with a GlogHandler that fans out to
// (a) stderr in the standard terminal format and (b) this ring buffer for
// the in-app Logs pane. Verbosity defaults to Info (level 3) — matching the
// CLI's default — and can be changed at runtime with SetVerbosity.
func (l *LogTail) Install() {
	tail := log.FuncHandler(func(r *log.Record) error {
		l.append(LogLine{
			Timestamp: r.Time.Unix(),
			Level:     r.Lvl.AlignedString(),
			Message:   formatRecord(r),
		})
		return nil
	})
	stream := log.StreamHandler(os.Stderr, log.TerminalFormat(false))

	glogger := log.NewGlogHandler(log.MultiHandler(stream, tail))
	glogger.Verbosity(log.LvlInfo)

	l.mu.Lock()
	l.glogger = glogger
	l.mu.Unlock()

	log.Root().SetHandler(glogger)
}

// SetVerbosity changes the active log level. Valid values are 0 (silent)
// through 5 (trace); 3 is Info.
func (l *LogTail) SetVerbosity(level int) {
	l.mu.RLock()
	g := l.glogger
	l.mu.RUnlock()
	if g != nil {
		g.Verbosity(log.Lvl(level))
	}
}

// Close releases the emitter so future log records aren't pushed to a dead
// frontend channel. The ring buffer continues to capture for any subsequent
// frontend reconnect.
func (l *LogTail) Close() {
	l.mu.Lock()
	l.emitter = nil
	l.mu.Unlock()
}

// SetEmitter installs a live-stream callback. Pass nil to disable streaming.
func (l *LogTail) SetEmitter(fn func(LogLine)) {
	l.mu.Lock()
	l.emitter = fn
	l.mu.Unlock()
}

// Tail returns the most recent up to n log lines, oldest first. If n <= 0
// the entire buffer is returned.
func (l *LogTail) Tail(n int) []LogLine {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if n <= 0 || n > len(l.buf) {
		n = len(l.buf)
	}
	out := make([]LogLine, n)
	copy(out, l.buf[len(l.buf)-n:])
	return out
}

// formatRecord renders a log record as "msg key=value key=value …", matching
// the layout the geth terminal handler uses (minus the timestamp and level,
// which travel as separate fields on LogLine). Without this, the in-app log
// pane would show "Imported new chain segment" with all the structured
// context — block numbers, tx counts, gas, etc. — silently dropped.
func formatRecord(r *log.Record) string {
	if len(r.Ctx) == 0 {
		return r.Msg
	}
	var b strings.Builder
	// Pad the message a little so the key=value tail aligns the way the
	// terminal handler does. 40 columns matches geth's default.
	b.WriteString(r.Msg)
	for i := len(r.Msg); i < 40; i++ {
		b.WriteByte(' ')
	}
	for i := 0; i+1 < len(r.Ctx); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		key, _ := r.Ctx[i].(string)
		if key == "" {
			key = "?"
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(formatValue(r.Ctx[i+1]))
	}
	return b.String()
}

// formatValue stringifies a context value, quoting it if it contains spaces
// or quotes — same heuristic the geth logfmt formatter uses, simplified.
func formatValue(v interface{}) string {
	if v == nil {
		return "nil"
	}
	if s, ok := v.(string); ok {
		if strings.ContainsAny(s, ` "=`) {
			return fmt.Sprintf("%q", s)
		}
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprintf("%v", v)
}

func (l *LogTail) append(line LogLine) {
	l.mu.Lock()
	if len(l.buf) == l.cap {
		copy(l.buf, l.buf[1:])
		l.buf[l.cap-1] = line
	} else {
		l.buf = append(l.buf, line)
	}
	emitter := l.emitter
	l.mu.Unlock()
	if emitter != nil {
		emitter(line)
	}
}
