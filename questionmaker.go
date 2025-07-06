package quizgenerator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// QuestionMaker generates questions using GPT-4o
type QuestionMaker struct {
	client *openai.Client
}

// NewQuestionMaker creates a new question maker with OpenAI client
func NewQuestionMaker(apiKey string) *QuestionMaker {
	return &QuestionMaker{
		client: openai.NewClient(apiKey),
	}
}

// GenerateQuestions generates a batch of questions for the given topic
func (qm *QuestionMaker) GenerateQuestions(ctx context.Context, req GenerationRequest, batchSize int) ([]*Question, error) {
	log.Printf("Generating %d questions for topic: %s", batchSize, req.Topic)

	prompt := qm.buildPrompt(req, batchSize)

	resp, err := qm.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: "You are an expert quiz question generator. Generate high-quality multiple choice questions with exactly 4 options each.",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
			Tools: []openai.Tool{
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        "submit_questions",
						Description: "Submit generated quiz questions",
						Parameters: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"questions": map[string]interface{}{
									"type": "array",
									"items": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"text": map[string]interface{}{
												"type":        "string",
												"description": "The question text",
											},
											"options": map[string]interface{}{
												"type": "array",
												"items": map[string]interface{}{
													"type": "string",
												},
												"description": "Array of 4 multiple choice options",
											},
											"correct_answer": map[string]interface{}{
												"type":        "integer",
												"description": "0-based index of the correct answer",
											},
											"explanation": map[string]interface{}{
												"type":        "string",
												"description": "Brief explanation of why the answer is correct",
											},
										},
										"required": []string{"text", "options", "correct_answer", "explanation"},
									},
								},
							},
							"required": []string{"questions"},
						},
					},
				},
			},
			ToolChoice: openai.ToolChoice{
				Type: openai.ToolTypeFunction,
				Function: openai.ToolFunction{
					Name: "submit_questions",
				},
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to generate questions: %w", err)
	}

	log.Printf("Received response from GPT-4o with %d choices", len(resp.Choices))

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from GPT-4o")
	}

	choice := resp.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("no tool calls in response")
	}

	toolCall := choice.Message.ToolCalls[0]
	if toolCall.Function.Name != "submit_questions" {
		return nil, fmt.Errorf("unexpected tool call: %s", toolCall.Function.Name)
	}

	var toolArgs struct {
		Questions []struct {
			Text          string   `json:"text"`
			Options       []string `json:"options"`
			CorrectAnswer int      `json:"correct_answer"`
			Explanation   string   `json:"explanation"`
		} `json:"questions"`
	}

	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &toolArgs); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}

	questions := make([]*Question, 0, len(toolArgs.Questions))
	for _, q := range toolArgs.Questions {
		question := &Question{
			ID:            generateQuestionID(),
			Text:          q.Text,
			Options:       q.Options,
			CorrectAnswer: q.CorrectAnswer,
			Explanation:   q.Explanation,
			Topic:         req.Topic,
			Status:        StatusTentative,
		}
		questions = append(questions, question)
	}

	log.Printf("Generated %d questions", len(questions))
	return questions, nil
}

func (qm *QuestionMaker) buildPrompt(req GenerationRequest, batchSize int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Generate %d multiple choice questions about: %s\n\n", batchSize, req.Topic))

	if req.SourceMaterial != "" {
		sb.WriteString("Use the following source material as reference:\n")
		sb.WriteString(req.SourceMaterial)
		sb.WriteString("\n\n")
	}

	if req.Difficulty != "" {
		sb.WriteString(fmt.Sprintf("Difficulty level: %s\n\n", req.Difficulty))
	}

	sb.WriteString("Requirements:\n")
	sb.WriteString("- Each question must have exactly 4 multiple choice options\n")
	sb.WriteString("- The correct answer should be non-obvious but clearly correct\n")
	sb.WriteString("- Incorrect options should be plausible but clearly wrong\n")
	sb.WriteString("- Questions should test understanding, not just memorization\n")
	sb.WriteString("- Avoid questions where the answer is given away in the question text\n")
	sb.WriteString("- Provide a brief explanation for why the correct answer is right\n")
	sb.WriteString("- Use the submit_questions tool to return your questions\n")

	return sb.String()
}

func generateQuestionID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
