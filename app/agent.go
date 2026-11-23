package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/openai/openai-go/v3"
)

func toolDefinitions(skills []skill) []openai.ChatCompletionToolUnionParam {
	tools := []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{ // Read
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
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{ // Write
			Name:        "Write",
			Description: openai.String("Write content to a file"),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path of the file to write to",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file",
					},
				},
				"required": []string{"file_path", "content"},
			},
		}),
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{ // Bash
			Name:        "Bash",
			Description: openai.String("Execute a shell command"),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The command to execute",
					},
				},
				"required": []string{"command"},
			},
		}),
	}

	if len(skills) == 0 {
		return tools
	}

	tools = append(tools, openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{ // Skill
		Name:        "Skill",
		Description: openai.String("Load a skill's instructions into the conversation"),
		Parameters: openai.FunctionParameters{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the skill to use",
				},
				"args": map[string]any{
					"type":        "string",
					"description": "Optional arguments for the skill",
				},
			},
		},
	}))
	return tools
}

func runAgent(prompt string) string {
	skills := discoverSkills()

	var messages []openai.ChatCompletionMessageParamUnion
	if systemPrompt := buildSystemPrompt(skills); systemPrompt != "" {
		messages = append(messages, openai.SystemMessage(systemPrompt))
	}

	invocations := expandInvocations(prompt, skills)
	if len(invocations) == 0 {
		messages = append(messages, openai.UserMessage(prompt))
	}

	for _, invocation := range invocations {
		if invocation.skill.Context != "fork" {
			messages = append(messages, openai.UserMessage(invocation.text))
			continue
		}

		result := runForkedSkill(invocation.skill, skills, invocation.text)
		messages = append(messages, openai.UserMessage(fmt.Sprintf(
			"The /%s skill ran in a separate context and returned:\n\n%s",
			invocation.skill.Name, result,
		)))
	}

	return runLoop(messages, skills)
}

// runLoop drives one conversation until the model answers without calling a tool.
func runLoop(messages []openai.ChatCompletionMessageParamUnion, skills []skill) string {
	for {
		resp, err := llmClient.Chat.Completions.New(context.Background(),
			openai.ChatCompletionNewParams{
				Model:    modelName,
				Messages: messages,
				Tools:    toolDefinitions(skills),
			},
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(resp.Choices) == 0 {
			fmt.Fprintln(os.Stderr, "error: no choices in response")
			os.Exit(1)
		}

		msg := resp.Choices[0].Message

		if len(msg.ToolCalls) == 0 {
			return msg.Content
		}

		toolCallParams := make([]openai.ChatCompletionMessageToolCallUnionParam, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			toolCallParams[i] = tc.ToParam()
		}
		messages = append(messages, openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				ToolCalls: toolCallParams,
			},
		})

		for _, toolCall := range msg.ToolCalls {
			var args map[string]string
			json.Unmarshal([]byte(toolCall.Function.Arguments), &args)

			var result string
			switch toolCall.Function.Name {
			case "Read":
				result = readFile(args["file_path"])
			case "Write":
				result = writeFile(args["file_path"], args["content"])
			case "Bash":
				result = bashCommand(args["command"])
			case "Skill":
				result = runSkillTool(args, skills)
			default:
				result = "Unknown tool: " + toolCall.Function.Name
			}

			messages = append(messages, openai.ToolMessage(result, toolCall.ID))
		}
	}
}

func runSkillTool(args map[string]string, skills []skill) string {
	name := args["name"]
	target := findSkill(skills, name)
	if target == nil {
		return "Unknown skill: " + name
	}

	argText := args["args"]
	if argText == "" {
		argText = args["arguments"]
	}

	invocation := renderInvocation(*target, strings.Fields(argText))

	if target.Context != "fork" {
		return invocation
	}

	// A forked skill runs in its own conversation; only its answer comes back.
	others := excludeSkill(skills, target)
	return runForkedSkill(*target, others, invocation)
}

func runForkedSkill(target skill, skills []skill, invocation string) string {
	return runLoop([]openai.ChatCompletionMessageParamUnion{openai.UserMessage(invocation)}, skills)
}

func readFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}
	return string(content)
}

func writeFile(path, content string) string {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Sprintf("Error writing to file: %v", err)
	}
	return content
}

func bashCommand(command string) string {
	output, _ := exec.Command("bash", "-c", command).CombinedOutput()
	return string(output)
}
