package logger

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
)

// TaskState represents the state of a single task in the multi-progress display
type TaskState int

const (
	TaskPending TaskState = iota
	TaskRunning
	TaskDone
	TaskFailed
)

// MultiTask holds the state for one task row
type MultiTask struct {
	Name      string
	State     TaskState
	Phase     string // current sub-phase description, e.g. "Phase 1: Discovering Tables"
	Progress  int    // 0-100
	StartTime time.Time
	EndTime   time.Time
	Error     string
	Detail    string // one-line status detail, e.g. "analyzing table: ship"
}

// MultiProgress provides a Docker-style parallel progress display
// that refreshes multiple lines in-place using ANSI escape codes.
type MultiProgress struct {
	mu          sync.Mutex
	tasks       []*MultiTask
	taskIndex   map[string]int // name -> index in tasks slice
	startTime   time.Time
	rendered    bool // whether we have rendered at least once
	lineCount   int  // number of lines rendered last time
	ticker      *time.Ticker
	done        chan struct{}
	isTTY       bool
	logBuffer   []string // buffered detail logs for non-TTY mode
	title       string
}

// NewMultiProgress creates a new multi-progress display with the given task names
func NewMultiProgress(title string, taskNames []string) *MultiProgress {
	mp := &MultiProgress{
		tasks:     make([]*MultiTask, len(taskNames)),
		taskIndex: make(map[string]int, len(taskNames)),
		startTime: time.Now(),
		done:      make(chan struct{}),
		isTTY:     isTerminal(),
		title:     title,
	}

	for i, name := range taskNames {
		mp.tasks[i] = &MultiTask{
			Name:  name,
			State: TaskPending,
		}
		mp.taskIndex[name] = i
	}

	return mp
}

// isTerminal checks if stdout is a terminal (supports ANSI codes)
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// Start begins the periodic refresh loop
func (mp *MultiProgress) Start() {
	if mp.isTTY {
		// Hide cursor during rendering
		fmt.Print(ansiHideCursor)

		// Ensure cursor is restored on interrupt
		mp.setupSignalHandler()

		// Initial render
		mp.render()
		// Refresh every 200ms
		mp.ticker = time.NewTicker(200 * time.Millisecond)
		go func() {
			for {
				select {
				case <-mp.ticker.C:
					mp.mu.Lock()
					mp.render()
					mp.mu.Unlock()
				case <-mp.done:
					return
				}
			}
		}()
	} else {
		// Non-TTY: print header once
		fmt.Printf("\n%s\n", mp.title)
	}
}

// Stop stops the refresh loop and renders the final state
func (mp *MultiProgress) Stop() {
	if mp.ticker != nil {
		mp.ticker.Stop()
	}
	close(mp.done)

	mp.mu.Lock()
	defer mp.mu.Unlock()

	if mp.isTTY {
		mp.render()
		fmt.Print(ansiShowCursor) // restore cursor
		fmt.Println()             // extra newline after final render
	} else {
		// Print any remaining buffered logs
		for _, line := range mp.logBuffer {
			fmt.Println(line)
		}
		mp.logBuffer = nil
	}
}

// setupSignalHandler ensures cursor is restored on Ctrl+C
func (mp *MultiProgress) setupSignalHandler() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		<-ch
		fmt.Print(ansiShowCursor)
		os.Exit(130)
	}()
}

// StartTask marks a task as running
func (mp *MultiProgress) StartTask(name string) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if idx, ok := mp.taskIndex[name]; ok {
		mp.tasks[idx].State = TaskRunning
		mp.tasks[idx].StartTime = time.Now()
		mp.tasks[idx].Phase = "Starting..."
		mp.tasks[idx].Progress = 0
	}

	if !mp.isTTY {
		mp.logBuffer = append(mp.logBuffer, fmt.Sprintf("  🔄 %s: started", name))
	}
}

// UpdateTask updates the progress and detail of a running task
func (mp *MultiProgress) UpdateTask(name, phase string, progress int) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if idx, ok := mp.taskIndex[name]; ok {
		mp.tasks[idx].Phase = phase
		if progress >= 0 && progress <= 100 {
			mp.tasks[idx].Progress = progress
		}
	}
}

// SetTaskDetail sets a one-line status detail for a task
func (mp *MultiProgress) SetTaskDetail(name, detail string) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if idx, ok := mp.taskIndex[name]; ok {
		mp.tasks[idx].Detail = detail
	}
}

// CompleteTask marks a task as done
func (mp *MultiProgress) CompleteTask(name string) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if idx, ok := mp.taskIndex[name]; ok {
		mp.tasks[idx].State = TaskDone
		mp.tasks[idx].EndTime = time.Now()
		mp.tasks[idx].Progress = 100
		mp.tasks[idx].Phase = "Done"
	}

	if !mp.isTTY {
		duration := ""
		if idx, ok := mp.taskIndex[name]; ok {
			duration = formatDuration(mp.tasks[idx].EndTime.Sub(mp.tasks[idx].StartTime))
		}
		mp.logBuffer = append(mp.logBuffer, fmt.Sprintf("  ✅ %s: done (%s)", name, duration))
		mp.flushLogBuffer()
	}
}

// FailTask marks a task as failed
func (mp *MultiProgress) FailTask(name string, err error) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if idx, ok := mp.taskIndex[name]; ok {
		mp.tasks[idx].State = TaskFailed
		mp.tasks[idx].EndTime = time.Now()
		mp.tasks[idx].Phase = "Failed"
		mp.tasks[idx].Error = err.Error()
	}

	if !mp.isTTY {
		mp.logBuffer = append(mp.logBuffer, fmt.Sprintf("  ❌ %s: %v", name, err))
		mp.flushLogBuffer()
	}
}

// flushLogBuffer prints buffered logs in non-TTY mode
func (mp *MultiProgress) flushLogBuffer() {
	for _, line := range mp.logBuffer {
		fmt.Println(line)
	}
	mp.logBuffer = nil
}

// Summary returns a summary string
func (mp *MultiProgress) Summary() string {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	var completed, failed int
	for _, t := range mp.tasks {
		switch t.State {
		case TaskDone:
			completed++
		case TaskFailed:
			failed++
		}
	}

	totalDuration := time.Since(mp.startTime)
	var sb strings.Builder

	sb.WriteString("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("📊 Generation Summary\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("  Total:     %d databases\n", len(mp.tasks)))
	sb.WriteString(fmt.Sprintf("  ✅ Done:    %d\n", completed))
	sb.WriteString(fmt.Sprintf("  ❌ Failed:  %d\n", failed))
	sb.WriteString(fmt.Sprintf("  ⏱️  Time:    %s\n", formatDuration(totalDuration)))

	if completed > 0 {
		avgTime := totalDuration / time.Duration(completed)
		sb.WriteString(fmt.Sprintf("  ⚡ Avg:     %s / database\n", formatDuration(avgTime)))
	}

	if failed > 0 {
		sb.WriteString("\n  Failed databases:\n")
		for _, t := range mp.tasks {
			if t.State == TaskFailed {
				errMsg := t.Error
				if len(errMsg) > 80 {
					errMsg = errMsg[:77] + "..."
				}
				sb.WriteString(fmt.Sprintf("    - %-25s %s\n", t.Name, errMsg))
			}
		}
	}

	sb.WriteString("\n")
	return sb.String()
}

// ──────────────────────────────────────────────────────
// ANSI rendering engine
// ──────────────────────────────────────────────────────

const (
	ansiReset      = "\033[0m"
	ansiBold       = "\033[1m"
	ansiDim        = "\033[2m"
	ansiGreen      = "\033[32m"
	ansiRed        = "\033[31m"
	ansiYellow     = "\033[33m"
	ansiCyan       = "\033[36m"
	ansiBlue       = "\033[34m"
	ansiClearLine  = "\033[2K"
	ansiHideCursor = "\033[?25l"
	ansiShowCursor = "\033[?25h"
)

// render redraws all task lines in place (must be called with mu held)
func (mp *MultiProgress) render() {
	// Move cursor up to overwrite previous output
	if mp.rendered && mp.lineCount > 0 {
		fmt.Printf("\033[%dA", mp.lineCount)
	}

	var lines []string

	// Title line
	lines = append(lines, fmt.Sprintf("%s%s%s", ansiBold, mp.title, ansiReset))
	lines = append(lines, "")

	// Task lines
	maxNameLen := 0
	for _, t := range mp.tasks {
		if len(t.Name) > maxNameLen {
			maxNameLen = len(t.Name)
		}
	}
	if maxNameLen < 15 {
		maxNameLen = 15
	}

	for _, t := range mp.tasks {
		lines = append(lines, mp.renderTaskLine(t, maxNameLen))
	}

	// Summary bar
	lines = append(lines, "")
	lines = append(lines, mp.renderSummaryBar())

	// Write all lines
	var output strings.Builder
	for _, line := range lines {
		output.WriteString(ansiClearLine)
		output.WriteString(line)
		output.WriteString("\n")
	}
	fmt.Print(output.String())

	mp.lineCount = len(lines)
	mp.rendered = true
}

// renderTaskLine renders a single task row
func (mp *MultiProgress) renderTaskLine(t *MultiTask, maxNameLen int) string {
	var icon, color string
	var elapsed time.Duration

	switch t.State {
	case TaskPending:
		icon = "⏳"
		color = ansiDim
	case TaskRunning:
		elapsed = time.Since(t.StartTime)
		icon = spinnerFrame(elapsed)
		color = ansiCyan
	case TaskDone:
		elapsed = t.EndTime.Sub(t.StartTime)
		icon = "✅"
		color = ansiGreen
	case TaskFailed:
		elapsed = t.EndTime.Sub(t.StartTime)
		icon = "❌"
		color = ansiRed
	}

	name := fmt.Sprintf("%-*s", maxNameLen, t.Name)

	// Progress bar (20 chars wide)
	bar := renderBar(t.Progress, 20)

	// Time display
	timeStr := ""
	if t.State == TaskRunning || t.State == TaskDone || t.State == TaskFailed {
		timeStr = formatDuration(elapsed)
	}

	// Phase / detail
	phase := t.Phase
	if t.State == TaskFailed && t.Error != "" {
		phase = t.Error
		if len(phase) > 50 {
			phase = phase[:47] + "..."
		}
	}
	if len(phase) > 40 {
		phase = phase[:37] + "..."
	}

	return fmt.Sprintf(" %s %s%s%s %s %3d%% %s%-40s%s",
		icon, color, name, ansiReset, bar, t.Progress, ansiDim, phase, ansiReset) +
		fmt.Sprintf(" %s", timeStr)
}

// renderBar creates an ASCII progress bar
func renderBar(percent, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := width * percent / 100
	empty := width - filled

	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	return fmt.Sprintf("%s%s%s%s%s", ansiBlue, string([]rune(bar)[:filled]), ansiDim, string([]rune(bar)[filled:]), ansiReset)
}

// renderSummaryBar renders the bottom summary line
func (mp *MultiProgress) renderSummaryBar() string {
	var completed, failed, running, pending int
	for _, t := range mp.tasks {
		switch t.State {
		case TaskPending:
			pending++
		case TaskRunning:
			running++
		case TaskDone:
			completed++
		case TaskFailed:
			failed++
		}
	}

	total := len(mp.tasks)
	done := completed + failed
	elapsed := time.Since(mp.startTime)

	// Calculate ETA
	etaStr := "calculating..."
	if done > 0 {
		avgTime := elapsed / time.Duration(done)
		remaining := total - done
		eta := avgTime * time.Duration(remaining)
		etaStr = formatDuration(eta)
	}

	overallPercent := 0
	if total > 0 {
		overallPercent = done * 100 / total
	}

	bar := renderBar(overallPercent, 30)

	return fmt.Sprintf(" %s%sOverall%s  %s %d/%d  ⏱️  %s  ETA %s  "+
		"(%s●%s %d running, %s●%s %d pending, %s●%s %d done, %s●%s %d fail)",
		ansiBold, ansiYellow, ansiReset,
		bar, done, total,
		formatDuration(elapsed), etaStr,
		ansiCyan, ansiReset, running,
		ansiDim, ansiReset, pending,
		ansiGreen, ansiReset, completed,
		ansiRed, ansiReset, failed,
	)
}

// spinnerFrame returns a rotating spinner character based on elapsed time
func spinnerFrame(elapsed time.Duration) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	idx := int(elapsed.Milliseconds()/100) % len(frames)
	return frames[idx]
}
