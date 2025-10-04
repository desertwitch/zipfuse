// Package logging implements the handling of logs.
package logging

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// RingBuffer is a simple ring-buffer implementation.
type RingBuffer struct {
	mu    sync.Mutex
	out   io.Writer
	buf   []string
	index int
	full  bool
	size  int
}

// NewRingBuffer returns a pointer to a new [ringBuffer].
func NewRingBuffer(size int, out io.Writer) *RingBuffer {
	return &RingBuffer{
		out:  out,
		buf:  make([]string, size),
		size: size,
	}
}

// Size returns the size of the ring-buffer.
func (b *RingBuffer) Size() int {
	return b.size
}

// Lines returns a copy of the slice of ring-buffer contents.
func (b *RingBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.full {
		out := make([]string, b.index)
		copy(out, b.buf[:b.index])

		return out
	}
	out := make([]string, b.size)
	copy(out, b.buf[b.index:])
	copy(out[b.size-b.index:], b.buf[:b.index])

	return out
}

// Reset returns the ring-buffer to zero state.
func (b *RingBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = make([]string, b.size)
	b.index = 0
	b.full = false
}

// Printf adds a message to the ring-buffer and also prints it to stderr.
func (b *RingBuffer) Printf(format string, args ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	msg := fmt.Sprintf(format, args...)
	full := fmt.Sprintf("%s %s", timestamp, msg)

	b.add(full)                    // add to buffer with timestamp
	fmt.Fprintf(b.out, "%s", full) // also goes to stream
}

// Println adds a message to the ring-buffer and also prints it to stderr.
func (b *RingBuffer) Println(args ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	msg := fmt.Sprintln(args...)
	full := fmt.Sprintf("%s %s", timestamp, strings.TrimRight(msg, "\n"))

	b.add(full)                    // add to buffer with timestamp
	fmt.Fprintf(b.out, "%s", full) // also goes to stream
}

// add adds a new message to the ring-buffer.
func (b *RingBuffer) add(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf[b.index] = strings.TrimSuffix(msg, "\n")
	b.index = (b.index + 1) % b.size
	if b.index == 0 {
		b.full = true
	}
}
