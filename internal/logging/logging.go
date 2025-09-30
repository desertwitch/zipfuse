// Package logging implements the handling of log messages.
package logging

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// BufferSize is the allocation size of the ring-buffer.
const BufferSize = 500

// Buffer is the primary ring-buffer for all program output.
var Buffer = newRingBuffer(BufferSize)

// ringBuffer is a simple ring-buffer implementation.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []string
	index int
	full  bool
	size  int
}

// newRingBuffer returns a pointer to a new [ringBuffer].
func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		buf:  make([]string, size),
		size: size,
	}
}

// Size returns the size of the ring-buffer.
func (l *ringBuffer) Size() int {
	return l.size
}

// Lines returns a copy of the slice of ring-buffer contents.
func (l *ringBuffer) Lines() []string {
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

// Reset returns the ring-buffer to zero state.
func (l *ringBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.buf = make([]string, l.size)
	l.index = 0
	l.full = false
}

// add adds a new message to the ring-buffer.
func (l *ringBuffer) add(msg string) {
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
