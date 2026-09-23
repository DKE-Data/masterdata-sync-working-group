package main

import (
	"log/slog"
	"sync"
	"time"
)

// eventLog is what the screens narrate from: a short history of what this
// participant did, and a way to watch it happen.
//
// It is narration and nothing else. Nothing in the protocol reads it, nothing
// resumes from it, and it is not written down — losing it on restart costs a
// scrollback, not a position.
type eventLog struct {
	mu      sync.Mutex
	entries []logEntry
	subs    map[chan logEntry]struct{}
}

type logEntry struct {
	At   time.Time
	Kind string
	Text string
}

// keptEntries bounds the history a long-running demo accumulates.
const keptEntries = 300

func newEventLog() *eventLog {
	return &eventLog{subs: map[chan logEntry]struct{}{}}
}

func (l *eventLog) say(kind, text string) {
	entry := logEntry{At: time.Now(), Kind: kind, Text: text}
	slog.Info(text, "kind", kind)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
	if len(l.entries) > keptEntries {
		l.entries = l.entries[len(l.entries)-keptEntries:]
	}
	for sub := range l.subs {
		// A watcher that has stopped reading is skipped rather than waited for:
		// a browser tab nobody is looking at must not stall the receive loop.
		select {
		case sub <- entry:
		default:
		}
	}
}

// recent returns the history, most recent first.
func (l *eventLog) recent() []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]logEntry, len(l.entries))
	for n := range l.entries {
		out[len(out)-1-n] = l.entries[n]
	}
	return out
}

// watch returns a channel of entries from now on, and the function that stops
// it. Buffered, because the sender never waits.
func (l *eventLog) watch() (<-chan logEntry, func()) {
	ch := make(chan logEntry, 32)

	l.mu.Lock()
	l.subs[ch] = struct{}{}
	l.mu.Unlock()

	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, ok := l.subs[ch]; ok {
			delete(l.subs, ch)
			close(ch)
		}
	}
}
