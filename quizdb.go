package quizgenerator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB represents a quiz database connection
type DB struct {
	db *sql.DB
}

// Quiz represents a quiz in the database
type DBQuiz struct {
	ID             string    `json:"id"`
	Topic          string    `json:"topic"`
	NumQuestions   int       `json:"num_questions"`
	SourceMaterial string    `json:"source_material"`
	Difficulty     string    `json:"difficulty"`
	CreatedAt      time.Time `json:"created_at"`
	Status         string    `json:"status"` // "generating", "ready", "completed"
}

// Question represents a question in the database
type DBQuestion struct {
	ID            string `json:"id"`
	QuizID        string `json:"quiz_id"`
	QuestionNum   int    `json:"question_num"`
	Text          string `json:"text"`
	Options       string `json:"options"` // JSON array of strings
	CorrectAnswer int    `json:"correct_answer"`
	Explanation   string `json:"explanation"`
}

// OpenDB opens a new database connection
func OpenDB(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{db: db}, nil
}

// Close closes the database connection
func (db *DB) CloseDB() error {
	return db.db.Close()
}

// CreateTables creates the necessary tables if they don't exist
func (db *DB) CreateTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS quizzes (
			id TEXT PRIMARY KEY,
			topic TEXT NOT NULL,
			num_questions INTEGER NOT NULL,
			source_material TEXT,
			difficulty TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			status TEXT NOT NULL DEFAULT 'generating'
		)`,
		`CREATE TABLE IF NOT EXISTS questions (
			id TEXT PRIMARY KEY,
			quiz_id TEXT NOT NULL,
			question_num INTEGER NOT NULL,
			text TEXT NOT NULL,
			options TEXT NOT NULL,
			correct_answer INTEGER NOT NULL,
			explanation TEXT,
			FOREIGN KEY (quiz_id) REFERENCES quizzes(id)
		)`,
	}

	for _, query := range queries {
		if _, err := db.db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute %s: %w", query, err)
		}
	}
	return nil
}

// CreateQuiz creates a new quiz in the database
func (db *DB) CreateQuiz(quiz *DBQuiz) error {
	_, err := db.db.Exec(
		"INSERT INTO quizzes (id, topic, num_questions, source_material, difficulty, created_at, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		quiz.ID, quiz.Topic, quiz.NumQuestions, quiz.SourceMaterial, quiz.Difficulty, quiz.CreatedAt, quiz.Status,
	)
	if err != nil {
		return fmt.Errorf("failed to create quiz: %w", err)
	}
	return nil
}

// GetQuiz retrieves a quiz by ID
func (db *DB) GetQuiz(id string) (*DBQuiz, error) {
	var quiz DBQuiz
	err := db.db.QueryRow(
		"SELECT id, topic, num_questions, source_material, difficulty, created_at, status FROM quizzes WHERE id = ?",
		id,
	).Scan(&quiz.ID, &quiz.Topic, &quiz.NumQuestions, &quiz.SourceMaterial, &quiz.Difficulty, &quiz.CreatedAt, &quiz.Status)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("quiz not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}
	return &quiz, nil
}

// GetQuizzes retrieves all quizzes, optionally limited by count
func (db *DB) GetQuizzes(limit int) ([]DBQuiz, error) {
	query := "SELECT id, topic, num_questions, source_material, difficulty, created_at, status FROM quizzes ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get quizzes: %w", err)
	}
	defer rows.Close()

	var quizzes []DBQuiz
	for rows.Next() {
		var quiz DBQuiz
		err := rows.Scan(&quiz.ID, &quiz.Topic, &quiz.NumQuestions, &quiz.SourceMaterial, &quiz.Difficulty, &quiz.CreatedAt, &quiz.Status)
		if err != nil {
			return nil, fmt.Errorf("failed to scan quiz: %w", err)
		}
		quizzes = append(quizzes, quiz)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating quizzes: %w", err)
	}

	return quizzes, nil
}

// UpdateQuizStatus updates the status of a quiz
func (db *DB) UpdateQuizStatus(id, status string) error {
	_, err := db.db.Exec("UPDATE quizzes SET status = ? WHERE id = ?", status, id)
	if err != nil {
		return fmt.Errorf("failed to update quiz status: %w", err)
	}
	return nil
}

// CreateQuestion creates a new question in the database
func (db *DB) CreateQuestion(question *DBQuestion) error {
	_, err := db.db.Exec(
		"INSERT INTO questions (id, quiz_id, question_num, text, options, correct_answer, explanation) VALUES (?, ?, ?, ?, ?, ?, ?)",
		question.ID, question.QuizID, question.QuestionNum, question.Text, question.Options, question.CorrectAnswer, question.Explanation,
	)
	if err != nil {
		return fmt.Errorf("failed to create question: %w", err)
	}
	return nil
}

// GetQuestion retrieves a question by quiz ID and question number
func (db *DB) GetQuestion(quizID string, questionNum int) (*DBQuestion, error) {
	var question DBQuestion
	err := db.db.QueryRow(
		"SELECT id, quiz_id, question_num, text, options, correct_answer, explanation FROM questions WHERE quiz_id = ? AND question_num = ?",
		quizID, questionNum,
	).Scan(&question.ID, &question.QuizID, &question.QuestionNum, &question.Text, &question.Options, &question.CorrectAnswer, &question.Explanation)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("question not found: quiz_id=%s, question_num=%d", quizID, questionNum)
		}
		return nil, fmt.Errorf("failed to get question: %w", err)
	}
	return &question, nil
}

// GetQuestions retrieves all questions for a quiz
func (db *DB) GetQuestions(quizID string) ([]DBQuestion, error) {
	rows, err := db.db.Query(
		"SELECT id, quiz_id, question_num, text, options, correct_answer, explanation FROM questions WHERE quiz_id = ? ORDER BY question_num",
		quizID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get questions: %w", err)
	}
	defer rows.Close()

	var questions []DBQuestion
	for rows.Next() {
		var question DBQuestion
		err := rows.Scan(&question.ID, &question.QuizID, &question.QuestionNum, &question.Text, &question.Options, &question.CorrectAnswer, &question.Explanation)
		if err != nil {
			return nil, fmt.Errorf("failed to scan question: %w", err)
		}
		questions = append(questions, question)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating questions: %w", err)
	}

	return questions, nil
}

// QuestionExists checks if a question exists for a given quiz and question number
func (db *DB) QuestionExists(quizID string, questionNum int) (bool, error) {
	var exists bool
	err := db.db.QueryRow("SELECT EXISTS(SELECT 1 FROM questions WHERE quiz_id = ? AND question_num = ?)", quizID, questionNum).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check if question exists: %w", err)
	}
	return exists, nil
}

// GetQuizNumQuestions gets the number of questions for a quiz
func (db *DB) GetQuizNumQuestions(quizID string) (int, error) {
	var numQuestions int
	err := db.db.QueryRow("SELECT num_questions FROM quizzes WHERE id = ?", quizID).Scan(&numQuestions)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("quiz not found: %s", quizID)
		}
		return 0, fmt.Errorf("failed to get quiz num questions: %w", err)
	}
	return numQuestions, nil
}

// Helper function to convert options slice to JSON string
func OptionsToJSON(options []string) (string, error) {
	data, err := json.Marshal(options)
	if err != nil {
		return "", fmt.Errorf("failed to marshal options: %w", err)
	}
	return string(data), nil
}

// Helper function to convert JSON string to options slice
func JSONToOptions(optionsJSON string) ([]string, error) {
	var options []string
	err := json.Unmarshal([]byte(optionsJSON), &options)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal options: %w", err)
	}
	return options, nil
}

func (db *DB) GenerateQuiz(quizID, topic string, numQuestions int, sourceMaterial, difficulty string) {
	req := GenerationRequest{
		Topic:          topic,
		NumQuestions:   numQuestions,
		SourceMaterial: sourceMaterial,
		Difficulty:     difficulty,
	}

	// Create a new QuizGenerator instance for this quiz
	apiKey := os.Getenv("OPENAI_API_KEY")
	generator := NewQuizGenerator(apiKey)

	// Create logger with our specific quiz ID
	logger, err := NewLLMLogger(quizID, req)
	if err != nil {
		log.Printf("Failed to create logger for quiz %s: %v", quizID, err)
		// Continue without logging rather than failing
	} else {
		// Set the logger on the generator so it uses our quiz ID
		generator.SetLogger(logger)
		defer logger.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	questionChan, err := generator.GenerateQuizStream(ctx, req)
	if err != nil {
		log.Printf("Failed to generate quiz %s: %v", quizID, err)
		return
	}

	questionNum := 1
	firstQuestionGenerated := false

	for question := range questionChan {
		// Store question in database
		optionsJSON, err := OptionsToJSON(question.Options)
		if err != nil {
			log.Printf("Failed to marshal options for question %s: %v", question.ID, err)
			continue
		}

		dbQuestion := &DBQuestion{
			ID:            question.ID,
			QuizID:        quizID,
			QuestionNum:   questionNum,
			Text:          question.Text,
			Options:       optionsJSON,
			CorrectAnswer: question.CorrectAnswer,
			Explanation:   question.Explanation,
		}

		if err := db.CreateQuestion(dbQuestion); err != nil {
			log.Printf("Failed to store question %s: %v", question.ID, err)
			continue
		}

		// Mark quiz as ready as soon as the first question is generated
		if !firstQuestionGenerated {
			if err := db.UpdateQuizStatus(quizID, "ready"); err != nil {
				log.Printf("Failed to update quiz status %s: %v", quizID, err)
			} else {
				log.Printf("Quiz %s marked as ready after first question", quizID)
			}
			firstQuestionGenerated = true
		}

		questionNum++
		if questionNum > numQuestions {
			break
		}
	}

	// Mark quiz as completed when all questions are done
	if err := db.UpdateQuizStatus(quizID, "completed"); err != nil {
		log.Printf("Failed to update quiz status to completed %s: %v", quizID, err)
	}
}
