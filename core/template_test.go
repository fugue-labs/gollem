package core

import (
	"context"
	"strings"
	"testing"
)

func TestPromptTemplate_Format(t *testing.T) {
	tmpl, err := NewPromptTemplate("greeting", "Hello, {{.Name}}! You are {{.Role}}.")
	if err != nil {
		t.Fatal(err)
	}

	result, err := tmpl.Format(map[string]string{
		"Name": "Alice",
		"Role": "admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	expected := "Hello, Alice! You are admin."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestPromptTemplate_Partial(t *testing.T) {
	tmpl, err := NewPromptTemplate("greeting", "Hello, {{.Name}}! You are {{.Role}}.")
	if err != nil {
		t.Fatal(err)
	}

	partial := tmpl.Partial(map[string]string{"Name": "Bob"})

	// Format with remaining vars.
	result, err := partial.Format(map[string]string{"Role": "user"})
	if err != nil {
		t.Fatal(err)
	}

	expected := "Hello, Bob! You are user."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}

	// Override partial vars.
	result, err = partial.Format(map[string]string{
		"Name": "Charlie",
		"Role": "editor",
	})
	if err != nil {
		t.Fatal(err)
	}

	expected = "Hello, Charlie! You are editor."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestPromptTemplate_Variables(t *testing.T) {
	tmpl, err := NewPromptTemplate("test", "{{.Name}} is a {{.Role}} in {{.Org}}")
	if err != nil {
		t.Fatal(err)
	}

	vars := tmpl.Variables()
	if len(vars) != 3 {
		t.Fatalf("expected 3 variables, got %d: %v", len(vars), vars)
	}
	// Variables should be sorted.
	if vars[0] != "Name" || vars[1] != "Org" || vars[2] != "Role" {
		t.Errorf("expected [Name Org Role], got %v", vars)
	}
}

func TestPromptTemplate_MissingVar(t *testing.T) {
	tmpl, err := NewPromptTemplate("test", "Hello, {{.Name}}!")
	if err != nil {
		t.Fatal(err)
	}

	_, err = tmpl.Format(map[string]string{})
	if err == nil {
		t.Error("expected error for missing variable")
	}
}

func TestPromptTemplate_MustTemplate(t *testing.T) {
	// Valid template should not panic.
	tmpl := MustTemplate("test", "Hello, {{.Name}}!")
	if tmpl == nil {
		t.Fatal("expected non-nil template")
	}

	// Invalid template should panic.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid template")
		}
	}()
	MustTemplate("bad", "{{.Invalid")
}

func TestPromptTemplate_AgentIntegration(t *testing.T) {
	tmpl := MustTemplate("system", "You are a {{.Role}} assistant helping with {{.Topic}}.")

	model := NewTestModel(TextResponse("I can help with that"))
	agent := NewAgent[string](model,
		WithSystemPromptTemplate[string](tmpl),
	)

	deps := map[string]string{
		"Role":  "coding",
		"Topic": "Go programming",
	}

	_, err := agent.Run(context.Background(), "help me",
		WithRunDeps(deps),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the model received the rendered system prompt.
	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}

	firstCall := calls[0]
	// Look for system prompt part in the first message.
	found := false
	for _, msg := range firstCall.Messages {
		if req, ok := msg.(ModelRequest); ok {
			for _, part := range req.Parts {
				if sp, ok := part.(SystemPromptPart); ok {
					if strings.Contains(sp.Content, "coding") && strings.Contains(sp.Content, "Go programming") {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Error("expected rendered template as system prompt")
	}
}
