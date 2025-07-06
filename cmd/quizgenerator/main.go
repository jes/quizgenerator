package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
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
	)

	flag.Parse()

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

	log.Printf("Starting quiz generation for topic: %s", *topic)
	log.Printf("Target questions: %d, Difficulty: %s", *numQuestions, *difficulty)
	if *sourceMaterial != "" {
		log.Printf("Using source material: %d characters", len(*sourceMaterial))
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

	log.Printf("Quiz generation completed successfully!")
}
