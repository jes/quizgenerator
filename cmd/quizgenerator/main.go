package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
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
		playQuiz(generator, req)
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

func playQuiz(generator *quizgenerator.QuizGenerator, req quizgenerator.GenerationRequest) {
	fmt.Printf("🎯 Starting interactive quiz on: %s\n", req.Topic)
	fmt.Printf("📝 Questions: %d, Difficulty: %s\n", req.NumQuestions, req.Difficulty)
	if req.SourceMaterial != "" {
		fmt.Printf("📚 Using source material: %d characters\n", len(req.SourceMaterial))
	}
	fmt.Println("⏳ Generating questions... (this may take a moment)")
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
	scanner := bufio.NewScanner(os.Stdin)
	correctAnswers := 0
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

		// Get user answer
		var userAnswer string
		for {
			fmt.Print("Your answer (A/B/C/D): ")
			scanner.Scan()
			userAnswer = strings.ToUpper(strings.TrimSpace(scanner.Text()))

			if userAnswer == "A" || userAnswer == "B" || userAnswer == "C" || userAnswer == "D" {
				break
			}
			fmt.Println("Please enter A, B, C, or D")
		}

		// Check answer
		userIndex := strings.Index("ABCD", userAnswer)
		isCorrect := userIndex == question.CorrectAnswer

		fmt.Println()
		if isCorrect {
			fmt.Println("✅ Correct!")
			correctAnswers++
		} else {
			correctOption := options[question.CorrectAnswer]
			fmt.Printf("❌ Incorrect. The correct answer is %s) %s\n",
				correctOption, question.Options[question.CorrectAnswer])
		}

		if question.Explanation != "" {
			fmt.Printf("💡 Explanation: %s\n", question.Explanation)
		}

		fmt.Printf("Score: %d/%d (%.1f%%)\n", correctAnswers, questionNum,
			float64(correctAnswers)/float64(questionNum)*100)
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
	fmt.Printf("Final Score: %d/%d (%.1f%%)\n", correctAnswers, req.NumQuestions,
		float64(correctAnswers)/float64(req.NumQuestions)*100)

	if float64(correctAnswers)/float64(req.NumQuestions) >= 0.8 {
		fmt.Println("🌟 Excellent work!")
	} else if float64(correctAnswers)/float64(req.NumQuestions) >= 0.6 {
		fmt.Println("👍 Good job!")
	} else {
		fmt.Println("📚 Keep studying!")
	}
}
