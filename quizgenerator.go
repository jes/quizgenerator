package quizgenerator

import (
	"context"
	"math/rand"
	"time"
)

// QuizGenerator orchestrates the generation and validation of quiz questions
type QuizGenerator struct {
	maker   *QuestionMaker
	checker *QuestionChecker
	dedup   *QuestionDedup
	pool    *QuestionPool
}

// NewQuizGenerator creates a new quiz generator
func NewQuizGenerator(apiKey string) *QuizGenerator {
	return &QuizGenerator{
		maker:   NewQuestionMaker(apiKey),
		checker: NewQuestionChecker(apiKey),
		dedup:   NewQuestionDedup(apiKey),
		pool:    NewQuestionPool(),
	}
}

// GenerateQuiz generates a complete quiz with the specified number of questions
func (qg *QuizGenerator) GenerateQuiz(ctx context.Context, req GenerationRequest) (*Quiz, error) {
	VerboseLog("Starting quiz generation for topic: %s, target questions: %d", req.Topic, req.NumQuestions)

	// Create logger for this quiz
	quizID := generateQuizID()
	logger, err := NewLLMLogger(quizID, req)
	if err != nil {
		VerboseLog("Failed to create logger: %v", err)
		// Continue without logging rather than failing
	} else {
		SetGlobalLogger(logger)
		defer logger.Close()
	}

	// Use the streaming version to collect all questions
	questionChan, err := qg.GenerateQuizStream(ctx, req)
	if err != nil {
		return nil, err
	}

	// Collect all questions from the stream
	acceptedQuestions := make([]*Question, 0, req.NumQuestions)
	for question := range questionChan {
		acceptedQuestions = append(acceptedQuestions, question)
	}

	// Create the final quiz
	questions := make([]Question, req.NumQuestions)
	for i, q := range acceptedQuestions[:req.NumQuestions] {
		questions[i] = *q
	}

	quiz := &Quiz{
		ID:             quizID,
		Topic:          req.Topic,
		Questions:      questions,
		CreatedAt:      time.Now(),
		TotalQuestions: req.NumQuestions,
	}

	VerboseLog("Quiz generation complete: %d questions for topic '%s'", len(quiz.Questions), quiz.Topic)
	return quiz, nil
}

// GenerateQuizStream generates questions and yields them as they become available
func (qg *QuizGenerator) GenerateQuizStream(ctx context.Context, req GenerationRequest) (<-chan *Question, error) {
	questionChan := make(chan *Question, req.NumQuestions)

	// Create logger for this quiz
	quizID := generateQuizID()
	logger, err := NewLLMLogger(quizID, req)
	if err != nil {
		VerboseLog("Failed to create logger: %v", err)
		// Continue without logging rather than failing
	} else {
		SetGlobalLogger(logger)
	}

	go func() {
		defer close(questionChan)
		if logger != nil {
			defer logger.Close()
		}

		acceptedCount := 0
		batchSize := 5

		for acceptedCount < req.NumQuestions {
			// Generate new questions if pool is empty
			if qg.pool.IsEmpty() {
				VerboseLog("Pool is empty, generating new batch of %d questions", batchSize)
				questions, err := qg.maker.GenerateQuestions(ctx, req, batchSize)
				if err != nil {
					VerboseLog("Failed to generate questions: %v", err)
					return
				}

				// Add to pool
				for _, question := range questions {
					qg.pool.Add(question)
				}

				VerboseLog("Added %d questions to pool", len(questions))
			}

			// Process one question at a time and yield accepted ones
			if !qg.pool.IsEmpty() {
				question := qg.pool.Get()
				if question == nil {
					continue
				}

				// Step 1: Validate the question
				validation, err := qg.checker.CheckQuestion(ctx, question)
				if err != nil {
					VerboseLog("Error checking question %s: %v", question.ID, err)
					// Put it back in pool for retry
					qg.pool.Add(question)
					continue
				}

				// If validation failed, skip to next question
				if validation.Action != ActionAccept {
					if validation.Action == ActionRevise && validation.RevisedQuestion != nil {
						// Add revised question back to pool
						qg.pool.Add(validation.RevisedQuestion)
					}
					continue
				}

				// Step 2: Check for duplicates
				dedupResult, err := qg.dedup.CheckDuplicate(ctx, question)
				if err != nil {
					VerboseLog("Error checking duplicate for question %s: %v", question.ID, err)
					// Put it back in pool for retry
					qg.pool.Add(question)
					continue
				}

				// If it's a duplicate, skip this question
				if dedupResult.IsDuplicate {
					VerboseLog("Question %s rejected as duplicate of %s: %s",
						question.ID, dedupResult.DuplicateID, dedupResult.Reason)
					continue
				}

				// Question passed both validation and deduplication
				question.Status = StatusAccepted
				select {
				case questionChan <- question:
					acceptedCount++
				case <-ctx.Done():
					return
				}
			}

			// If we're not making progress, increase batch size
			if acceptedCount == 0 {
				batchSize = min(batchSize+2, 10)
				VerboseLog("No questions accepted, increasing batch size to %d", batchSize)
			}
		}
	}()

	return questionChan, nil
}

func generateQuizID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
