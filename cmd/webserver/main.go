package main

import (
	"encoding/gob"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"quizgenerator"

	"github.com/gorilla/sessions"
)

type Server struct {
	db        *quizgenerator.DB
	store     *sessions.CookieStore
	templates map[string]*template.Template
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
	db, err := quizgenerator.OpenDB("./quiz.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.CloseDB()

	// Create tables
	if err := db.CreateTables(); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	// Initialize session store
	store := sessions.NewCookieStore([]byte("your-secret-key-here"))

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

	server := &Server{
		db:        db,
		store:     store,
		templates: templates,
	}

	// Setup routes
	http.HandleFunc("/", server.handleHome)
	http.HandleFunc("/quiz/new", server.handleNewQuiz)
	http.HandleFunc("/quiz/", server.handleQuiz)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8180"
	}

	log.Printf("Starting server on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Get all quizzes from database
	allQuizzes, err := s.db.GetQuizzes(0) // Get all quizzes
	if err != nil {
		log.Printf("Failed to get quizzes: %v", err)
		http.Error(w, "Failed to get quizzes", http.StatusInternalServerError)
		return
	}

	// Filter to only show completed quizzes
	var completedQuizzes []quizgenerator.DBQuiz
	for _, quiz := range allQuizzes {
		if quiz.Status == "completed" {
			completedQuizzes = append(completedQuizzes, quiz)
		}
	}

	err = s.templates["home"].ExecuteTemplate(w, "base.html", map[string]interface{}{
		"Quizzes": completedQuizzes,
	})
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
	quiz := &quizgenerator.DBQuiz{
		ID:             quizID,
		Topic:          topic,
		NumQuestions:   numQuestions,
		SourceMaterial: sourceMaterial,
		Difficulty:     difficulty,
		CreatedAt:      time.Now(),
		Status:         "generating",
	}

	if err := s.db.CreateQuiz(quiz); err != nil {
		http.Error(w, "Failed to create quiz", http.StatusInternalServerError)
		return
	}

	// Start generating in background
	go s.db.GenerateQuiz(quizID, topic, numQuestions, sourceMaterial, difficulty)

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
	quiz, err := s.db.GetQuiz(quizID)
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

	// Check if this specific question exists
	questionExists, err := s.db.QuestionExists(quizID, questionNum)
	if err != nil {
		log.Printf("Failed to check if question exists: %v", err)
		http.Error(w, "Failed to check question", http.StatusInternalServerError)
		return
	}

	if !questionExists {
		// Check if the quiz is still generating or if we've reached the end
		quiz, err := s.db.GetQuiz(quizID)
		if err != nil {
			log.Printf("Failed to get quiz: %v", err)
			http.Error(w, "Failed to get quiz", http.StatusInternalServerError)
			return
		}

		// If quiz is still generating, show generating page
		if quiz.Status == "generating" || quiz.Status == "ready" {
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

		// If quiz is completed but this question doesn't exist, redirect to results
		// This handles the case where we truncated the quiz
		log.Printf("Question %d for quiz %s doesn't exist, quiz is completed, redirecting to results", questionNum, quizID)
		gameSession.Completed = true
		session.Values["game"] = gameSession
		session.Save(r, w)
		http.Redirect(w, r, fmt.Sprintf("/quiz/%s/results", quizID), http.StatusSeeOther)
		return
	}

	// Get question from database
	question, err := s.db.GetQuestion(quizID, questionNum)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Parse options
	options, err := quizgenerator.JSONToOptions(question.Options)
	if err != nil {
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

	// Check if quiz is complete using actual number of questions
	actualQuestions, err := s.db.GetQuizActualQuestionCount(quizID)
	if err != nil {
		http.Error(w, "Failed to get quiz info", http.StatusInternalServerError)
		return
	}

	if questionNum >= actualQuestions {
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
	quiz, err := s.db.GetQuiz(quizID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Get all questions
	dbQuestions, err := s.db.GetQuestions(quizID)
	if err != nil {
		http.Error(w, "Failed to get questions", http.StatusInternalServerError)
		return
	}

	var questions []struct {
		QuestionNum   int
		Text          string
		Options       []string
		CorrectAnswer int
		Explanation   string
	}

	for _, q := range dbQuestions {
		// Parse options
		options, err := quizgenerator.JSONToOptions(q.Options)
		if err != nil {
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

func generateQuizID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
