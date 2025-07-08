package main

import (
	"fmt"
	"log"
	"net/http"
	"quizgenerator"
	"strconv"
	"strings"
	"time"
)

// handleMultiplayer handles all multiplayer routes
func (s *Server) handleMultiplayer(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/multiplayer/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 1 && parts[0] == "new" {
		// /multiplayer/new - create new multiplayer session
		s.handleNewMultiplayer(w, r)
		return
	}

	if len(parts) == 1 {
		// /multiplayer/{sessionID} - unified session page
		sessionID := parts[0]
		s.handleUnifiedSession(w, r, sessionID)
		return
	}

	if len(parts) == 2 && parts[1] == "answer" {
		// /multiplayer/{sessionID}/answer - submit answer
		sessionID := parts[0]
		s.handleSubmitAnswer(w, r, sessionID)
		return
	}

	if len(parts) == 2 && parts[1] == "start" {
		// /multiplayer/{sessionID}/start - start the game
		sessionID := parts[0]
		s.handleStartGame(w, r, sessionID)
		return
	}

	if len(parts) == 2 && parts[1] == "results" {
		// /multiplayer/{sessionID}/results - game results
		sessionID := parts[0]
		s.handleMultiplayerResults(w, r, sessionID)
		return
	}

	http.NotFound(w, r)
}

// handleNewMultiplayer handles creating a new multiplayer session
func (s *Server) handleNewMultiplayer(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		// Get all completed quizzes from database
		allQuizzes, err := s.db.GetQuizzes(0)
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

		// Check if quiz_id is provided in URL
		quizID := r.URL.Query().Get("quiz_id")

		err = s.templates["new_multiplayer"].ExecuteTemplate(w, "base.html", map[string]interface{}{
			"Quizzes":        completedQuizzes,
			"SelectedQuizID": quizID,
		})
		if err != nil {
			log.Printf("Template error in new_multiplayer: %v", err)
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

	quizID := r.FormValue("quiz_id")
	hostName := r.FormValue("host_name")

	if quizID == "" || hostName == "" {
		http.Error(w, "Quiz ID and host name are required", http.StatusBadRequest)
		return
	}

	// Verify quiz exists and is ready
	quiz, err := s.db.GetQuiz(quizID)
	if err != nil {
		http.Error(w, "Quiz not found", http.StatusNotFound)
		return
	}

	if quiz.Status != "completed" {
		http.Error(w, "Quiz is not ready for multiplayer", http.StatusBadRequest)
		return
	}

	// Create new multiplayer session
	sessionID := generateSessionID()
	session := &MultiplayerSession{
		ID:         sessionID,
		QuizID:     quizID,
		HostName:   hostName,
		Status:     "waiting",
		CurrentQ:   1,
		CreatedAt:  time.Now(),
		MaxPlayers: 10,
		Players:    []MultiplayerPlayer{},
		Answers:    make(map[int]map[string]int),
	}

	// Add host as first player
	hostPlayer := MultiplayerPlayer{
		ID:        generatePlayerID(),
		SessionID: sessionID,
		Name:      hostName,
		JoinedAt:  time.Now(),
		Score:     0,
		Ready:     true,
	}
	session.Players = append(session.Players, hostPlayer)

	// Store session in memory
	s.mu.Lock()
	s.multiplayerSessions[sessionID] = session
	s.mu.Unlock()

	// Redirect to unified session URL with player info
	http.Redirect(w, r, fmt.Sprintf("/multiplayer/%s?player_id=%s&player_name=%s", sessionID, hostPlayer.ID, hostName), http.StatusSeeOther)
}

// handleJoinSession handles joining an existing multiplayer session
func (s *Server) handleJoinSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	playerName := r.FormValue("player_name")
	if playerName == "" {
		http.Error(w, "Player name is required", http.StatusBadRequest)
		return
	}

	// Get session
	s.mu.RLock()
	session, exists := s.multiplayerSessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if session.Status != "waiting" {
		http.Error(w, "Game has already started", http.StatusBadRequest)
		return
	}

	// Check if name is already taken
	session.mu.Lock()
	for _, player := range session.Players {
		if player.Name == playerName {
			session.mu.Unlock()
			http.Error(w, "Name already taken", http.StatusBadRequest)
			return
		}
	}

	// Add new player
	newPlayer := MultiplayerPlayer{
		ID:        generatePlayerID(),
		SessionID: sessionID,
		Name:      playerName,
		JoinedAt:  time.Now(),
		Score:     0,
		Ready:     true,
	}
	session.Players = append(session.Players, newPlayer)
	session.mu.Unlock()

	// Redirect to unified session URL with player info
	http.Redirect(w, r, fmt.Sprintf("/multiplayer/%s?player_id=%s&player_name=%s", sessionID, newPlayer.ID, playerName), http.StatusSeeOther)
}

// handleSubmitAnswer handles a player submitting their answer
func (s *Server) handleSubmitAnswer(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get player info from form (passed from the question page)
	playerID := r.FormValue("player_id")
	playerName := r.FormValue("player_name")

	answerStr := r.FormValue("answer")
	questionNumStr := r.FormValue("question_num")

	if playerID == "" || playerName == "" || answerStr == "" || questionNumStr == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	questionNum, err := strconv.Atoi(questionNumStr)
	if err != nil {
		http.Error(w, "Invalid question number", http.StatusBadRequest)
		return
	}

	answer, err := strconv.Atoi(answerStr)
	if err != nil || answer < 0 || answer > 3 {
		http.Error(w, "Invalid answer", http.StatusBadRequest)
		return
	}

	// Get session
	s.mu.RLock()
	session, exists := s.multiplayerSessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Record the answer
	session.mu.Lock()
	if session.Answers[questionNum] == nil {
		session.Answers[questionNum] = make(map[string]int)
	}
	session.Answers[questionNum][playerID] = answer
	session.mu.Unlock()

	// Check if all players have answered
	allAnswered := s.checkAllPlayersAnswered(sessionID, questionNum)
	if allAnswered {
		// Move to next question or end game
		s.moveToNextQuestion(sessionID, questionNum)

		// Check if game is completed
		s.mu.RLock()
		session, exists := s.multiplayerSessions[sessionID]
		s.mu.RUnlock()

		if exists && session.Status == "completed" {
			http.Redirect(w, r, fmt.Sprintf("/multiplayer/%s/results", sessionID), http.StatusSeeOther)
		} else {
			// Redirect back to unified session URL
			http.Redirect(w, r, fmt.Sprintf("/multiplayer/%s?player_id=%s&player_name=%s", sessionID, playerID, playerName), http.StatusSeeOther)
		}
	} else {
		// Redirect back to unified session URL (will show waiting page)
		http.Redirect(w, r, fmt.Sprintf("/multiplayer/%s?player_id=%s&player_name=%s", sessionID, playerID, playerName), http.StatusSeeOther)
	}
}

// handleMultiplayerResults shows the final results
func (s *Server) handleMultiplayerResults(w http.ResponseWriter, _ *http.Request, sessionID string) {
	// Get session
	s.mu.RLock()
	session, exists := s.multiplayerSessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Get quiz info
	quiz, err := s.db.GetQuiz(session.QuizID)
	if err != nil {
		http.Error(w, "Quiz not found", http.StatusNotFound)
		return
	}

	// Get all questions
	dbQuestions, err := s.db.GetQuestions(session.QuizID)
	if err != nil {
		http.Error(w, "Failed to get questions", http.StatusInternalServerError)
		return
	}

	// Parse options for each question
	questionsWithOptions := make([]map[string]interface{}, len(dbQuestions))
	for i, question := range dbQuestions {
		options, err := quizgenerator.JSONToOptions(question.Options)
		if err != nil {
			log.Printf("Failed to parse options for question %d: %v", i+1, err)
			options = []string{"Error parsing options"}
		}

		questionsWithOptions[i] = map[string]interface{}{
			"Text":          question.Text,
			"Options":       options,
			"CorrectAnswer": question.CorrectAnswer,
			"Explanation":   question.Explanation,
		}
	}

	session.mu.RLock()
	players := make([]MultiplayerPlayer, len(session.Players))
	copy(players, session.Players)
	answers := make(map[int]map[string]int)
	for q, a := range session.Answers {
		answers[q] = make(map[string]int)
		for playerID, answer := range a {
			answers[q][playerID] = answer
		}
	}
	session.mu.RUnlock()

	err = s.templates["multiplayer_results"].ExecuteTemplate(w, "base.html", map[string]interface{}{
		"SessionID": sessionID,
		"Quiz":      quiz,
		"Players":   players,
		"Questions": questionsWithOptions,
		"Answers":   answers,
	})
	if err != nil {
		log.Printf("Template error in multiplayer_results: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

// handleUnifiedSession handles the main session page, showing different content based on state
func (s *Server) handleUnifiedSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	// Get session
	s.mu.RLock()
	session, exists := s.multiplayerSessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Get player info from URL
	playerID := r.URL.Query().Get("player_id")
	playerName := r.URL.Query().Get("player_name")

	// Route based on session status
	switch session.Status {
	case "waiting":
		s.handleLobbyContent(w, r, session, playerID, playerName)
	case "playing":
		s.handleQuestionContent(w, r, session, playerID, playerName)
	case "completed":
		s.handleMultiplayerResults(w, r, sessionID)
	default:
		http.Error(w, "Invalid session status", http.StatusBadRequest)
	}
}

// Helper functions

func (s *Server) checkAllPlayersAnswered(sessionID string, questionNum int) bool {
	s.mu.RLock()
	session, exists := s.multiplayerSessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		return false
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	if session.Answers[questionNum] == nil {
		return false
	}

	return len(session.Answers[questionNum]) == len(session.Players)
}

func (s *Server) moveToNextQuestion(sessionID string, currentQuestionNum int) {
	s.mu.RLock()
	session, exists := s.multiplayerSessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		return
	}

	// Get total questions
	totalQuestions, err := s.db.GetQuizActualQuestionCount(session.QuizID)
	if err != nil {
		log.Printf("Failed to get total questions: %v", err)
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// Update scores for this question
	s.updateScores(session, currentQuestionNum)

	if currentQuestionNum >= totalQuestions {
		// Game is complete
		session.Status = "completed"
	} else {
		// Move to next question
		session.CurrentQ = currentQuestionNum + 1
	}
}

func (s *Server) updateScores(session *MultiplayerSession, questionNum int) {
	// Get the question to check correct answer
	question, err := s.db.GetQuestion(session.QuizID, questionNum)
	if err != nil {
		log.Printf("Failed to get question for scoring: %v", err)
		return
	}

	// Update scores for players who answered correctly
	if answers, exists := session.Answers[questionNum]; exists {
		for playerID, answer := range answers {
			if answer == question.CorrectAnswer {
				// Find player and update score
				for i := range session.Players {
					if session.Players[i].ID == playerID {
						session.Players[i].Score++
						break
					}
				}
			}
		}
	}
}

// handleLobbyContent handles the lobby content when session is in waiting state
func (s *Server) handleLobbyContent(w http.ResponseWriter, r *http.Request, session *MultiplayerSession, playerID, playerName string) {
	// If no player info, show join form
	if playerID == "" || playerName == "" {
		if r.Method == "GET" {
			err := s.templates["join_session"].ExecuteTemplate(w, "base.html", map[string]interface{}{
				"SessionID": session.ID,
				"Quiz":      session,
			})
			if err != nil {
				log.Printf("Template error in join_session: %v", err)
				http.Error(w, "Template error", http.StatusInternalServerError)
			}
			return
		}

		if r.Method == "POST" {
			s.handleJoinSession(w, r, session.ID)
			return
		}
	}

	// Get quiz info
	quiz, err := s.db.GetQuiz(session.QuizID)
	if err != nil {
		http.Error(w, "Quiz not found", http.StatusNotFound)
		return
	}

	session.mu.RLock()
	players := make([]MultiplayerPlayer, len(session.Players))
	copy(players, session.Players)
	session.mu.RUnlock()

	err = s.templates["multiplayer_lobby"].ExecuteTemplate(w, "base.html", map[string]interface{}{
		"SessionID":  session.ID,
		"Session":    session,
		"Quiz":       quiz,
		"Players":    players,
		"PlayerID":   playerID,
		"PlayerName": playerName,
	})
	if err != nil {
		log.Printf("Template error in multiplayer_lobby: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

// handleQuestionContent handles the question content when session is in playing state
func (s *Server) handleQuestionContent(w http.ResponseWriter, r *http.Request, session *MultiplayerSession, playerID, playerName string) {
	if playerID == "" || playerName == "" {
		http.Error(w, "Player info not found in URL", http.StatusBadRequest)
		return
	}

	// Check if player has already answered current question
	session.mu.RLock()
	hasAnswered := false
	if answers, exists := session.Answers[session.CurrentQ]; exists {
		_, hasAnswered = answers[playerID]
	}
	currentQ := session.CurrentQ
	session.mu.RUnlock()

	// If player has answered, show waiting page
	if hasAnswered {
		s.handleWaitingContent(w, r, session, playerID, playerName)
		return
	}

	// Get question from database
	question, err := s.db.GetQuestion(session.QuizID, currentQ)
	if err != nil {
		http.Error(w, "Question not found", http.StatusNotFound)
		return
	}

	// Get total number of questions
	totalQuestions, err := s.db.GetQuizActualQuestionCount(session.QuizID)
	if err != nil {
		log.Printf("Failed to get total questions: %v", err)
		totalQuestions = 10
	}

	// Parse options
	options, err := quizgenerator.JSONToOptions(question.Options)
	if err != nil {
		http.Error(w, "Failed to parse question", http.StatusInternalServerError)
		return
	}

	session.mu.RLock()
	players := make([]MultiplayerPlayer, len(session.Players))
	copy(players, session.Players)
	session.mu.RUnlock()

	err = s.templates["multiplayer_question"].ExecuteTemplate(w, "base.html", map[string]interface{}{
		"SessionID":      session.ID,
		"QuestionNum":    currentQ,
		"TotalQuestions": totalQuestions,
		"Question":       question.Text,
		"Options":        options,
		"Players":        players,
		"PlayerID":       playerID,
		"PlayerName":     playerName,
	})
	if err != nil {
		log.Printf("Template error in multiplayer_question: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

// handleWaitingContent handles the waiting content when player has answered but others haven't
func (s *Server) handleWaitingContent(w http.ResponseWriter, _ *http.Request, session *MultiplayerSession, playerID, playerName string) {
	session.mu.RLock()
	players := make([]MultiplayerPlayer, len(session.Players))
	copy(players, session.Players)
	currentQ := session.CurrentQ
	session.mu.RUnlock()

	// Check which players have answered
	answeredPlayers := make(map[string]bool)
	session.mu.RLock()
	if answers, exists := session.Answers[currentQ]; exists {
		for playerID := range answers {
			answeredPlayers[playerID] = true
		}
	}
	session.mu.RUnlock()

	err := s.templates["multiplayer_waiting"].ExecuteTemplate(w, "base.html", map[string]interface{}{
		"SessionID":       session.ID,
		"QuestionNum":     currentQ,
		"Players":         players,
		"AnsweredPlayers": answeredPlayers,
		"PlayerID":        playerID,
		"PlayerName":      playerName,
	})
	if err != nil {
		log.Printf("Template error in multiplayer_waiting: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

// handleStartGame starts the multiplayer game
func (s *Server) handleStartGame(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get session
	s.mu.RLock()
	session, exists := s.multiplayerSessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.mu.Lock()
	if session.Status != "waiting" {
		session.mu.Unlock()
		http.Error(w, "Game has already started", http.StatusBadRequest)
		return
	}

	// Start the game
	now := time.Now()
	session.Status = "playing"
	session.StartedAt = &now
	session.CurrentQ = 1
	session.mu.Unlock()

	// Get player info from form
	playerID := r.FormValue("player_id")
	playerName := r.FormValue("player_name")

	// Redirect to unified session URL
	http.Redirect(w, r, fmt.Sprintf("/multiplayer/%s?player_id=%s&player_name=%s", sessionID, playerID, playerName), http.StatusSeeOther)
}
