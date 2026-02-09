package logger

import (
	"fmt"
	"sync"
	"time"
)

// Logger progress logger
type Logger struct {
	mu             sync.Mutex
	totalTasks     int
	completedTasks int
	startTime      time.Time
	currentPhase   string
	taskDetails    map[string]*TaskProgress
}

// TaskProgress task progress
type TaskProgress struct {
	Name      string
	Status    string // "pending", "running", "completed", "failed"
	StartTime time.Time
	EndTime   time.Time
	Error     string
}

// NewLogger creates new logger
func NewLogger(totalTasks int) *Logger {
	return &Logger{
		totalTasks:  totalTasks,
		startTime:   time.Now(),
		taskDetails: make(map[string]*TaskProgress),
	}
}

// SetPhase sets current phase
func (l *Logger) SetPhase(phase string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.currentPhase = phase
	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("📍 %s\n", phase)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

// StartTask starts task
func (l *Logger) StartTask(taskName string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.taskDetails[taskName] = &TaskProgress{
		Name:      taskName,
		Status:    "running",
		StartTime: time.Now(),
	}

	fmt.Printf("[%s] 🔄 Started\n", taskName)
}

// CompleteTask completes task
func (l *Logger) CompleteTask(taskName string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if task, ok := l.taskDetails[taskName]; ok {
		task.Status = "completed"
		task.EndTime = time.Now()
		l.completedTasks++

		duration := task.EndTime.Sub(task.StartTime)
		fmt.Printf("[%s] ✓ Completed (%.2fs)\n", taskName, duration.Seconds())
		l.printProgress()
	}
}

// FailTask fails task
func (l *Logger) FailTask(taskName string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if task, ok := l.taskDetails[taskName]; ok {
		task.Status = "failed"
		task.EndTime = time.Now()
		task.Error = err.Error()
		l.completedTasks++

		fmt.Printf("[%s] ✗ Failed: %v\n", taskName, err)
		l.printProgress()
	}
}

// printProgress prints progress (internal, locked)
func (l *Logger) printProgress() {
	if l.totalTasks == 0 {
		return
	}

	percentage := float64(l.completedTasks) / float64(l.totalTasks) * 100
	elapsed := time.Since(l.startTime)

	// Estimate remaining time
	var eta time.Duration
	if l.completedTasks > 0 {
		avgTime := elapsed / time.Duration(l.completedTasks)
		remaining := l.totalTasks - l.completedTasks
		eta = avgTime * time.Duration(remaining)
	}

	fmt.Printf("📊 Progress: %d/%d (%.1f%%) | Elapsed: %s | ETA: %s\n\n",
		l.completedTasks, l.totalTasks, percentage,
		formatDuration(elapsed), formatDuration(eta))
}

// PrintSummary prints final summary
func (l *Logger) PrintSummary() {
	l.mu.Lock()
	defer l.mu.Unlock()

	totalDuration := time.Since(l.startTime)

	var completed, failed int
	for _, task := range l.taskDetails {
		if task.Status == "completed" {
			completed++
		} else if task.Status == "failed" {
			failed++
		}
	}

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("📊 Final Summary\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
	fmt.Printf("Total Tasks: %d\n", l.totalTasks)
	fmt.Printf("✓ Completed: %d\n", completed)
	fmt.Printf("✗ Failed: %d\n", failed)
	fmt.Printf("⏱️  Total Time: %s\n", formatDuration(totalDuration))

	if completed > 0 {
		avgTime := totalDuration / time.Duration(completed)
		fmt.Printf("⚡ Avg Time/Task: %s\n", formatDuration(avgTime))
	}

	if failed > 0 {
		fmt.Printf("\n❌ Failed Tasks:\n")
		for _, task := range l.taskDetails {
			if task.Status == "failed" {
				fmt.Printf("  - %s: %s\n", task.Name, task.Error)
			}
		}
	}

	fmt.Printf("\n")
}

// formatDuration formats duration
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "N/A"
	}

	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// Info prints info
func (l *Logger) Info(format string, args ...interface{}) {
	fmt.Printf("ℹ️  "+format+"\n", args...)
}

// Warn prints warning
func (l *Logger) Warn(format string, args ...interface{}) {
	fmt.Printf("⚠️  "+format+"\n", args...)
}

// Error prints error
func (l *Logger) Error(format string, args ...interface{}) {
	fmt.Printf("❌ "+format+"\n", args...)
}
