package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const skillsDir = ".claude/skills"

type skill struct {
	Name        string
	Description string
	Body        string
	Context     string
	Dir         string
}

type invocation struct {
	skill skill
	text  string
}

// discoverSkills finds every skill under .claude/skills, sorted by folder name.
func discoverSkills() []skill {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}

	var skills []skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(skillsDir, entry.Name())
		content, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}

		fields, body := parseSkillFile(string(content))
		name := entry.Name()
		if fields["name"] != "" {
			name = fields["name"]
		}

		skills = append(skills, skill{
			Name:        name,
			Description: fields["description"],
			Body:        body,
			Context:     fields["context"],
			Dir:         dir,
		})
	}

	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

// parseSkillFile splits a SKILL.md into its frontmatter fields and its body.
func parseSkillFile(content string) (map[string]string, string) {
	fields := make(map[string]string)
	content = strings.ReplaceAll(content, "\r\n", "\n")

	if !strings.HasPrefix(content, "---") {
		return fields, strings.TrimSpace(content)
	}

	rest := strings.TrimPrefix(content, "---")
	rest = strings.TrimPrefix(rest, "\n")

	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fields, strings.TrimSpace(content)
	}

	frontmatter := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")

	lines := strings.Split(frontmatter, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if value == "|" || value == ">" || value == "|-" || value == ">-" {
			var block []string
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) == "" {
					block = append(block, "")
					i++
					continue
				}
				if len(next) == len(strings.TrimLeft(next, " \t")) {
					break
				}
				block = append(block, strings.TrimLeft(next, " \t"))
				i++
			}
			joiner := "\n"
			if strings.HasPrefix(value, ">") {
				joiner = " "
			}
			fields[key] = strings.Join(block, joiner)
			continue
		}

		fields[key] = unquoteYAML(value)
	}

	return fields, strings.TrimSpace(body)
}

func unquoteYAML(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// buildSystemPrompt advertises only level 1 of each skill: name and description.
func buildSystemPrompt(skills []skill) string {
	if len(skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("You have access to the following skills:\n\n")
	for _, s := range skills {
		b.WriteString("- ")
		b.WriteString(s.Name)
		b.WriteString(": ")
		b.WriteString(s.Description)
		b.WriteString("\n")
	}
	b.WriteString("\nIf a skill matches the user's request, call the Skill tool with its name\n")
	b.WriteString("and follow the instructions it returns.")
	return b.String()
}

// expandInvocations expands the run of /name tokens at the start of a prompt.
// The first token that does not name a skill ends the run; that token and
// everything after it becomes the arguments for every expanded skill.
func expandInvocations(prompt string, skills []skill) []invocation {
	tokens := strings.Fields(prompt)
	byName := make(map[string]skill, len(skills))
	for _, s := range skills {
		byName[s.Name] = s
	}

	var invoked []skill
	index := 0
	for index < len(tokens) && strings.HasPrefix(tokens[index], "/") {
		target, ok := byName[strings.TrimPrefix(tokens[index], "/")]
		if !ok {
			break
		}
		invoked = append(invoked, target)
		index++
	}

	if len(invoked) == 0 {
		return nil
	}

	arguments := tokens[index:]
	result := make([]invocation, 0, len(invoked))
	for _, target := range invoked {
		result = append(result, invocation{skill: target, text: renderInvocation(target, arguments)})
	}
	return result
}

func renderInvocation(target skill, arguments []string) string {
	body := substituteArguments(target.Body, arguments)
	return "Skill: " + target.Name + " (located at " + target.Dir + ")\n" +
		"Paths in the instructions below are relative to that folder.\n\n" + body
}

// substituteArguments replaces $ARGUMENTS[N] before $ARGUMENTS, since the former
// contains the latter, and positional placeholders from the highest index down
// so that $1 never matches the start of $10.
func substituteArguments(body string, arguments []string) string {
	for index, value := range arguments {
		body = strings.ReplaceAll(body, "$ARGUMENTS["+strconv.Itoa(index)+"]", value)
	}

	body = strings.ReplaceAll(body, "$ARGUMENTS", strings.Join(arguments, " "))

	for index := len(arguments) - 1; index >= 0; index-- {
		body = strings.ReplaceAll(body, "$"+strconv.Itoa(index), arguments[index])
	}

	return body
}

func findSkill(skills []skill, name string) *skill {
	for i := range skills {
		if skills[i].Name == name {
			return &skills[i]
		}
	}
	return nil
}

func excludeSkill(skills []skill, target *skill) []skill {
	result := make([]skill, 0, len(skills))
	for _, s := range skills {
		if s.Name == target.Name {
			continue
		}
		result = append(result, s)
	}
	return result
}
