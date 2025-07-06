package quizgenerator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// QuestionDedup checks for duplicate questions using GPT-4o
type QuestionDedup struct {
	client *openai.Client
	cache  map[string]*Question // Cache of accepted questions by ID
}

// NewQuestionDedup creates a new question deduplicator
func NewQuestionDedup(apiKey string) *QuestionDedup {
	return &QuestionDedup{
		client: openai.NewClient(apiKey),
		cache:  make(map[string]*Question),
	}
}

// DedupResult represents the result of deduplication
type DedupResult struct {
	IsDuplicate bool   `json:"is_duplicate"`
	Reason      string `json:"reason"`
	DuplicateID string `json:"duplicate_id,omitempty"` // ID of the duplicate question if found
}

// CheckDuplicate checks if a question is a duplicate of any previously accepted question
func (qd *QuestionDedup) CheckDuplicate(ctx context.Context, question *Question) (*DedupResult, error) {
	if len(qd.cache) == 0 {
		// First question, always accept
		qd.cache[question.ID] = question
		return &DedupResult{IsDuplicate: false, Reason: "First question"}, nil
	}

	VerboseLog("Checking for duplicates: %s", question.ID)

	// Build context of existing questions
	var existingQuestions strings.Builder
	existingQuestions.WriteString("Existing accepted questions:\n\n")

	for id, existing := range qd.cache {
		existingQuestions.WriteString(fmt.Sprintf("ID: %s\n", id))
		existingQuestions.WriteString(fmt.Sprintf("Question: %s\n", existing.Text))
		existingQuestions.WriteString("Options:\n")
		for i, option := range existing.Options {
			marker := " "
			if i == existing.CorrectAnswer {
				marker = "*"
			}
			existingQuestions.WriteString(fmt.Sprintf("%s%d. %s\n", marker, i+1, option))
		}
		existingQuestions.WriteString(fmt.Sprintf("Correct Answer: %d\n", existing.CorrectAnswer+1))
		existingQuestions.WriteString(fmt.Sprintf("Explanation: %s\n\n", existing.Explanation))
	}

	// Build prompt for new question
	var newQuestion strings.Builder
	newQuestion.WriteString("New question to check:\n\n")
	newQuestion.WriteString(fmt.Sprintf("ID: %s\n", question.ID))
	newQuestion.WriteString(fmt.Sprintf("Question: %s\n", question.Text))
	newQuestion.WriteString("Options:\n")
	for i, option := range question.Options {
		marker := " "
		if i == question.CorrectAnswer {
			marker = "*"
		}
		newQuestion.WriteString(fmt.Sprintf("%s%d. %s\n", marker, i+1, option))
	}
	newQuestion.WriteString(fmt.Sprintf("Correct Answer: %d\n", question.CorrectAnswer+1))
	newQuestion.WriteString(fmt.Sprintf("Explanation: %s\n\n", question.Explanation))

	prompt := existingQuestions.String() + newQuestion.String() + qd.buildEvaluationCriteria()

	// Log the request
	if logger := GetGlobalLogger(); logger != nil {
		logger.LogLLMRequest("QuestionDedup", prompt)
	}

	resp, err := qd.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: "You are an expert at detecting duplicate quiz questions. Compare the new question against existing questions and determine if it's a duplicate.",
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
						Name:        "check_duplicate",
						Description: "Check if the new question is a duplicate of any existing question",
						Parameters: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"reason": map[string]interface{}{
									"type":        "string",
									"description": "Explanation for the decision",
								},
								"is_duplicate": map[string]interface{}{
									"type":        "boolean",
									"description": "Whether the new question is a duplicate",
								},
								"duplicate_id": map[string]interface{}{
									"type":        "string",
									"description": "ID of the duplicate question if found (empty if not a duplicate)",
								},
							},
							"required": []string{"reason", "is_duplicate"},
						},
					},
				},
			},
			ToolChoice: openai.ToolChoice{
				Type: openai.ToolTypeFunction,
				Function: openai.ToolFunction{
					Name: "check_duplicate",
				},
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to check duplicate: %w", err)
	}

	// Log the response
	if logger := GetGlobalLogger(); logger != nil {
		responseText := ""
		if len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0 {
			responseText = resp.Choices[0].Message.ToolCalls[0].Function.Arguments
		}
		logger.LogLLMResponse("QuestionDedup", responseText)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from GPT-4o")
	}

	choice := resp.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("no tool calls in response")
	}

	toolCall := choice.Message.ToolCalls[0]
	if toolCall.Function.Name != "check_duplicate" {
		return nil, fmt.Errorf("unexpected tool call: %s", toolCall.Function.Name)
	}

	var toolArgs struct {
		Reason      string `json:"reason"`
		IsDuplicate bool   `json:"is_duplicate"`
		DuplicateID string `json:"duplicate_id"`
	}

	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &toolArgs); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}

	result := &DedupResult{
		IsDuplicate: toolArgs.IsDuplicate,
		Reason:      toolArgs.Reason,
		DuplicateID: toolArgs.DuplicateID,
	}

	// If not a duplicate, add to cache
	if !result.IsDuplicate {
		qd.cache[question.ID] = question
	}

	// Log the result
	if logger := GetGlobalLogger(); logger != nil {
		logger.LogDedupResult(question.ID, result.IsDuplicate, result.Reason, result.DuplicateID)
	}

	VerboseLog("Question %s: duplicate=%v, reason=%s", question.ID, result.IsDuplicate, result.Reason)
	return result, nil
}

func (qd *QuestionDedup) buildEvaluationCriteria() string {
	return `Evaluation criteria for duplicates:

1. EXACT DUPLICATES: Same question text, same options, same correct answer
2. NEAR-DUPLICATES: 
   - Same concept tested but different wording
   - Same question with minor rephrasing
   - Same topic with very similar answer choices
   - Questions that test the same knowledge point
3. NOT DUPLICATES:
   - Different aspects of the same topic
   - Different difficulty levels
   - Different approaches to testing knowledge
   - Questions that test related but distinct concepts

Consider both the question text and the answer choices when determining duplicates.
If the new question is a duplicate, provide the ID of the existing question it duplicates.

Decide whether the new question is a duplicate of any existing question.`
}
