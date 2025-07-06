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
	logger  *LLMLogger
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

// SetLogger sets the logger for this quiz generator
func (qg *QuizGenerator) SetLogger(logger *LLMLogger) {
	qg.logger = logger
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
		qg.logger = logger
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

	// Create logger for this quiz if not already set
	if qg.logger == nil {
		quizID := generateQuizID()
		logger, err := NewLLMLogger(quizID, req)
		if err != nil {
			VerboseLog("Failed to create logger: %v", err)
			// Continue without logging rather than failing
		} else {
			qg.logger = logger
		}
	}

	go func() {
		defer close(questionChan)
		if qg.logger != nil {
			defer qg.logger.Close()
		}

		acceptedCount := 0
		batchSize := 5

		for acceptedCount < req.NumQuestions {
			// Generate new questions if pool is empty
			if qg.pool.IsEmpty() {
				VerboseLog("Pool is empty, generating new batch of %d questions", batchSize)
				questions, err := qg.maker.GenerateQuestions(ctx, req, batchSize, qg.logger)
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
				validation, err := qg.checker.CheckQuestion(ctx, question, qg.logger)
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
				dedupResult, err := qg.dedup.CheckDuplicate(ctx, question, qg.logger)
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

				// Randomize answer order to avoid position bias
				qg.randomizeAnswerOrder(question)

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

// randomizeAnswerOrder randomizes the order of answer options while preserving the correct answer
func (qg *QuizGenerator) randomizeAnswerOrder(question *Question) {
	if len(question.Options) != 4 {
		// Don't randomize if we don't have exactly 4 options
		return
	}

	// Create a permutation of indices [0,1,2,3]
	indices := []int{0, 1, 2, 3}
	for i := len(indices) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		indices[i], indices[j] = indices[j], indices[i]
	}

	// Create new options and find new correct answer index
	newOptions := make([]string, 4)
	var newCorrectAnswer int

	for i, oldIndex := range indices {
		newOptions[i] = question.Options[oldIndex]
		if oldIndex == question.CorrectAnswer {
			newCorrectAnswer = i
		}
	}

	// Update the question
	question.Options = newOptions
	question.CorrectAnswer = newCorrectAnswer
}
