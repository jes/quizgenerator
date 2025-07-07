package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"quizgenerator"

	openai "github.com/sashabaranov/go-openai"
)

// TopicSuggestion represents a suggested quiz topic
type TopicSuggestion struct {
	Topic          string `json:"topic"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	Difficulty     string `json:"difficulty"`
	SourceMaterial string `json:"source_material"`
}

// TopicGenerator generates quiz topics using an LLM
type TopicGenerator struct {
	client *openai.Client
}

// NewTopicGenerator creates a new topic generator
func NewTopicGenerator(apiKey string) *TopicGenerator {
	return &TopicGenerator{
		client: openai.NewClient(apiKey),
	}
}

// GenerateFreshTopic generates a single fresh quiz topic that doesn't exist in the database
func (tg *TopicGenerator) GenerateFreshTopic(ctx context.Context, existingTopics []string, category string) (*TopicSuggestion, error) {
	var prompt strings.Builder

	prompt.WriteString("Generate ONE interesting quiz topic that would make for engaging multiple choice questions.\n\n")

	if category != "" {
		prompt.WriteString(fmt.Sprintf("Focus on the category: %s\n\n", category))
	}

	prompt.WriteString("IMPORTANT: The topic must be completely different from these existing topics:\n")
	for _, topic := range existingTopics {
		prompt.WriteString(fmt.Sprintf("- %s\n", topic))
	}
	prompt.WriteString("\n")

	prompt.WriteString("Requirements:\n")
	prompt.WriteString("- Topic should be educational and engaging\n")
	prompt.WriteString("- Topic should be broad enough to generate 10+ questions\n")
	prompt.WriteString("- Avoid overly specific or niche topics\n")
	prompt.WriteString("- Must be completely different from the existing topics listed above\n")
	prompt.WriteString("- Include topics from various fields: science, history, literature, technology, arts, etc.\n\n")

	prompt.WriteString("For the topic, provide:\n")
	prompt.WriteString("- A clear, concise topic name\n")
	prompt.WriteString("- A brief description of what the quiz would cover\n")
	prompt.WriteString("- A category (e.g., Science, History, Technology, Arts, Literature, Geography, etc.)\n")
	prompt.WriteString("- A suggested difficulty level (easy, medium, or hard)\n")
	prompt.WriteString("- Source material: Write 3-4 detailed paragraphs about the topic that can be used to generate accurate questions. Include key facts, concepts, historical context, important figures, and interesting details that would make for good multiple choice questions.\n\n")

	prompt.WriteString("Return the topic using the submit_topic tool.")

	resp, err := tg.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: "You are an expert at creating engaging quiz topics. Generate unique, educational topics that would make for interesting multiple choice quizzes. When writing source material, be comprehensive and include specific details that can be used to create accurate questions.",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt.String(),
				},
			},
			Tools: []openai.Tool{
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        "submit_topic",
						Description: "Submit the generated quiz topic",
						Parameters: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"topic": map[string]interface{}{
									"type":        "string",
									"description": "The quiz topic name",
								},
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Brief description of what the quiz covers",
								},
								"category": map[string]interface{}{
									"type":        "string",
									"description": "Category of the topic (e.g., Science, History, Technology)",
								},
								"difficulty": map[string]interface{}{
									"type":        "string",
									"description": "Suggested difficulty level (easy, medium, hard)",
								},
								"source_material": map[string]interface{}{
									"type":        "string",
									"description": "Detailed source material (3-4 paragraphs) about the topic for generating questions",
								},
							},
							"required": []string{"topic", "description", "category", "difficulty", "source_material"},
						},
					},
				},
			},
			ToolChoice: openai.ToolChoice{
				Type: openai.ToolTypeFunction,
				Function: openai.ToolFunction{
					Name: "submit_topic",
				},
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to generate topic: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	choice := resp.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("no tool calls in response")
	}

	toolCall := choice.Message.ToolCalls[0]
	if toolCall.Function.Name != "submit_topic" {
		return nil, fmt.Errorf("unexpected tool call: %s", toolCall.Function.Name)
	}

	var topic TopicSuggestion
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &topic); err != nil {
		return nil, fmt.Errorf("failed to parse topic: %w", err)
	}

	return &topic, nil
}

func main() {
	var (
		category     = flag.String("category", "", "Focus on specific category (optional)")
		numQuestions = flag.Int("questions", 10, "Number of questions per quiz")
		difficulty   = flag.String("difficulty", "medium", "Default difficulty level")
		dbPath       = flag.String("db", "./quiz.db", "Database path")
		apiKey       = flag.String("api-key", "", "OpenAI API key (or set OPENAI_API_KEY env var)")
		verbose      = flag.Bool("verbose", false, "Enable verbose output")
	)

	flag.Parse()

	quizgenerator.SetVerbose(*verbose)

	// Get API key from flag or environment
	if *apiKey == "" {
		*apiKey = os.Getenv("OPENAI_API_KEY")
		if *apiKey == "" {
			log.Fatal("OpenAI API key is required. Use -api-key flag or set OPENAI_API_KEY environment variable.")
		}
	}

	// Initialize database
	db, err := quizgenerator.OpenDB(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.CloseDB()

	// Create tables if they don't exist
	if err := db.CreateTables(); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	// Get existing quiz topics
	existingQuizzes, err := db.GetQuizzes(0) // Get all quizzes
	if err != nil {
		log.Fatalf("Failed to get existing quizzes: %v", err)
	}

	var existingTopics []string
	for _, quiz := range existingQuizzes {
		existingTopics = append(existingTopics, quiz.Topic)
	}

	fmt.Printf("📚 Found %d existing quiz topics in database\n", len(existingTopics))
	if len(existingTopics) > 0 {
		fmt.Println("Existing topics:")
		for _, topic := range existingTopics {
			fmt.Printf("  - %s\n", topic)
		}
		fmt.Println()
	}

	// Create topic generator
	topicGen := NewTopicGenerator(*apiKey)

	// Generate fresh topic
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	fmt.Printf("🎯 Generating a fresh quiz topic")
	if *category != "" {
		fmt.Printf(" in category: %s", *category)
	}
	fmt.Println("...")

	topic, err := topicGen.GenerateFreshTopic(ctx, existingTopics, *category)
	if err != nil {
		log.Fatalf("Failed to generate topic: %v", err)
	}

	fmt.Printf("✅ Generated fresh topic:\n\n")
	fmt.Printf("Topic: %s (%s - %s)\n", topic.Topic, topic.Category, topic.Difficulty)
	fmt.Printf("Description: %s\n\n", topic.Description)
	fmt.Printf("Source Material:\n%s\n\n", topic.SourceMaterial)

	// Use topic's difficulty or default
	quizDifficulty := topic.Difficulty
	if quizDifficulty == "" {
		quizDifficulty = *difficulty
	}

	// Generate quiz ID
	quizID := generateQuizID()

	// Create quiz in database
	quiz := &quizgenerator.DBQuiz{
		ID:             quizID,
		Topic:          topic.Topic,
		NumQuestions:   *numQuestions,
		SourceMaterial: topic.SourceMaterial, // Use the detailed source material
		Difficulty:     quizDifficulty,
		CreatedAt:      time.Now(),
		Status:         "generating",
	}

	if err := db.CreateQuiz(quiz); err != nil {
		log.Fatalf("Failed to create quiz for topic '%s': %v", topic.Topic, err)
	}

	fmt.Printf("🚀 Quiz created with ID: %s\n", quizID)

	db.GenerateQuiz(quizID, topic.Topic, *numQuestions, topic.SourceMaterial, quizDifficulty)

	fmt.Printf("🎉 Successfully completed quiz generation!\n")
}

func generateQuizID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
