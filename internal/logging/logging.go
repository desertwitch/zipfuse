// Package logging implements the handling of log messages.
package logging

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// BufferSize is the to-allocate size of the ring-buffer.
const BufferSize = 500

// Buffer is the main ring-buffer for all program output.
var Buffer = newRingBuffer(BufferSize)

// ringBuffer is a simple ring-buffer for log messages.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []string
	index int
	full  bool
	size  int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		buf:  make([]string, size),
		size: size,
	}
}

func (l *ringBuffer) Size() int {
	return l.size
}

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

func (l *ringBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.buf = make([]string, l.size)
	l.index = 0
	l.full = false
}

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
