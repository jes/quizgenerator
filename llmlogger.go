package quizgenerator

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LLMLogger handles logging of all LLM interactions
type LLMLogger struct {
	file   *os.File
	mu     sync.Mutex
	quizID string
}

// NewLLMLogger creates a new LLM logger for a specific quiz
func NewLLMLogger(quizID string, req GenerationRequest) (*LLMLogger, error) {
	// Ensure log directory exists
	if err := os.MkdirAll("log", 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create log file
	filename := filepath.Join("log", fmt.Sprintf("%s.log", quizID))
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	logger := &LLMLogger{
		file:   file,
		quizID: quizID,
	}

	// Write header with quiz parameters
	logger.Logf("=== Quiz Generation Log ===\n")
	logger.Logf("Quiz ID: %s\n", quizID)
	logger.Logf("Topic: %s\n", req.Topic)
	logger.Logf("Number of Questions: %d\n", req.NumQuestions)
	logger.Logf("Difficulty: %s\n", req.Difficulty)
	if req.SourceMaterial != "" {
		logger.Logf("Source Material Length: %d characters\n", len(req.SourceMaterial))
	}
	logger.Logf("Started: %s\n", time.Now().Format(time.RFC3339))
	logger.Logf("========================\n\n")

	return logger, nil
}

// Logf writes a formatted log entry with timestamp
func (ll *LLMLogger) Logf(format string, args ...interface{}) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	timestamp := time.Now().Format("15:04:05.000")
	message := fmt.Sprintf(format, args...)

	// Write to file
	fmt.Fprintf(ll.file, "[%s] %s", timestamp, message)

	// Also flush to ensure it's written immediately
	ll.file.Sync()
}

// LogLLMRequest logs an LLM request
func (ll *LLMLogger) LogLLMRequest(module, prompt string) {
	ll.Logf("=== LLM REQUEST (%s) ===\n", module)
	ll.Logf("Prompt:\n%s\n", prompt)
	ll.Logf("=====================\n\n")
}

// LogLLMResponse logs an LLM response
func (ll *LLMLogger) LogLLMResponse(module, response string) {
	ll.Logf("=== LLM RESPONSE (%s) ===\n", module)
	ll.Logf("Response:\n%s\n", response)
	ll.Logf("======================\n\n")
}

// LogQuestionResult logs the result of processing a question
func (ll *LLMLogger) LogQuestionResult(questionID, action, reason string) {
	ll.Logf("Question %s: %s - %s\n", questionID, action, reason)
}

// LogDedupResult logs the result of deduplication
func (ll *LLMLogger) LogDedupResult(questionID string, isDuplicate bool, reason, duplicateID string) {
	if isDuplicate {
		ll.Logf("Question %s: DUPLICATE of %s - %s\n", questionID, duplicateID, reason)
	} else {
		ll.Logf("Question %s: UNIQUE - %s\n", questionID, reason)
	}
}

// Close closes the log file
func (ll *LLMLogger) Close() error {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	if ll.file != nil {
		ll.Logf("=== Quiz Generation Complete ===\n")
		ll.Logf("Completed: %s\n", time.Now().Format(time.RFC3339))
		ll.Logf("=============================\n")
		return ll.file.Close()
	}
	return nil
}
