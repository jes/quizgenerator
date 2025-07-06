package main

import (
	"context"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"quizgenerator"

	"github.com/gorilla/sessions"
	_ "github.com/mattn/go-sqlite3"
)

type Server struct {
	db           *sql.DB
	store        *sessions.CookieStore
	generator    *quizgenerator.QuizGenerator
	generating   map[string]bool
	generatingMu sync.RWMutex
	templates    map[string]*template.Template
}

type Quiz struct {
	ID             string    `json:"id"`
	Topic          string    `json:"topic"`
	NumQuestions   int       `json:"num_questions"`
	SourceMaterial string    `json:"source_material"`
	Difficulty     string    `json:"difficulty"`
	CreatedAt      time.Time `json:"created_at"`
	Status         string    `json:"status"` // "generating", "ready", "completed"
}

type GameSession struct {
	QuizID    string   `json:"quiz_id"`
	Players   []Player `json:"players"`
	CurrentQ  int      `json:"current_q"`
	Answers   [][]int  `json:"answers"` // [question][player] -> answer
	Scores    []int    `json:"scores"`
	Completed bool     `json:"completed"`
}

type Player struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
}

func init() {
	gob.Register(GameSession{})
	gob.Register(Player{})
}

func main() {
	quizgenerator.SetVerbose(true)
	// Get API key from environment
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Initialize database
	db, err := sql.Open("sqlite3", "./quiz.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create tables
	if err := createTables(db); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	// Initialize session store
	store := sessions.NewCookieStore([]byte("your-secret-key-here"))

	// Initialize quiz generator
	generator := quizgenerator.NewQuizGenerator(apiKey)

	// Load templates with custom functions
	funcMap := template.FuncMap{
		"list": func(items ...interface{}) []interface{} {
			return items
		},
		"index": func(slice interface{}, i int) interface{} {
			switch v := slice.(type) {
			case []interface{}:
				return v[i]
			case []int:
				return v[i]
			case []string:
				return v[i]
			case [][]int:
				return v[i]
			default:
				log.Printf("Warning: index function called with unsupported type: %T", slice)
				return nil
			}
		},
		"add": func(a, b int) int {
			return a + b
		},
		"mul": func(a, b float64) float64 {
			return a * b
		},
		"div": func(a, b int) float64 {
			return float64(a) / float64(b)
		},
		"printf": fmt.Sprintf,
		"default": func(value, defaultValue interface{}) interface{} {
			if value == nil || value == "" {
				return defaultValue
			}
			return value
		},
	}

	// Create template map
	templates := make(map[string]*template.Template)

	// Load each template with base.html
	templateFiles := []struct {
		name string
		file string
	}{
		{"home", "templates/home.html"},
		{"new_quiz", "templates/new_quiz.html"},
		{"quiz_setup", "templates/quiz_setup.html"},
		{"question", "templates/question.html"},
		{"generating", "templates/generating.html"},
		{"results", "templates/results.html"},
	}

	for _, tmpl := range templateFiles {
		templates[tmpl.name] = template.Must(template.New(tmpl.name).Funcs(funcMap).ParseFiles("templates/base.html", tmpl.file))
	}

	// Debug: list all loaded templates
	log.Printf("Loaded templates:")
	for _, tmpl := range templates {
		log.Printf("  - %s", tmpl.Name())
	}

	server := &Server{
		db:         db,
		store:      store,
		generator:  generator,
		generating: make(map[string]bool),
		templates:  templates,
	}

	// Setup routes
	http.HandleFunc("/", server.handleHome)
	http.HandleFunc("/quiz/new", server.handleNewQuiz)
	http.HandleFunc("/quiz/", server.handleQuiz)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func createTables(db *sql.DB) error {
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
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute %s: %w", query, err)
		}
	}
	return nil
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	err := s.templates["home"].ExecuteTemplate(w, "base.html", nil)
	if err != nil {
		log.Printf("Template error in home: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleNewQuiz(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		err := s.templates["new_quiz"].ExecuteTemplate(w, "base.html", nil)
		if err != nil {
			log.Printf("Template error in new_quiz: %v", err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	topic := r.FormValue("topic")
	numQuestionsStr := r.FormValue("num_questions")
	sourceMaterial := r.FormValue("source_material")
	difficulty := r.FormValue("difficulty")

	if topic == "" {
		http.Error(w, "Topic is required", http.StatusBadRequest)
		return
	}

	numQuestions, err := strconv.Atoi(numQuestionsStr)
	if err != nil || numQuestions <= 0 {
		numQuestions = 10
	}

	// Create quiz in database
	quizID := generateQuizID()
	_, err = s.db.Exec(
		"INSERT INTO quizzes (id, topic, num_questions, source_material, difficulty, created_at, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		quizID, topic, numQuestions, sourceMaterial, difficulty, time.Now(), "generating",
	)
	if err != nil {
		http.Error(w, "Failed to create quiz", http.StatusInternalServerError)
		return
	}

	// Start generating in background
	go s.generateQuiz(quizID, topic, numQuestions, sourceMaterial, difficulty)

	// Redirect to quiz page
	http.Redirect(w, r, "/quiz/"+quizID, http.StatusSeeOther)
}

func (s *Server) handleQuiz(w http.ResponseWriter, r *http.Request) {
	log.Printf("Handling quiz request: %v", r.URL.Path)
	path := strings.TrimPrefix(r.URL.Path, "/quiz/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}

	quizID := parts[0]

	if len(parts) == 1 {
		log.Printf("Handling quiz setup request: %v", r.URL.Path)
		// /quiz/{id} - quiz setup page
		s.handleQuizSetup(w, r, quizID)
		return
	}

	if len(parts) == 2 {
		log.Printf("Handling question request: %v", r.URL.Path)
		if parts[1] == "results" {
			log.Printf("Handling results request: %v", r.URL.Path)
			// /quiz/{id}/results - results page
			s.handleResults(w, r, quizID)
			return
		}

		// /quiz/{id}/{num} - question page
		questionNum, err := strconv.Atoi(parts[1])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.handleQuestion(w, r, quizID, questionNum)
		return
	}

	log.Printf("Handling unknown request: %v", r.URL.Path)

	http.NotFound(w, r)
}

func (s *Server) handleQuizSetup(w http.ResponseWriter, r *http.Request, quizID string) {
	log.Printf("Handling quiz setup request: %v", r.URL.Path)
	// Get quiz info
	var quiz Quiz
	err := s.db.QueryRow(
		"SELECT id, topic, num_questions, source_material, difficulty, created_at, status FROM quizzes WHERE id = ?",
		quizID,
	).Scan(&quiz.ID, &quiz.Topic, &quiz.NumQuestions, &quiz.SourceMaterial, &quiz.Difficulty, &quiz.CreatedAt, &quiz.Status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == "GET" {
		log.Printf("Handling quiz setup request: %v", r.URL.Path)
		log.Printf("Quiz data: %+v", quiz)
		err := s.templates["quiz_setup"].ExecuteTemplate(w, "base.html", quiz)
		if err != nil {
			log.Printf("Template execution error: %v", err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	numPlayersStr := r.FormValue("num_players")
	numPlayers, err := strconv.Atoi(numPlayersStr)
	if err != nil || numPlayers <= 0 {
		numPlayers = 1
	}

	// Get player names
	var players []Player
	for i := 1; i <= numPlayers; i++ {
		name := r.FormValue(fmt.Sprintf("player_%d", i))
		if name == "" {
			name = fmt.Sprintf("Player %d", i)
		}
		players = append(players, Player{Name: name, Score: 0})
	}

	// Create game session
	session, _ := s.store.Get(r, "quiz-session")
	gameSession := GameSession{
		QuizID:    quizID,
		Players:   players,
		CurrentQ:  1,
		Answers:   make([][]int, quiz.NumQuestions),
		Scores:    make([]int, len(players)),
		Completed: false,
	}

	// Initialize answers array
	for i := range gameSession.Answers {
		gameSession.Answers[i] = make([]int, len(players))
	}

	session.Values["game"] = gameSession
	err = session.Save(r, w)
	if err != nil {
		log.Printf("Session save error: %v", err)
	}

	// Redirect to first question
	http.Redirect(w, r, fmt.Sprintf("/quiz/%s/1", quizID), http.StatusSeeOther)
}

func (s *Server) handleQuestion(w http.ResponseWriter, r *http.Request, quizID string, questionNum int) {
	// Get game session
	session, _ := s.store.Get(r, "quiz-session")
	gameInterface := session.Values["game"]
	if gameInterface == nil {
		http.Redirect(w, r, "/quiz/"+quizID, http.StatusSeeOther)
		return
	}

	gameSession := gameInterface.(GameSession)
	if gameSession.QuizID != quizID {
		http.Redirect(w, r, "/quiz/"+quizID, http.StatusSeeOther)
		return
	}

	// Check if quiz is ready
	var status string
	err := s.db.QueryRow("SELECT status FROM quizzes WHERE id = ?", quizID).Scan(&status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if status == "generating" {
		// Show generating page with auto-refresh
		err := s.templates["generating"].ExecuteTemplate(w, "base.html", map[string]interface{}{
			"QuizID":      quizID,
			"QuestionNum": questionNum,
		})
		if err != nil {
			log.Printf("Template error in generating: %v", err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		return
	}

	// Get question from database
	var question struct {
		ID            string
		Text          string
		Options       string
		CorrectAnswer int
		Explanation   string
	}

	err = s.db.QueryRow(
		"SELECT id, text, options, correct_answer, explanation FROM questions WHERE quiz_id = ? AND question_num = ?",
		quizID, questionNum,
	).Scan(&question.ID, &question.Text, &question.Options, &question.CorrectAnswer, &question.Explanation)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Parse options
	var options []string
	if err := json.Unmarshal([]byte(question.Options), &options); err != nil {
		http.Error(w, "Failed to parse question", http.StatusInternalServerError)
		return
	}

	if r.Method == "GET" {
		err := s.templates["question"].ExecuteTemplate(w, "base.html", map[string]interface{}{
			"QuizID":      quizID,
			"QuestionNum": questionNum,
			"Question":    question.Text,
			"Options":     options,
			"Players":     gameSession.Players,
		})
		if err != nil {
			log.Printf("Template error in question: %v", err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get answers from all players
	for i := range gameSession.Players {
		answerStr := r.FormValue(fmt.Sprintf("player_%d", i))
		if answerStr == "" {
			http.Error(w, "All players must answer", http.StatusBadRequest)
			return
		}
		answer, err := strconv.Atoi(answerStr)
		if err != nil || answer < 0 || answer > 3 {
			http.Error(w, "Invalid answer", http.StatusBadRequest)
			return
		}
		gameSession.Answers[questionNum-1][i] = answer
	}

	// Update scores
	for i, answer := range gameSession.Answers[questionNum-1] {
		if answer == question.CorrectAnswer {
			gameSession.Scores[i]++
		}
	}

	// Check if quiz is complete
	var quiz Quiz
	err = s.db.QueryRow("SELECT num_questions FROM quizzes WHERE id = ?", quizID).Scan(&quiz.NumQuestions)
	if err != nil {
		http.Error(w, "Failed to get quiz info", http.StatusInternalServerError)
		return
	}

	if questionNum >= quiz.NumQuestions {
		gameSession.Completed = true
		session.Values["game"] = gameSession
		session.Save(r, w)
		http.Redirect(w, r, fmt.Sprintf("/quiz/%s/results", quizID), http.StatusSeeOther)
		return
	}

	// Move to next question
	gameSession.CurrentQ = questionNum + 1
	session.Values["game"] = gameSession
	session.Save(r, w)

	http.Redirect(w, r, fmt.Sprintf("/quiz/%s/%d", quizID, questionNum+1), http.StatusSeeOther)
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request, quizID string) {
	// Get game session
	session, _ := s.store.Get(r, "quiz-session")
	gameInterface := session.Values["game"]
	if gameInterface == nil {
		http.Redirect(w, r, "/quiz/"+quizID, http.StatusSeeOther)
		return
	}

	gameSession := gameInterface.(GameSession)
	if gameSession.QuizID != quizID {
		http.Redirect(w, r, "/quiz/"+quizID, http.StatusSeeOther)
		return
	}

	// Get quiz info
	var quiz Quiz
	err := s.db.QueryRow(
		"SELECT id, topic, num_questions, source_material, difficulty, created_at, status FROM quizzes WHERE id = ?",
		quizID,
	).Scan(&quiz.ID, &quiz.Topic, &quiz.NumQuestions, &quiz.SourceMaterial, &quiz.Difficulty, &quiz.CreatedAt, &quiz.Status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Get all questions
	rows, err := s.db.Query(
		"SELECT question_num, text, options, correct_answer, explanation FROM questions WHERE quiz_id = ? ORDER BY question_num",
		quizID,
	)
	if err != nil {
		http.Error(w, "Failed to get questions", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var questions []struct {
		QuestionNum   int
		Text          string
		Options       []string
		CorrectAnswer int
		Explanation   string
	}

	for rows.Next() {
		var q struct {
			QuestionNum   int
			Text          string
			Options       string
			CorrectAnswer int
			Explanation   string
		}
		err := rows.Scan(&q.QuestionNum, &q.Text, &q.Options, &q.CorrectAnswer, &q.Explanation)
		if err != nil {
			continue
		}

		// Parse options
		var options []string
		if err := json.Unmarshal([]byte(q.Options), &options); err != nil {
			continue
		}

		questions = append(questions, struct {
			QuestionNum   int
			Text          string
			Options       []string
			CorrectAnswer int
			Explanation   string
		}{
			QuestionNum:   q.QuestionNum,
			Text:          q.Text,
			Options:       options,
			CorrectAnswer: q.CorrectAnswer,
			Explanation:   q.Explanation,
		})
	}

	err = s.templates["results"].ExecuteTemplate(w, "base.html", map[string]interface{}{
		"Quiz":      quiz,
		"Game":      gameSession,
		"Questions": questions,
	})
	if err != nil {
		log.Printf("Template error in results: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) generateQuiz(quizID, topic string, numQuestions int, sourceMaterial, difficulty string) {
	s.generatingMu.Lock()
	s.generating[quizID] = true
	s.generatingMu.Unlock()

	defer func() {
		s.generatingMu.Lock()
		delete(s.generating, quizID)
		s.generatingMu.Unlock()
	}()

	req := quizgenerator.GenerationRequest{
		Topic:          topic,
		NumQuestions:   numQuestions,
		SourceMaterial: sourceMaterial,
		Difficulty:     difficulty,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	questionChan, err := s.generator.GenerateQuizStream(ctx, req)
	if err != nil {
		log.Printf("Failed to generate quiz %s: %v", quizID, err)
		return
	}

	questionNum := 1
	for question := range questionChan {
		// Store question in database
		optionsJSON, _ := json.Marshal(question.Options)
		_, err := s.db.Exec(
			"INSERT INTO questions (id, quiz_id, question_num, text, options, correct_answer, explanation) VALUES (?, ?, ?, ?, ?, ?, ?)",
			question.ID, quizID, questionNum, question.Text, string(optionsJSON), question.CorrectAnswer, question.Explanation,
		)
		if err != nil {
			log.Printf("Failed to store question %s: %v", question.ID, err)
			continue
		}

		questionNum++
		if questionNum > numQuestions {
			break
		}
	}

	// Mark quiz as ready
	_, err = s.db.Exec("UPDATE quizzes SET status = 'ready' WHERE id = ?", quizID)
	if err != nil {
		log.Printf("Failed to update quiz status %s: %v", quizID, err)
	}
}

func generateQuizID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
