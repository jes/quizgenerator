package quizgenerator

import "time"

// Question represents a single quiz question with multiple choice answers
type Question struct {
	ID            string         `json:"id"`
	Text          string         `json:"text"`
	Options       []string       `json:"options"`
	CorrectAnswer int            `json:"correct_answer"` // 0-based index
	Explanation   string         `json:"explanation"`
	Topic         string         `json:"topic"`
	CreatedAt     time.Time      `json:"created_at"`
	Status        QuestionStatus `json:"status"`
	RevisionCount int            `json:"revision_count"` // Number of times this question has been revised
}

// QuestionStatus represents the state of a question in the pipeline
type QuestionStatus string

const (
	StatusTentative QuestionStatus = "tentative"
	StatusAccepted  QuestionStatus = "accepted"
	StatusRejected  QuestionStatus = "rejected"
	StatusRevised   QuestionStatus = "revised"
)

// Quiz represents a complete quiz with metadata
type Quiz struct {
	ID             string     `json:"id"`
	Topic          string     `json:"topic"`
	Questions      []Question `json:"questions"`
	CreatedAt      time.Time  `json:"created_at"`
	TotalQuestions int        `json:"total_questions"`
}

// ValidationResult represents the result of checking a question
type ValidationResult struct {
	QuestionID      string           `json:"question_id"`
	Reason          string           `json:"reason"`
	Action          ValidationAction `json:"action"`
	RevisedQuestion *Question        `json:"revised_question,omitempty"`
}

// ValidationAction represents what the validator decided to do
type ValidationAction string

const (
	ActionAccept ValidationAction = "accept"
	ActionReject ValidationAction = "reject"
	ActionRevise ValidationAction = "revise"
)

// GenerationRequest represents a request to generate questions
type GenerationRequest struct {
	Topic          string `json:"topic"`
	NumQuestions   int    `json:"num_questions"`
	SourceMaterial string `json:"source_material,omitempty"`
	Difficulty     string `json:"difficulty,omitempty"`
}
