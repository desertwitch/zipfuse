// Package logging implements the handling of log messages within the program.
package logging

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const logBufferLinesMax = 500

// Buffer is the global ring-buffer for all of the program events.
var Buffer = newLogBuffer(logBufferLinesMax)

// logBuffer is a simple ring-buffer for log messages.
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

func (l *logBuffer) Size() int {
	return l.size
}

func (l *logBuffer) Lines() []string {
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

func (l *logBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.buf = make([]string, l.size)
	l.index = 0
	l.full = false
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

// Printf adds a message to the ring-buffer and also prints it to stderr.
func Printf(format string, args ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	msg := fmt.Sprintf(format, args...)
	full := fmt.Sprintf("%s %s", timestamp, msg)

	Buffer.add(full)            // add to buffer with timestamp
	log.Printf(format, args...) // also goes to stderr
}

// Println adds a message to the ring-buffer and also prints it to stderr.
func Println(args ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	msg := fmt.Sprintln(args...)
	full := fmt.Sprintf("%s %s", timestamp, strings.TrimRight(msg, "\n"))

	Buffer.add(full)     // add to buffer with timestamp
	log.Println(args...) // also goes to stderr
}
