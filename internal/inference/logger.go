package inference

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// InferenceLogger writes log output to both stdout and an optional file simultaneously.
// When no file is set, it behaves exactly like fmt.Printf.
type InferenceLogger struct {
	mu      sync.Mutex
	file    *os.File
	writers []io.Writer
}

// NewInferenceLogger creates a logger that writes to stdout only.
func NewInferenceLogger() *InferenceLogger {
	return &InferenceLogger{
		writers: []io.Writer{os.Stdout},
	}
}

// SetFile sets an additional log file destination.
// All subsequent Printf calls will write to both stdout and this file.
func (l *InferenceLogger) SetFile(f *os.File) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.file = f
	if f != nil {
		l.writers = []io.Writer{os.Stdout, f}
	} else {
		l.writers = []io.Writer{os.Stdout}
	}
}

// CloseFile closes the current log file (if any) and reverts to stdout-only.
func (l *InferenceLogger) CloseFile() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Sync()
		l.file.Close()
		l.file = nil
	}
	l.writers = []io.Writer{os.Stdout}
}

// Printf writes formatted output to all destinations (stdout + file).
func (l *InferenceLogger) Printf(format string, a ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(format, a...)
	for _, w := range l.writers {
		fmt.Fprint(w, msg)
	}
}

// Println writes a line to all destinations.
func (l *InferenceLogger) Println(a ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintln(a...)
	for _, w := range l.writers {
		fmt.Fprint(w, msg)
	}
}

// FileOnly writes formatted output to the file only (not stdout).
// This is useful for writing detailed info that shouldn't clutter the terminal.
func (l *InferenceLogger) FileOnly(format string, a ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintf(l.file, format, a...)
	}
}

// Sync flushes the file buffer.
func (l *InferenceLogger) Sync() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Sync()
	}
}
