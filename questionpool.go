package quizgenerator

import (
	"sync"
	"time"
)

// QuestionPool manages a queue of tentative questions
type QuestionPool struct {
	mu        sync.RWMutex
	questions map[string]*Question
	queue     []string // FIFO queue of question IDs
}

// NewQuestionPool creates a new question pool
func NewQuestionPool() *QuestionPool {
	return &QuestionPool{
		questions: make(map[string]*Question),
		queue:     make([]string, 0),
	}
}

// Add adds a question to the pool
func (qp *QuestionPool) Add(question *Question) {
	qp.mu.Lock()
	defer qp.mu.Unlock()

	question.Status = StatusTentative
	question.CreatedAt = time.Now()

	qp.questions[question.ID] = question
	qp.queue = append(qp.queue, question.ID)
}

// Get retrieves the next question from the pool
func (qp *QuestionPool) Get() *Question {
	qp.mu.Lock()
	defer qp.mu.Unlock()

	if len(qp.queue) == 0 {
		return nil
	}

	questionID := qp.queue[0]
	qp.queue = qp.queue[1:]

	question := qp.questions[questionID]
	delete(qp.questions, questionID)

	return question
}

// Remove removes a question from the pool
func (qp *QuestionPool) Remove(questionID string) {
	qp.mu.Lock()
	defer qp.mu.Unlock()

	delete(qp.questions, questionID)

	// Remove from queue
	for i, id := range qp.queue {
		if id == questionID {
			qp.queue = append(qp.queue[:i], qp.queue[i+1:]...)
			break
		}
	}
}

// Size returns the number of questions in the pool
func (qp *QuestionPool) Size() int {
	qp.mu.RLock()
	defer qp.mu.RUnlock()
	return len(qp.queue)
}

// IsEmpty returns true if the pool is empty
func (qp *QuestionPool) IsEmpty() bool {
	return qp.Size() == 0
}

// GetAll returns all questions in the pool (for debugging/logging)
func (qp *QuestionPool) GetAll() []*Question {
	qp.mu.RLock()
	defer qp.mu.RUnlock()

	questions := make([]*Question, 0, len(qp.questions))
	for _, question := range qp.questions {
		questions = append(questions, question)
	}
	return questions
}
