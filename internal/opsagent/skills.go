package opsagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type SkillSummary struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
}

type Skill struct {
	SkillSummary
	Content string `json:"content"`
	path    string
}

type SkillLoader struct{ skills map[string]Skill }

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func LoadSkills(root string) (*SkillLoader, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read skills directory: %w", err)
	}
	loader := &SkillLoader{skills: make(map[string]Skill)}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		skill, err := parseSkill(path, data)
		if err != nil {
			return nil, err
		}
		if _, exists := loader.skills[skill.Name]; exists {
			return nil, fmt.Errorf("duplicate skill name %q", skill.Name)
		}
		loader.skills[skill.Name] = skill
	}
	if len(loader.skills) == 0 {
		return nil, errors.New("skills directory contains no valid SKILL.md")
	}
	return loader, nil
}

func parseSkill(path string, data []byte) (Skill, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Skill{}, fmt.Errorf("%s: missing YAML frontmatter", path)
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "---\n"), "\n---\n", 2)
	if len(parts) != 2 {
		return Skill{}, fmt.Errorf("%s: unterminated YAML frontmatter", path)
	}
	var summary SkillSummary
	if err := yaml.Unmarshal([]byte(parts[0]), &summary); err != nil {
		return Skill{}, fmt.Errorf("%s: parse frontmatter: %w", path, err)
	}
	if !skillNamePattern.MatchString(summary.Name) || strings.TrimSpace(summary.Description) == "" {
		return Skill{}, fmt.Errorf("%s: invalid name or description", path)
	}
	if len(parts[1]) > 64*1024 {
		return Skill{}, fmt.Errorf("%s: skill body exceeds 64 KiB", path)
	}
	return Skill{SkillSummary: summary, Content: strings.TrimSpace(parts[1]), path: path}, nil
}

func (l *SkillLoader) List() []SkillSummary {
	items := make([]SkillSummary, 0, len(l.skills))
	for _, skill := range l.skills {
		items = append(items, skill.SkillSummary)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (l *SkillLoader) Get(name string) (Skill, error) {
	skill, exists := l.skills[name]
	if !exists {
		return Skill{}, fmt.Errorf("skill %q is not registered", name)
	}
	return skill, nil
}
