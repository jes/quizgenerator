package quizgenerator

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// QuizGenerator orchestrates the generation and validation of quiz questions
type QuizGenerator struct {
	maker   *QuestionMaker
	checker *QuestionChecker
	pool    *QuestionPool
}

// NewQuizGenerator creates a new quiz generator
func NewQuizGenerator(apiKey string) *QuizGenerator {
	return &QuizGenerator{
		maker:   NewQuestionMaker(apiKey),
		checker: NewQuestionChecker(apiKey),
		pool:    NewQuestionPool(),
	}
}

// GenerateQuiz generates a complete quiz with the specified number of questions
func (qg *QuizGenerator) GenerateQuiz(ctx context.Context, req GenerationRequest) (*Quiz, error) {
	log.Printf("Starting quiz generation for topic: %s, target questions: %d", req.Topic, req.NumQuestions)

	acceptedQuestions := make([]*Question, 0, req.NumQuestions)
	batchSize := 5 // Generate questions in batches

	// Keep generating until we have enough accepted questions
	for len(acceptedQuestions) < req.NumQuestions {
		// Generate new questions if pool is empty
		if qg.pool.IsEmpty() {
			log.Printf("Pool is empty, generating new batch of %d questions", batchSize)
			questions, err := qg.maker.GenerateQuestions(ctx, req, batchSize)
			if err != nil {
				return nil, fmt.Errorf("failed to generate questions: %w", err)
			}

			// Add to pool
			for _, question := range questions {
				qg.pool.Add(question)
			}

			log.Printf("Added %d questions to pool", len(questions))
		}

		// Process questions from pool
		processed := qg.processPool(ctx)
		acceptedQuestions = append(acceptedQuestions, processed.accepted...)

		log.Printf("Processed %d questions: %d accepted, %d rejected, %d revised",
			len(processed.accepted)+len(processed.rejected)+len(processed.revised),
			len(processed.accepted), len(processed.rejected), len(processed.revised))

		// If we're not making progress, increase batch size
		if len(processed.accepted) == 0 && len(processed.rejected) > 0 {
			batchSize = min(batchSize+2, 10)
			log.Printf("No questions accepted, increasing batch size to %d", batchSize)
		}
	}

	// Create the final quiz
	questions := make([]Question, req.NumQuestions)
	for i, q := range acceptedQuestions[:req.NumQuestions] {
		questions[i] = *q
	}

	quiz := &Quiz{
		ID:             generateQuizID(),
		Topic:          req.Topic,
		Questions:      questions,
		CreatedAt:      time.Now(),
		TotalQuestions: req.NumQuestions,
	}

	log.Printf("Quiz generation complete: %d questions for topic '%s'", len(quiz.Questions), quiz.Topic)
	return quiz, nil
}

// processResult holds the results of processing questions from the pool
type processResult struct {
	accepted []*Question
	rejected []*Question
	revised  []*Question
}

// processPool processes all questions currently in the pool
func (qg *QuizGenerator) processPool(ctx context.Context) processResult {
	result := processResult{}

	// Process questions one by one
	for !qg.pool.IsEmpty() {
		question := qg.pool.Get()
		if question == nil {
			break
		}

		validation, err := qg.checker.CheckQuestion(ctx, question)
		if err != nil {
			log.Printf("Error checking question %s: %v", question.ID, err)
			// Put it back in pool for retry
			qg.pool.Add(question)
			continue
		}

		switch validation.Action {
		case ActionAccept:
			question.Status = StatusAccepted
			result.accepted = append(result.accepted, question)

		case ActionReject:
			question.Status = StatusRejected
			result.rejected = append(result.rejected, question)

		case ActionRevise:
			if validation.RevisedQuestion != nil {
				// Add revised question back to pool
				qg.pool.Add(validation.RevisedQuestion)
				result.revised = append(result.revised, validation.RevisedQuestion)
			} else {
				// No revision provided, reject
				question.Status = StatusRejected
				result.rejected = append(result.rejected, question)
			}
		}
	}

	return result
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
