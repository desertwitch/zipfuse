package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// logBuffer is a simple ring-buffer for the log messages of the filesystem.
// All the contained messages are served on the dashboard (via [diagnosticsMux]).
type logBuffer struct {
	mu    sync.Mutex
	buf   []string
	index int
	full  bool
	size  int
}

func newLogBuffer(size int) *logBuffer {
	return &logBuffer{
		buf:  make([]string, size),
		size: size,
	}
}

func (l *logBuffer) add(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.buf[l.index] = strings.TrimSuffix(msg, "\n")
	l.index = (l.index + 1) % l.size
	if l.index == 0 {
		l.full = true
	}
}

func (l *logBuffer) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.full {
		return l.buf[:l.index]
	}

	out := make([]string, l.size)
	copy(out, l.buf[l.index:])
	copy(out[l.size-l.index:], l.buf[:l.index])

	return out
}

// logPrintf adds a message to the ring-buffer and also prints it to stderr.
func logPrintf(format string, args ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	msg := fmt.Sprintf(format, args...)
	full := fmt.Sprintf("%s %s", timestamp, msg)

	logs.add(full)              // add to buffer with timestamp
	log.Printf(format, args...) // also goes to stderr
}

// logPrintln adds a message to the ring-buffer and also prints it to stderr.
func logPrintln(args ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	msg := fmt.Sprintln(args...)
	full := fmt.Sprintf("%s %s", timestamp, strings.TrimRight(msg, "\n"))

	logs.add(full)       // add to buffer with timestamp
	log.Println(args...) // also goes to stderr
}
