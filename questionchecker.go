package quizgenerator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// QuestionChecker validates and potentially revises questions using GPT-4o
type QuestionChecker struct {
	client *openai.Client
}

// NewQuestionChecker creates a new question checker with OpenAI client
func NewQuestionChecker(apiKey string) *QuestionChecker {
	return &QuestionChecker{
		client: openai.NewClient(apiKey),
	}
}

// CheckQuestion validates a single question and returns the validation result
func (qc *QuestionChecker) CheckQuestion(ctx context.Context, question *Question, logger *LLMLogger) (*ValidationResult, error) {
	VerboseLog("Checking question: %s (revision count: %d)", question.ID, question.RevisionCount)

	// If question has been revised 3 times, reject it to prevent infinite loops
	if question.RevisionCount >= 3 {
		result := &ValidationResult{
			QuestionID: question.ID,
			Action:     ActionReject,
			Reason:     fmt.Sprintf("Question rejected after %d revision attempts to prevent infinite loop", question.RevisionCount),
		}

		if logger != nil {
			logger.LogQuestionResult(question.ID, string(result.Action), result.Reason)
		}

		VerboseLog("Question %s: %s - %s", question.ID, result.Action, result.Reason)
		return result, nil
	}

	prompt := qc.buildPrompt(question)

	// Log the request
	if logger != nil {
		logger.LogLLMRequest("QuestionChecker", prompt)
	}

	resp, err := qc.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: "You are an expert quiz question validator. Evaluate questions for quality, clarity, and fairness.",
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
						Name:        "evaluate_question",
						Description: "Evaluate a quiz question and decide whether to accept, reject, or revise it",
						Parameters: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"reason": map[string]interface{}{
									"type":        "string",
									"description": "Explanation for the decision",
								},
								"action": map[string]interface{}{
									"type":        "string",
									"enum":        []string{"accept", "reject", "revise"},
									"description": "What to do with this question",
								},
								"revised_question": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"text": map[string]interface{}{
											"type":        "string",
											"description": "The revised question text",
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
									"description": "Revised question (only if action is 'revise')",
								},
							},
							"required": []string{"reason", "action"},
						},
					},
				},
			},
			ToolChoice: openai.ToolChoice{
				Type: openai.ToolTypeFunction,
				Function: openai.ToolFunction{
					Name: "evaluate_question",
				},
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to check question: %w", err)
	}

	// Log the response
	if logger != nil {
		responseText := ""
		if len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0 {
			responseText = resp.Choices[0].Message.ToolCalls[0].Function.Arguments
		}
		logger.LogLLMResponse("QuestionChecker", responseText)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from GPT-4o")
	}

	choice := resp.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("no tool calls in response")
	}

	toolCall := choice.Message.ToolCalls[0]
	if toolCall.Function.Name != "evaluate_question" {
		return nil, fmt.Errorf("unexpected tool call: %s", toolCall.Function.Name)
	}

	var toolArgs struct {
		Reason          string `json:"reason"`
		Action          string `json:"action"`
		RevisedQuestion *struct {
			Text          string   `json:"text"`
			Options       []string `json:"options"`
			CorrectAnswer int      `json:"correct_answer"`
			Explanation   string   `json:"explanation"`
		} `json:"revised_question,omitempty"`
	}

	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &toolArgs); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}

	result := &ValidationResult{
		QuestionID: question.ID,
		Action:     ValidationAction(toolArgs.Action),
		Reason:     toolArgs.Reason,
	}

	if toolArgs.Action == "revise" && toolArgs.RevisedQuestion != nil {
		revised := &Question{
			ID:            question.ID, // Keep same ID
			Text:          toolArgs.RevisedQuestion.Text,
			Options:       toolArgs.RevisedQuestion.Options,
			CorrectAnswer: toolArgs.RevisedQuestion.CorrectAnswer,
			Explanation:   toolArgs.RevisedQuestion.Explanation,
			Topic:         question.Topic,
			Status:        StatusRevised,
			RevisionCount: question.RevisionCount + 1, // Increment revision counter
		}
		result.RevisedQuestion = revised
	}

	// Log the result
	if logger != nil {
		logger.LogQuestionResult(question.ID, string(result.Action), result.Reason)
	}

	VerboseLog("Question %s: %s - %s", question.ID, result.Action, result.Reason)
	return result, nil
}

func (qc *QuestionChecker) buildPrompt(question *Question) string {
	var sb strings.Builder

	sb.WriteString("Evaluate the following quiz question:\n\n")
	sb.WriteString(fmt.Sprintf("Quiz Topic: %s\n\n", question.Topic))
	sb.WriteString(fmt.Sprintf("Question: %s\n\n", question.Text))

	sb.WriteString("Options:\n")
	for i, option := range question.Options {
		marker := " "
		if i == question.CorrectAnswer {
			marker = "*"
		}
		sb.WriteString(fmt.Sprintf("%s%d. %s\n", marker, i+1, option))
	}

	sb.WriteString(fmt.Sprintf("\nCorrect Answer: %d\n", question.CorrectAnswer+1))
	sb.WriteString(fmt.Sprintf("Explanation: %s\n\n", question.Explanation))

	sb.WriteString("CRITICAL EVALUATION CRITERIA:\n")
	sb.WriteString("🚨 AUTOMATIC REJECTION: If the correct answer appears in the question text, REJECT immediately or REVISE to improve it.\n")
	sb.WriteString("🚨 AUTOMATIC REJECTION: If the question text contains obvious clues that give away the answer, REJECT immediately or REVISE to improve it.\n")
	sb.WriteString("🚨 AUTOMATIC REJECTION: If the question is not relevant to the quiz topic, REJECT immediately.\n")

	sb.WriteString("Additional evaluation criteria:\n")
	sb.WriteString("1. Is the question relevant to the quiz topic?\n")
	sb.WriteString("2. Is the question clear and unambiguous?\n")
	sb.WriteString("3. Is the correct answer actually correct?\n")
	sb.WriteString("4. Are all incorrect options plausible but clearly wrong?\n")
	sb.WriteString("5. Does the question test understanding rather than just memorization?\n")
	sb.WriteString("6. Does the explanation provide meaningful context or reasoning for WHY the answer is correct?\n\n")

	sb.WriteString("Topic relevance check:\n")
	sb.WriteString("- The question must be directly related to the quiz topic\n")
	sb.WriteString("- If the question is about a different subject or person, it should be rejected\n")
	sb.WriteString("- The question should test knowledge about the specific topic, not general knowledge\n\n")

	sb.WriteString("Explanation quality check:\n")
	sb.WriteString("- The explanation should explain WHY the answer is correct, not just restate what the answer is\n")
	sb.WriteString("- For acronyms, the explanation should break down what each letter stands for\n")
	sb.WriteString("- For concepts, the explanation should provide context or reasoning\n")
	sb.WriteString("- Avoid explanations that just repeat the answer in different words\n\n")

	sb.WriteString("Decision guidelines:\n")
	sb.WriteString("- REJECT: The question has fundamental problems (especially if answer is in question text or not relevant to topic or obvious given the topic)\n")
	sb.WriteString("- REVISE: If the question has potential but needs improvements\n")
	sb.WriteString("- ACCEPT: The question is good as-is (only if it passes all criteria)\n\n")

	sb.WriteString("IMPORTANT: Only revise explanations if they are spectacularly bad (e.g., missing acronym definitions, completely wrong information, or no explanation at all).\n")
	sb.WriteString("For mediocre or basic explanations, ACCEPT the question rather than rejecting it. A good question with a basic explanation is better than no question at all.\n")
	sb.WriteString("Only reject questions if they have fundamental structural problems (answer in question text, obvious clues, or not relevant to topic or obvious given the topic).\n")

	sb.WriteString("If you choose to revise, provide a complete revised version of the question.")

	return sb.String()
}
