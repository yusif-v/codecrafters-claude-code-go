package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func main() {
	var prompt string
	flag.StringVar(&prompt, "p", "", "Prompt to send to LLM")
	flag.Parse()

	if prompt == "" {
		panic("Prompt must not be empty")
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	baseUrl := os.Getenv("OPENROUTER_BASE_URL")
	if baseUrl == "" {
		baseUrl = "https://openrouter.ai/api/v1"
	}

	if apiKey == "" {
		panic("Env variable OPENROUTER_API_KEY not found")
	}

	client := openai.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(baseUrl))

	messages := []openai.ChatCompletionMessageParamUnion{
		{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.String(prompt),
				},
			},
		},
	}

	for {
		resp, err := client.Chat.Completions.New(context.Background(),
			openai.ChatCompletionNewParams{
				Model:    "anthropic/claude-haiku-4.5",
				Messages: messages,
				Tools: []openai.ChatCompletionToolUnionParam{
					openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
						Name:        "Read",
						Description: openai.String("Read and return the contents of a file"),
						Parameters: openai.FunctionParameters{
							"type": "object",
							"properties": map[string]any{
								"file_path": map[string]any{
									"type":        "string",
									"description": "The path to the file to read",
								},
							},
							"required": []string{"file_path"},
						},
					}),
				},
			},
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(resp.Choices) == 0 {
			panic("No choices in response")
		}

		msg := resp.Choices[0].Message

		if msg.ToolCalls == nil {
			fmt.Print(msg.Content)
			return
		}

		// Append assistant message with tool calls to history
		toolCallParams := make([]openai.ChatCompletionMessageToolCallUnionParam, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			toolCallParams[i] = tc.ToParam()
		}
		messages = append(messages, openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				ToolCalls: toolCallParams,
			},
		})

		// Execute each tool call and append result to history
		for _, toolCall := range msg.ToolCalls {
			var args map[string]string
			json.Unmarshal([]byte(toolCall.Function.Arguments), &args)
			filePath := args["file_path"]
			content, err := os.ReadFile(filePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
				os.Exit(1)
			}
			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					ToolCallID: toolCall.ID,
					Content: openai.ChatCompletionToolMessageParamContentUnion{
						OfString: openai.String(string(content)),
					},
				},
			})
		}
	}
}
