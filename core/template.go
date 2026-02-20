package core

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

// PromptTemplate is a prompt with named variable placeholders using {{.VarName}} syntax.
// Uses Go's text/template under the hood for safe, familiar templating.
type PromptTemplate struct {
	name string
	raw  string
	tmpl *template.Template
	// partial holds pre-filled variable values.
	partial map[string]string
}

// NewPromptTemplate creates a template from a Go template string.
// Returns error if the template is malformed.
func NewPromptTemplate(name, tmpl string) (*PromptTemplate, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template %q: %w", name, err)
	}
	return &PromptTemplate{
		name: name,
		raw:  tmpl,
		tmpl: t,
	}, nil
}

// MustTemplate is like NewPromptTemplate but panics on error. For use with constants.
func MustTemplate(name, tmpl string) *PromptTemplate {
	pt, err := NewPromptTemplate(name, tmpl)
	if err != nil {
		panic(err)
	}
	return pt
}

// Format renders the template with the given variables.
func (t *PromptTemplate) Format(vars map[string]string) (string, error) {
	// Merge partial vars with provided vars (provided vars take precedence).
	merged := make(map[string]string)
	for k, v := range t.partial {
		merged[k] = v
	}
	for k, v := range vars {
		merged[k] = v
	}

	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, merged); err != nil {
		return "", fmt.Errorf("template %q: %w", t.name, err)
	}
	return buf.String(), nil
}

// Partial returns a new template with some variables pre-filled.
func (t *PromptTemplate) Partial(vars map[string]string) *PromptTemplate {
	merged := make(map[string]string)
	for k, v := range t.partial {
		merged[k] = v
	}
	for k, v := range vars {
		merged[k] = v
	}
	return &PromptTemplate{
		name:    t.name,
		raw:     t.raw,
		tmpl:    t.tmpl,
		partial: merged,
	}
}

// Variables returns the set of variable names used in the template.
// It parses the template tree to extract field names from actions.
func (t *PromptTemplate) Variables() []string {
	// Simple extraction: find all {{.VarName}} patterns in the raw template.
	varSet := make(map[string]bool)
	raw := t.raw
	for {
		idx := strings.Index(raw, "{{")
		if idx < 0 {
			break
		}
		end := strings.Index(raw[idx:], "}}")
		if end < 0 {
			break
		}
		action := strings.TrimSpace(raw[idx+2 : idx+end])
		if strings.HasPrefix(action, ".") {
			varName := strings.TrimPrefix(action, ".")
			// Handle cases like {{.Foo}} and ignore pipeline actions.
			if varName != "" && !strings.ContainsAny(varName, " |()") {
				varSet[varName] = true
			}
		}
		raw = raw[idx+end+2:]
	}

	vars := make([]string, 0, len(varSet))
	for v := range varSet {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	return vars
}

// TemplateVars provides template variable values. Implement on your deps type.
type TemplateVars interface {
	TemplateVars() map[string]string
}

// WithSystemPromptTemplate uses a template as a system prompt, rendered with
// variables from the RunContext.Deps (must be map[string]string or implement TemplateVars).
func WithSystemPromptTemplate[T any](tmpl *PromptTemplate) AgentOption[T] {
	return func(a *Agent[T]) {
		a.dynamicSystemPrompts = append(a.dynamicSystemPrompts, func(ctx context.Context, rc *RunContext) (string, error) {
			vars, err := extractTemplateVars(rc.Deps)
			if err != nil {
				return "", err
			}
			return tmpl.Format(vars)
		})
	}
}

// extractTemplateVars extracts template variables from deps.
func extractTemplateVars(deps any) (map[string]string, error) {
	if deps == nil {
		return map[string]string{}, nil
	}
	if tv, ok := deps.(TemplateVars); ok {
		return tv.TemplateVars(), nil
	}
	if m, ok := deps.(map[string]string); ok {
		return m, nil
	}
	return nil, fmt.Errorf("deps must implement TemplateVars or be map[string]string, got %T", deps)
}
