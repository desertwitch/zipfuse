package logging

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Expectation: newRingBuffer should create a buffer with the correct size.
func Test_newRingBuffer_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(10, os.Stderr)

	require.NotNil(t, buf)
	require.Equal(t, 10, buf.Size())
	require.Equal(t, 0, buf.index)
	require.False(t, buf.full)
}

// Expectation: add should append messages to the buffer.
func Test_ringBuffer_add_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(3, os.Stderr)

	buf.add("first")
	buf.add("second")
	buf.add("third")

	lines := buf.Lines()

	require.Len(t, lines, 3)
	require.Equal(t, "first", lines[0])
	require.Equal(t, "second", lines[1])
	require.Equal(t, "third", lines[2])
}

// Expectation: add should wrap around when the buffer is full.
func Test_ringBuffer_add_WrapAround_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(3, os.Stderr)

	buf.add("first")
	buf.add("second")
	buf.add("third")
	buf.add("fourth") // wraps around, replaces "first"
	buf.add("fifth")  // replaces "second"

	lines := buf.Lines()

	require.Len(t, lines, 3)
	require.Equal(t, "third", lines[0])
	require.Equal(t, "fourth", lines[1])
	require.Equal(t, "fifth", lines[2])
}

// Expectation: add should trim trailing newlines.
func Test_ringBuffer_add_TrimNewline_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(2, os.Stderr)

	buf.add("message with newline\n")
	buf.add("another\n\n")

	lines := buf.Lines()

	require.Len(t, lines, 2)
	require.Equal(t, "message with newline", lines[0])
	require.Equal(t, "another\n", lines[1])
}

// Expectation: Lines should return the partial buffer when not full.
func Test_ringBuffer_Lines_PartialBuffer_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(5, os.Stderr)

	buf.add("one")
	buf.add("two")

	lines := buf.Lines()

	require.Len(t, lines, 2)
	require.Equal(t, "one", lines[0])
	require.Equal(t, "two", lines[1])
}

// Expectation: Lines should always return a copy, not the internal slice.
func Test_ringBuffer_Lines_ReturnsCopy_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(3, os.Stderr)
	buf.add("a")
	buf.add("b")

	lines := buf.Lines()
	require.Equal(t, []string{"a", "b"}, lines)

	lines[0] = "MUTATED"

	lines2 := buf.Lines()
	require.Equal(t, []string{"a", "b"}, lines2)

	buf = NewRingBuffer(3, os.Stderr)
	buf.add("x")
	buf.add("y")
	buf.add("z")
	buf.add("w") // overwrites "x"

	lines = buf.Lines()
	require.Equal(t, []string{"y", "z", "w"}, lines)

	lines[1] = "MUTATED"

	lines2 = buf.Lines()
	require.Equal(t, []string{"y", "z", "w"}, lines2)
}

// Expectation: Reset should return the buffer to empty, pre-allocated state.
func Test_ringBuffer_Reset_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(5, os.Stderr)

	buf.add("one")
	buf.add("two")
	buf.Reset()

	for _, v := range buf.buf {
		require.Empty(t, v)
	}
	require.Zero(t, buf.index)
	require.False(t, buf.full)
	require.Equal(t, 5, buf.size)
}

// Expectation: Concurrent access should be thread-safe.
func Test_ringBuffer_Concurrency_Success(t *testing.T) {
	t.Parallel()

	buf := NewRingBuffer(100, os.Stderr)
	done := make(chan bool)

	for i := range 10 {
		go func(id int) {
			for range 10 {
				buf.add(strings.Repeat("x", id))
			}
			done <- true
		}(i)
	}

	for range 10 {
		<-done
	}

	lines := buf.Lines()
	require.Len(t, lines, 100)
}

// Expectation: Printf should add to buffer and also write to stderr.
func Test_Printf_Success(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	buf := NewRingBuffer(100, &out)

	buf.Printf("test %s %d", "message", 42)

	lines := buf.Lines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "test message 42")
	require.Contains(t, out.String(), "test message 42")
}

// Expectation: Println should add to buffer and also write to stderr.
func Test_Println_Success(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	buf := NewRingBuffer(100, &out)

	buf.Println("test", "message")

	lines := buf.Lines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "test message")
	require.Contains(t, out.String(), "test message\n")
}
