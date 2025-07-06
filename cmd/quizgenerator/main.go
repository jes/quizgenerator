package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"quizgenerator"
)

func main() {
	var (
		topic          = flag.String("topic", "", "Quiz topic (required)")
		numQuestions   = flag.Int("questions", 10, "Number of questions to generate")
		sourceMaterial = flag.String("source", "", "Source material to base questions on")
		difficulty     = flag.String("difficulty", "medium", "Difficulty level (easy, medium, hard)")
		outputFile     = flag.String("output", "", "Output file for quiz JSON (default: stdout)")
		apiKey         = flag.String("api-key", "", "OpenAI API key (or set OPENAI_API_KEY env var)")
		playMode       = flag.Bool("play", false, "Play the quiz interactively")
		numPlayers     = flag.Int("players", 1, "Number of players for multiplayer mode")
		verbose        = flag.Bool("verbose", false, "Enable verbose debugging output")
	)

	flag.Parse()

	quizgenerator.SetVerbose(*verbose)

	if *topic == "" {
		log.Fatal("Topic is required. Use -topic flag.")
	}

	// Get API key from flag or environment
	if *apiKey == "" {
		*apiKey = os.Getenv("OPENAI_API_KEY")
		if *apiKey == "" {
			log.Fatal("OpenAI API key is required. Use -api-key flag or set OPENAI_API_KEY environment variable.")
		}
	}

	// Create quiz generator
	generator := quizgenerator.NewQuizGenerator(*apiKey)

	// Create generation request
	req := quizgenerator.GenerationRequest{
		Topic:          *topic,
		NumQuestions:   *numQuestions,
		SourceMaterial: *sourceMaterial,
		Difficulty:     *difficulty,
	}

	if *playMode {
		playQuiz(generator, req, *numPlayers)
		return
	}

	if *verbose {
		log.Printf("Starting quiz generation for topic: %s", *topic)
		log.Printf("Target questions: %d, Difficulty: %s", *numQuestions, *difficulty)
		if *sourceMaterial != "" {
			log.Printf("Using source material: %d characters", len(*sourceMaterial))
		}
	}

	// Generate quiz with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	quiz, err := generator.GenerateQuiz(ctx, req)
	if err != nil {
		log.Fatalf("Failed to generate quiz: %v", err)
	}

	// Output the quiz
	output, err := json.MarshalIndent(quiz, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal quiz: %v", err)
	}

	if *outputFile != "" {
		err = os.WriteFile(*outputFile, output, 0644)
		if err != nil {
			log.Fatalf("Failed to write output file: %v", err)
		}
		log.Printf("Quiz saved to: %s", *outputFile)
	} else {
		fmt.Println(string(output))
	}

	if *verbose {
		log.Printf("Quiz generation completed successfully!")
	}
}

// Player represents a player in the multiplayer quiz
type Player struct {
	Name    string
	Score   int
	Answers []int // Track answers for each question (0-3 for A-D)
}

func playQuiz(generator *quizgenerator.QuizGenerator, req quizgenerator.GenerationRequest, numPlayers int) {
	fmt.Printf("🎯 Starting interactive quiz on: %s\n", req.Topic)
	fmt.Printf("📝 Questions: %d, Difficulty: %s\n", req.NumQuestions, req.Difficulty)
	fmt.Printf("👥 Players: %d\n", numPlayers)
	if req.SourceMaterial != "" {
		fmt.Printf("📚 Using source material: %d characters\n", len(req.SourceMaterial))
	}
	fmt.Println("⏳ Generating questions... (this may take a moment)")
	fmt.Println()

	// Initialize players
	players := make([]*Player, numPlayers)
	scanner := bufio.NewScanner(os.Stdin)

	for i := 0; i < numPlayers; i++ {
		fmt.Printf("Enter name for Player %d: ", i+1)
		scanner.Scan()
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			name = fmt.Sprintf("Player %d", i+1)
		}
		players[i] = &Player{
			Name:    name,
			Score:   0,
			Answers: make([]int, 0, req.NumQuestions),
		}
	}
	fmt.Println()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Get streaming questions
	questionChan, err := generator.GenerateQuizStream(ctx, req)
	if err != nil {
		log.Fatalf("Failed to start quiz stream: %v", err)
	}

	// Interactive quiz playing
	questionNum := 0

	for question := range questionChan {
		questionNum++
		fmt.Printf("Question %d/%d:\n", questionNum, req.NumQuestions)
		fmt.Printf("%s\n\n", question.Text)

		// Display options
		options := []string{"A", "B", "C", "D"}
		for i, option := range question.Options {
			fmt.Printf("%s) %s\n", options[i], option)
		}
		fmt.Println()

		// Get answers from all players
		playerAnswers := make([]int, numPlayers)
		for i, player := range players {
			var userAnswer string
			for {
				fmt.Printf("%s's answer (A/B/C/D): ", player.Name)
				scanner.Scan()
				userAnswer = strings.ToUpper(strings.TrimSpace(scanner.Text()))

				if userAnswer == "A" || userAnswer == "B" || userAnswer == "C" || userAnswer == "D" {
					break
				}
				fmt.Println("Please enter A, B, C, or D")
			}

			playerIndex := strings.Index("ABCD", userAnswer)
			playerAnswers[i] = playerIndex
			player.Answers = append(player.Answers, playerIndex)
		}

		// Check answers and update scores
		fmt.Println()
		correctOption := options[question.CorrectAnswer]

		for i, player := range players {
			isCorrect := playerAnswers[i] == question.CorrectAnswer

			if isCorrect {
				fmt.Printf("✅ %s: Correct!\n", player.Name)
				player.Score++
			} else {
				fmt.Printf("❌ %s: Incorrect. The correct answer is %s) %s\n",
					player.Name, correctOption, question.Options[question.CorrectAnswer])
			}
		}

		if question.Explanation != "" {
			fmt.Printf("💡 Explanation: %s\n", question.Explanation)
		}

		// Show current scores
		fmt.Println("\n📊 Current Scores:")
		for _, player := range players {
			percentage := float64(player.Score) / float64(questionNum) * 100
			fmt.Printf("  %s: %d/%d (%.1f%%)\n", player.Name, player.Score, questionNum, percentage)
		}

		fmt.Println()
		fmt.Println(strings.Repeat("─", 50))
		fmt.Println()

		// If this isn't the last question, show a brief pause
		if questionNum < req.NumQuestions {
			fmt.Println("⏳ Generating next question...")
			fmt.Println()
		}
	}

	// Final results
	fmt.Println("🎉 Quiz completed!")
	fmt.Println("\n🏆 Final Results:")

	// Sort players by score (highest first)
	sort.Slice(players, func(i, j int) bool {
		return players[i].Score > players[j].Score
	})

	for i, player := range players {
		percentage := float64(player.Score) / float64(req.NumQuestions) * 100
		rank := i + 1

		if rank == 1 {
			fmt.Printf("🥇 %s: %d/%d (%.1f%%)\n", player.Name, player.Score, req.NumQuestions, percentage)
		} else if rank == 2 && numPlayers > 1 {
			fmt.Printf("🥈 %s: %d/%d (%.1f%%)\n", player.Name, player.Score, req.NumQuestions, percentage)
		} else if rank == 3 && numPlayers > 2 {
			fmt.Printf("🥉 %s: %d/%d (%.1f%%)\n", player.Name, player.Score, req.NumQuestions, percentage)
		} else {
			fmt.Printf("   %s: %d/%d (%.1f%%)\n", player.Name, player.Score, req.NumQuestions, percentage)
		}
	}

	// Winner announcement
	if numPlayers > 1 {
		winner := players[0]
		percentage := float64(winner.Score) / float64(req.NumQuestions) * 100

		fmt.Printf("\n🎊 Winner: %s with %d/%d correct answers (%.1f%%)\n",
			winner.Name, winner.Score, req.NumQuestions, percentage)

		if percentage >= 0.8 {
			fmt.Println("🌟 Outstanding performance!")
		} else if percentage >= 0.6 {
			fmt.Println("👍 Well done!")
		} else {
			fmt.Println("📚 Keep studying!")
		}
	} else {
		// Single player mode - use original feedback
		player := players[0]
		percentage := float64(player.Score) / float64(req.NumQuestions) * 100

		if percentage >= 0.8 {
			fmt.Println("🌟 Excellent work!")
		} else if percentage >= 0.6 {
			fmt.Println("👍 Good job!")
		} else {
			fmt.Println("📚 Keep studying!")
		}
	}
}
