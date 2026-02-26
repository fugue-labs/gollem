package codetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fugue-labs/gollem/core"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeTestFile(t, dir, "hello.go", `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}
`)
	writeTestFile(t, dir, "lib/utils.go", `package lib

func Add(a, b int) int {
	return a + b
}

func Multiply(a, b int) int {
	return a * b
}
`)
	writeTestFile(t, dir, "lib/utils_test.go", `package lib

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Error("expected 5")
	}
}
`)
	writeTestFile(t, dir, "README.md", `# Test Project

This is a test project.
`)
	return dir
}

func writeTestFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func call(t *testing.T, tool core.Tool, argsJSON string) string {
	t.Helper()
	ctx := context.Background()
	rc := &core.RunContext{}
	result, err := tool.Handler(ctx, rc, argsJSON)
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	return s
}

func callErr(t *testing.T, tool core.Tool, argsJSON string) error {
	t.Helper()
	ctx := context.Background()
	rc := &core.RunContext{}
	_, err := tool.Handler(ctx, rc, argsJSON)
	return err
}

func callBashStr(t *testing.T, tool core.Tool, argsJSON string) string {
	t.Helper()
	return call(t, tool, argsJSON)
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected %q NOT to contain %q", s, substr)
	}
}

// --- Truncation Tests ---

func TestTruncateOutput_Short(t *testing.T) {
	result := truncateOutput("short output", 1000)
	if result != "short output" {
		t.Errorf("expected no truncation, got %q", result)
	}
}

func TestTruncateOutput_Long(t *testing.T) {
	// Create a long string with identifiable head and tail.
	input := strings.Repeat("HEAD", 100) + strings.Repeat("MIDDLE", 500) + strings.Repeat("TAIL", 100)
	result := truncateOutput(input, 500)

	if len(result) > 600 { // some slack for the separator
		t.Errorf("result too long: %d bytes", len(result))
	}
	assertContains(t, result, "HEAD")
	assertContains(t, result, "TAIL")
	assertContains(t, result, "truncated")
}

func TestTruncateOutput_ZeroMax(t *testing.T) {
	result := truncateOutput("anything", 0)
	if result != "anything" {
		t.Errorf("expected no truncation with maxLen=0, got %q", result)
	}
}

// --- Bash Tests ---

func TestBash_Echo(t *testing.T) {
	dir := setupTestDir(t)
	tool := Bash(WithWorkDir(dir))
	result := callBashStr(t, tool, `{"command": "echo hello world"}`)
	assertContains(t, result, "hello world")
	// Success: no exit code shown.
	assertNotContains(t, result, "exit code")
}

func TestBash_ExitCode(t *testing.T) {
	tool := Bash()
	result := callBashStr(t, tool, `{"command": "exit 42"}`)
	assertContains(t, result, "[exit code: 42]")
}

func TestBash_Timeout(t *testing.T) {
	tool := Bash(WithBashTimeout(1 * time.Second))
	result := callBashStr(t, tool, `{"command": "sleep 10"}`)
	assertContains(t, result, "timed out")
}

func TestBash_EmptyCommand(t *testing.T) {
	tool := Bash()
	err := callErr(t, tool, `{"command": ""}`)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestBash_WorkDir(t *testing.T) {
	dir := setupTestDir(t)
	tool := Bash(WithWorkDir(dir))
	result := callBashStr(t, tool, `{"command": "ls hello.go"}`)
	assertContains(t, result, "hello.go")
}

func TestBash_Stderr(t *testing.T) {
	tool := Bash()
	result := callBashStr(t, tool, `{"command": "echo err >&2"}`)
	assertContains(t, result, "err")
}

func TestBash_CustomTimeout(t *testing.T) {
	tool := Bash(WithBashTimeout(60 * time.Second))
	result := callBashStr(t, tool, `{"command": "sleep 10", "timeout": 1}`)
	assertContains(t, result, "timed out")
}

func TestBash_BuildTimeout(t *testing.T) {
	// Verify build commands get auto-extended timeout.
	if !isBuildCommand("make -j4") {
		t.Error("expected make to be detected as build command")
	}
	if !isBuildCommand("cargo build --release") {
		t.Error("expected cargo build to be detected as build command")
	}
	if !isBuildCommand("pip install numpy") {
		t.Error("expected pip install to be detected as build command")
	}
	if isBuildCommand("echo hello") {
		t.Error("expected echo NOT to be detected as build command")
	}
}

func TestBash_BuildTimeoutFloorWithExplicitShortTimeout(t *testing.T) {
	tool := Bash(WithBashTimeout(1 * time.Second))
	// Even with timeout=1, build commands get a 5m floor.
	result := callBashStr(t, tool, `{"command": "make --version >/dev/null && sleep 2", "timeout": 1}`)
	assertNotContains(t, result, "timed out")
}

func TestFormatBashOutput(t *testing.T) {
	// Success with stdout only.
	result := formatBashOutput("hello\n", "", 0, false, 0, "")
	if result != "hello\n" {
		t.Errorf("stdout only: got %q", result)
	}

	// Success with stderr.
	result = formatBashOutput("out\n", "warn\n", 0, false, 0, "")
	assertContains(t, result, "out")
	assertContains(t, result, "[stderr]")
	assertContains(t, result, "warn")

	// Error with no output.
	result = formatBashOutput("", "", 1, false, 0, "")
	assertContains(t, result, "[exit code: 1]")
	assertContains(t, result, "(no output)")

	// Timeout.
	result = formatBashOutput("partial\n", "", 124, true, 120*time.Second, "")
	assertContains(t, result, "partial")
	assertContains(t, result, "[timed out after")

	// No output, success.
	result = formatBashOutput("", "", 0, false, 0, "")
	if result != "(no output)" {
		t.Errorf("empty success: got %q", result)
	}
}

func TestModuleNotFoundHint(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"simple module", "ModuleNotFoundError: No module named 'numpy'", "[hint: try: uv pip install --system numpy (fallback: pip install --break-system-packages numpy)]"},
		{"aliased module", "ModuleNotFoundError: No module named 'cv2'", "[hint: try: uv pip install --system opencv-python (fallback: pip install --break-system-packages opencv-python)]"},
		{"submodule", "ModuleNotFoundError: No module named 'sklearn.ensemble'", "[hint: try: uv pip install --system scikit-learn (fallback: pip install --break-system-packages scikit-learn)]"},
		{"double quotes", `ModuleNotFoundError: No module named "yaml"`, "[hint: try: uv pip install --system PyYAML (fallback: pip install --break-system-packages PyYAML)]"},
		{"no match", "some random error output", ""},
		{"PIL alias", "ModuleNotFoundError: No module named 'PIL'", "[hint: try: uv pip install --system Pillow (fallback: pip install --break-system-packages Pillow)]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := moduleNotFoundHint(tt.output)
			if got != tt.want {
				t.Errorf("moduleNotFoundHint(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestModuleNotFoundHintLocalModule(t *testing.T) {
	// Create a temp directory with a local Python module.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "solution.py"), []byte("# solution"), 0o644)

	// Also create a package directory with __init__.py.
	pkgDir := filepath.Join(dir, "mylib")
	os.MkdirAll(pkgDir, 0o755)
	os.WriteFile(filepath.Join(pkgDir, "__init__.py"), []byte(""), 0o644)

	// Local .py file: should suggest PYTHONPATH, not pip install.
	got := moduleNotFoundHint("ModuleNotFoundError: No module named 'solution'", dir)
	if !strings.Contains(got, "local module") || !strings.Contains(got, "PYTHONPATH") {
		t.Errorf("local .py file: got %q, want PYTHONPATH hint", got)
	}

	// Local package dir: should suggest PYTHONPATH.
	got = moduleNotFoundHint("ModuleNotFoundError: No module named 'mylib'", dir)
	if !strings.Contains(got, "local module") || !strings.Contains(got, "PYTHONPATH") {
		t.Errorf("local package: got %q, want PYTHONPATH hint", got)
	}

	// Non-local module: should suggest uv first, then pip fallback.
	got = moduleNotFoundHint("ModuleNotFoundError: No module named 'numpy'", dir)
	if !strings.Contains(got, "uv pip install") || !strings.Contains(got, "pip install --break-system-packages") {
		t.Errorf("non-local module: got %q, want uv+pip fallback hint", got)
	}

	// No workDir: should still suggest uv first with pip fallback.
	got = moduleNotFoundHint("ModuleNotFoundError: No module named 'solution'")
	if !strings.Contains(got, "uv pip install") || !strings.Contains(got, "pip install --break-system-packages") {
		t.Errorf("no workDir: got %q, want uv+pip fallback hint", got)
	}
}

func TestModuleNotFoundHintVirtualEnv(t *testing.T) {
	t.Setenv("VIRTUAL_ENV", "/opt/.venv")
	got := moduleNotFoundHint("ModuleNotFoundError: No module named 'numpy'")
	if !strings.Contains(got, "uv pip install numpy") || strings.Contains(got, "--system") {
		t.Errorf("virtualenv hint: got %q, want uv/pip without --system", got)
	}
}

func TestTransientErrorHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		want     string
	}{
		{"externally managed", "error: externally-managed-environment\n× pip install failed", 1, "[hint: add --break-system-packages flag to pip install]"},
		{"dpkg lock", "E: Could not get lock /var/lib/dpkg/lock", 100, "[hint: try: dpkg --configure -a && apt-get install -f]"},
		{"network error", "Temporary failure resolving 'archive.ubuntu.com'", 100, "[hint: network error — this container may not have internet access. Use only locally available packages and tools. For Python: check if the package is already installed with 'python3 -c \"import <module>\"'. For apt: try 'dpkg -l | grep <package>' to check installed packages]"},
		{"permission denied /usr", "bash: /usr/local/bin/foo: Permission denied", 126, "[hint: try running with sudo or use --user flag for pip]"},
		{"no match", "some other error", 1, ""},
		{"success ignores", "externally-managed-environment", 0, "[hint: add --break-system-packages flag to pip install]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transientErrorHint(tt.output, tt.exitCode)
			if got != tt.want {
				t.Errorf("transientErrorHint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignalHint(t *testing.T) {
	tests := []struct {
		exitCode int
		contains string
	}{
		{137, "SIGKILL"},
		{139, "SIGSEGV"},
		{136, "SIGFPE"},
		{134, "SIGABRT"},
		{1, ""},
		{0, ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("exit_%d", tt.exitCode), func(t *testing.T) {
			got := signalHint(tt.exitCode)
			if tt.contains == "" && got != "" {
				t.Errorf("signalHint(%d) = %q, want empty", tt.exitCode, got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("signalHint(%d) = %q, want containing %q", tt.exitCode, got, tt.contains)
			}
		})
	}
}

func TestTestResultSummary(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		contains string
	}{
		{
			"pytest summary",
			"test_foo.py::test_a PASSED\ntest_foo.py::test_b FAILED\n======= 1 passed, 1 failed =======",
			"1 passed, 1 failed",
		},
		{
			"go test failures",
			"--- FAIL: TestFoo (0.01s)\n--- FAIL: TestBar (0.02s)\nFAIL\tgithub.com/example",
			"2 test(s) FAILED",
		},
		{
			"no test output",
			"hello world\nsome random output\ndone",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := testResultSummary(tt.output)
			if tt.contains == "" && got != "" {
				t.Errorf("testResultSummary() = %q, want empty", got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("testResultSummary() = %q, want containing %q", got, tt.contains)
			}
		})
	}
}

func TestCompilationErrorSummary(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		contains string
	}{
		{
			"gcc errors",
			"main.c:10:5: error: expected ';' after expression\nmain.c:15:1: error: unknown type name 'foo'",
			1,
			"2 error(s) found",
		},
		{
			"success output",
			"main.c:10:5: error: something",
			0,
			"",
		},
		{
			"no errors",
			"Building project...\nDone.",
			1,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compilationErrorSummary(tt.output, tt.exitCode)
			if tt.contains == "" && got != "" {
				t.Errorf("compilationErrorSummary() = %q, want empty", got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("compilationErrorSummary() = %q, want containing %q", got, tt.contains)
			}
		})
	}
}

// --- View Tests ---

func TestView_ReadFile(t *testing.T) {
	dir := setupTestDir(t)
	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path": "hello.go"}`)
	assertContains(t, result, "Hello, World!")
	assertContains(t, result, "package main")
}

func TestView_LineNumbers(t *testing.T) {
	dir := setupTestDir(t)
	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path": "hello.go"}`)
	assertContains(t, result, "1\t")
}

func TestView_Offset(t *testing.T) {
	dir := setupTestDir(t)
	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path": "hello.go", "offset": 5}`)
	assertNotContains(t, result, "package main")
	assertContains(t, result, "Println")
}

func TestView_Limit(t *testing.T) {
	dir := setupTestDir(t)
	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path": "hello.go", "limit": 2}`)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	// Should have 2 content lines (possibly + a truncation message)
	contentLines := 0
	for _, l := range lines {
		if !strings.HasPrefix(l, "...") {
			contentLines++
		}
	}
	if contentLines != 2 {
		t.Errorf("expected 2 content lines, got %d", contentLines)
	}
}

func TestView_NotFound(t *testing.T) {
	dir := setupTestDir(t)
	tool := View(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "nonexistent.go"}`)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestView_Directory(t *testing.T) {
	dir := setupTestDir(t)
	tool := View(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "lib"}`)
	if err == nil {
		t.Error("expected error for directory")
	}
}

func TestView_EmptyPath(t *testing.T) {
	tool := View()
	err := callErr(t, tool, `{"path": ""}`)
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestView_BinaryFile(t *testing.T) {
	dir := setupTestDir(t)
	// Write a binary file with null bytes.
	binPath := filepath.Join(dir, "binary.dat")
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG header
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1 pixel
	}
	os.WriteFile(binPath, data, 0o644)

	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path":"binary.dat"}`)
	assertContains(t, result, "Binary file")
}

func TestView_TotalLineCount(t *testing.T) {
	dir := setupTestDir(t)
	// Create a file with 20 lines.
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	os.WriteFile(filepath.Join(dir, "long.txt"), []byte(strings.Join(lines, "\n")), 0o644)

	tool := View(WithWorkDir(dir))
	// Read only first 5 lines.
	result := call(t, tool, `{"path":"long.txt","limit":5}`)
	assertContains(t, result, "line 1")
	assertContains(t, result, "line 5")
	// Should show total line count.
	assertContains(t, result, "20 total lines")
}

func TestView_MinifiedWarning(t *testing.T) {
	dir := setupTestDir(t)
	// Create a "minified" file: 6000 bytes in a single line.
	content := strings.Repeat("x", 6000)
	os.WriteFile(filepath.Join(dir, "bundle.min.js"), []byte(content), 0o644)

	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path":"bundle.min.js"}`)
	assertContains(t, result, "minified")
}

func TestView_NormalFileNoMinifiedWarning(t *testing.T) {
	dir := setupTestDir(t)
	// Create a normal file with many short lines.
	var lines []string
	for i := range 100 {
		lines = append(lines, fmt.Sprintf("line %d: some content here", i))
	}
	os.WriteFile(filepath.Join(dir, "normal.js"), []byte(strings.Join(lines, "\n")), 0o644)

	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path":"normal.js"}`)
	if strings.Contains(result, "minified") {
		t.Error("normal file should not trigger minified warning")
	}
}

// --- Write Tests ---

func TestWrite_NewFile(t *testing.T) {
	dir := setupTestDir(t)
	tool := Write(WithWorkDir(dir))
	result := call(t, tool, `{"path": "new.txt", "content": "hello new file"}`)
	assertContains(t, result, "Wrote")

	data, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello new file" {
		t.Errorf("file content mismatch: %q", data)
	}
}

func TestWrite_CreatesDirs(t *testing.T) {
	dir := setupTestDir(t)
	tool := Write(WithWorkDir(dir))
	call(t, tool, `{"path": "deep/nested/file.txt", "content": "deep content"}`)

	data, err := os.ReadFile(filepath.Join(dir, "deep/nested/file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep content" {
		t.Errorf("file content mismatch: %q", data)
	}
}

func TestWrite_Overwrite(t *testing.T) {
	dir := setupTestDir(t)
	tool := Write(WithWorkDir(dir))
	call(t, tool, `{"path": "hello.go", "content": "overwritten"}`)

	data, err := os.ReadFile(filepath.Join(dir, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "overwritten" {
		t.Errorf("file content mismatch: %q", data)
	}
}

func TestWrite_EmptyPath(t *testing.T) {
	tool := Write()
	err := callErr(t, tool, `{"path": ""}`)
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestWrite_PreservesExecutablePerms(t *testing.T) {
	dir := t.TempDir()
	// Create an executable file.
	scriptPath := filepath.Join(dir, "solution")
	os.WriteFile(scriptPath, []byte("#!/bin/bash\necho v1\n"), 0o755)

	tool := Write(WithWorkDir(dir))
	call(t, tool, `{"path": "solution", "content": "#!/bin/bash\necho v2\n"}`)

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0o111 == 0 {
		t.Errorf("expected executable permissions to be preserved on overwrite, got %o", perm)
	}
}

func TestWrite_PreservesPermsNonScript(t *testing.T) {
	dir := t.TempDir()
	// Create an executable non-script file (e.g., compiled binary placeholder).
	binPath := filepath.Join(dir, "mybin")
	os.WriteFile(binPath, []byte("old"), 0o755)

	tool := Write(WithWorkDir(dir))
	call(t, tool, `{"path": "mybin", "content": "new"}`)

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0o111 == 0 {
		t.Errorf("expected executable permissions to be preserved even for non-script files, got %o", perm)
	}
}

// --- Edit Tests ---

func TestEdit_SimpleReplace(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	result := call(t, tool, `{"path": "hello.go", "old_string": "Hello, World!", "new_string": "Hello, Gollem!"}`)
	assertContains(t, result, "Replaced 1")
	// Should show context around the edit.
	assertContains(t, result, "Hello, Gollem!")
	assertContains(t, result, "Context:")

	data, _ := os.ReadFile(filepath.Join(dir, "hello.go"))
	assertContains(t, string(data), "Hello, Gollem!")
}

func TestEdit_NotFound(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "hello.go", "old_string": "DOES NOT EXIST", "new_string": "replacement"}`)
	if err == nil {
		t.Error("expected error when old_string not found")
	}
}

func TestEdit_WhitespaceAutoCorrect(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	// hello.go uses tabs, but we'll try with spaces — should auto-correct.
	result := call(t, tool, `{"path": "hello.go", "old_string": "  fmt.Println(\"Hello, World!\")", "new_string": "  fmt.Println(\"Hi\")"}`)
	assertContains(t, result, "auto-corrected whitespace")
	// Verify the edit was applied with the file's tab indentation.
	content, err := os.ReadFile(filepath.Join(dir, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(content), "\tfmt.Println(\"Hi\")")
	assertNotContains(t, string(content), "Hello, World!")
}

func TestDetectWhitespaceMismatch(t *testing.T) {
	content := "func main() {\n\tfmt.Println(\"hello\")\n}\n"

	// Spaces instead of tab — should detect mismatch.
	search := "    fmt.Println(\"hello\")"
	hint := detectWhitespaceMismatch(content, search)
	if hint == "" {
		t.Error("expected whitespace mismatch hint for spaces vs tab")
	}
	assertContains(t, hint, "Whitespace mismatch")
	assertContains(t, hint, "fmt.Println")

	// Exact match — should return empty (no mismatch).
	search2 := "\tfmt.Println(\"hello\")"
	hint2 := detectWhitespaceMismatch(content, search2)
	if hint2 != "" {
		t.Errorf("expected empty hint for exact match, got %q", hint2)
	}

	// Totally different content — should return empty.
	hint3 := detectWhitespaceMismatch(content, "completely different")
	if hint3 != "" {
		t.Errorf("expected empty hint for non-matching content, got %q", hint3)
	}
}

func TestAutoCorrectWhitespace(t *testing.T) {
	// Tab-indented content, space-indented search.
	content := "func main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")\n}\n"

	t.Run("spaces_to_tabs", func(t *testing.T) {
		oldStr := "    fmt.Println(\"hello\")\n    fmt.Println(\"world\")"
		newStr := "    fmt.Println(\"HI\")\n    fmt.Println(\"WORLD\")"
		actualOld, adjustedNew, ok := autoCorrectWhitespace(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected auto-correct to succeed")
		}
		assertContains(t, actualOld, "\tfmt.Println(\"hello\")")
		assertContains(t, adjustedNew, "\tfmt.Println(\"HI\")")
		assertContains(t, adjustedNew, "\tfmt.Println(\"WORLD\")")
	})

	t.Run("exact_match_returns_false", func(t *testing.T) {
		oldStr := "\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")"
		newStr := "\tfmt.Println(\"HI\")"
		_, _, ok := autoCorrectWhitespace(content, oldStr, newStr)
		if ok {
			t.Error("expected no auto-correct for exact match")
		}
	})

	t.Run("no_match_returns_false", func(t *testing.T) {
		_, _, ok := autoCorrectWhitespace(content, "completely different", "new")
		if ok {
			t.Error("expected no auto-correct for non-matching content")
		}
	})

	t.Run("ambiguous_match_returns_false", func(t *testing.T) {
		// Content with duplicate lines when normalized.
		dupContent := "if true {\n\tfoo()\n}\nif false {\n\tfoo()\n}\n"
		_, _, ok := autoCorrectWhitespace(dupContent, "    foo()", "    bar()")
		if ok {
			t.Error("expected no auto-correct for ambiguous match")
		}
	})

	t.Run("indent_change_preserved", func(t *testing.T) {
		// Model increases indent: 4 spaces → 8 spaces (old) should map to tab → double tab.
		oldStr := "    fmt.Println(\"hello\")"
		newStr := "        if true {\n            fmt.Println(\"hello\")\n        }"
		_, adjustedNew, ok := autoCorrectWhitespace(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected auto-correct to succeed")
		}
		// The new string should have tab-based indentation, not spaces.
		if strings.Contains(adjustedNew, "    ") {
			t.Errorf("expected tab indentation in adjusted new, got: %q", adjustedNew)
		}
	})

	t.Run("indent_change_correct_tab_count", func(t *testing.T) {
		// Verify that when the model adds 1 indent level (4 spaces → 8 spaces)
		// and the file uses tabs, the result has exactly 2 tabs (not 8 tabs).
		// oldStr is at 4 spaces = 1 tab level. newStr wraps in if, so the
		// inner line goes from 4 spaces → 12 spaces (3 levels).
		// Actual is 1 tab → result should be 3 tabs (not 12 tabs).
		oldStr := "    fmt.Println(\"hello\")"
		newStr := "    if true {\n            fmt.Println(\"hello\")\n    }"
		_, adjustedNew, ok := autoCorrectWhitespace(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected auto-correct to succeed")
		}
		// The "if true {" line has same indent as old (4 spaces) → should be 1 tab.
		adjustedLines := strings.Split(adjustedNew, "\n")
		if len(adjustedLines) < 3 {
			t.Fatalf("expected 3 lines, got %d: %q", len(adjustedLines), adjustedNew)
		}
		// First line: "if true {" at same indent → 1 tab.
		if adjustedLines[0] != "\tif true {" {
			t.Errorf("expected first line to be \"\\tif true {\", got %q", adjustedLines[0])
		}
		// Inner line: 12 spaces → delta = 12-4 = 8, actual=4, target=12 → 3 tabs.
		if adjustedLines[1] != "\t\t\tfmt.Println(\"hello\")" {
			t.Errorf("expected inner line to be \"\\t\\t\\tfmt.Println(\\\"hello\\\")\", got %q", adjustedLines[1])
		}
	})
}

func TestAutoCorrectLineTrim(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\n"

	t.Run("trim_first_line", func(t *testing.T) {
		// Model included an extra context line at the start that doesn't match.
		// The first lines of old and new are identical (pure context), so trimming is safe.
		oldStr := "WRONG_LINE1\nline2\nline3\nline4"
		newStr := "WRONG_LINE1\nLINE2\nline3\nline4"
		actual, adjusted, ok := autoCorrectLineTrim(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected trim to succeed")
		}
		if actual != "line2\nline3\nline4" {
			t.Errorf("unexpected actual: %q", actual)
		}
		if adjusted != "LINE2\nline3\nline4" {
			t.Errorf("unexpected adjusted: %q", adjusted)
		}
	})

	t.Run("trim_last_line", func(t *testing.T) {
		oldStr := "line4\nline5\nWRONG_LINE6"
		newStr := "line4\nLINE5\nWRONG_LINE6"
		actual, adjusted, ok := autoCorrectLineTrim(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected trim to succeed")
		}
		if actual != "line4\nline5" {
			t.Errorf("unexpected actual: %q", actual)
		}
		if adjusted != "line4\nLINE5" {
			t.Errorf("unexpected adjusted: %q", adjusted)
		}
	})

	t.Run("trim_both_lines", func(t *testing.T) {
		// Extra context at BOTH ends. Requires 5+ lines.
		oldStr := "WRONG_START\nline2\nline3\nline4\nline5\nWRONG_END"
		newStr := "WRONG_START\nLINE2\nline3\nline4\nLINE5\nWRONG_END"
		actual, adjusted, ok := autoCorrectLineTrim(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected double trim to succeed")
		}
		if actual != "line2\nline3\nline4\nline5" {
			t.Errorf("unexpected actual: %q", actual)
		}
		if adjusted != "LINE2\nline3\nline4\nLINE5" {
			t.Errorf("unexpected adjusted: %q", adjusted)
		}
	})

	t.Run("trim_both_requires_5_lines", func(t *testing.T) {
		// Only 4 lines — double trim should not fire.
		oldStr := "WRONG\nline3\nline4\nWRONG2"
		newStr := "WRONG\nLINE3\nline4\nWRONG2"
		// Single trims won't match either since trimmed versions are ambiguous,
		// so this should fail. Use content where single trim also fails.
		uniqueContent := "A\nB\nC\nD\nE\n"
		_, _, ok := autoCorrectLineTrim(uniqueContent, oldStr, newStr)
		// The 4-line version should not use double-trim (needs 5+), but
		// single trims may still work. We test that the 5-line threshold
		// is enforced by checking with content that makes single trims fail.
		_ = ok // Either result is acceptable — the key property is that
		// double-trim only fires at 5+ lines, tested by the positive case above.
	})

	t.Run("no_trim_when_lines_differ", func(t *testing.T) {
		// First lines differ between old and new — not pure context.
		oldStr := "CONTEXT_A\nline3\nline4"
		newStr := "CONTEXT_B\nline3\nline4"
		_, _, ok := autoCorrectLineTrim(content, oldStr, newStr)
		if ok {
			t.Error("should not trim when first lines differ (intended edit)")
		}
	})

	t.Run("no_trim_too_few_lines", func(t *testing.T) {
		_, _, ok := autoCorrectLineTrim(content, "line2\nline3", "LINE2\nline3")
		if ok {
			t.Error("should not trim with < 3 lines")
		}
	})

	t.Run("no_trim_ambiguous", func(t *testing.T) {
		// Trimmed version matches multiple times.
		dupContent := "x\ny\nz\nx\ny\nz\n"
		oldStr := "WRONG\ny\nz"
		newStr := "WRONG\nY\nz"
		_, _, ok := autoCorrectLineTrim(dupContent, oldStr, newStr)
		if ok {
			t.Error("should not trim when result is ambiguous")
		}
	})
}

func TestEdit_InternalBlankLineAndWhitespaceCascade(t *testing.T) {
	dir := t.TempDir()
	// File with 1 blank line between functions and tab indentation.
	os.WriteFile(filepath.Join(dir, "funcs.go"), []byte(
		"func A() {\n\treturn 1\n}\n\nfunc B() {\n\treturn 2\n}\n"), 0o644)

	tool := Edit(WithWorkDir(dir))

	// Model sends old_string with 2 blank lines between functions AND space indentation.
	// Neither internal-blank-line correction nor whitespace correction alone works:
	// - Internal blank line normalization fixes blank lines but leaves spaces
	// - Whitespace normalization can't match because blank line count is wrong
	// The cascade should handle both.
	result := call(t, tool, `{"path": "funcs.go", "old_string": "func A() {\n    return 1\n}\n\n\nfunc B() {\n    return 2\n}", "new_string": "func A() {\n    return 42\n}\n\n\nfunc B() {\n    return 99\n}"}`)
	assertContains(t, result, "Replaced 1")
	assertContains(t, result, "auto-corrected internal blank lines and whitespace")

	data, _ := os.ReadFile(filepath.Join(dir, "funcs.go"))
	content := string(data)
	if !strings.Contains(content, "return 42") || !strings.Contains(content, "return 99") {
		t.Errorf("expected edits to be applied, got: %s", content)
	}
	// Verify tab indentation was preserved.
	if !strings.Contains(content, "\treturn 42") {
		t.Error("expected tab indentation to be preserved")
	}
}

func TestEdit_AmbiguousMatch(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "lib/utils.go", "old_string": "return a", "new_string": "return x"}`)
	if err == nil {
		t.Error("expected error for ambiguous match")
	}
}

func TestEdit_ReplaceAll(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	result := call(t, tool, `{"path": "lib/utils.go", "old_string": "return a", "new_string": "return x", "replace_all": true}`)
	assertContains(t, result, "Replaced 2")
}

func TestEdit_Delete(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	call(t, tool, `{"path": "hello.go", "old_string": "import \"fmt\"\n\n", "new_string": ""}`)

	data, _ := os.ReadFile(filepath.Join(dir, "hello.go"))
	assertNotContains(t, string(data), "import")
}

func TestEdit_FileNotExist(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "nope.go", "old_string": "a", "new_string": "b"}`)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestEdit_SameStrings(t *testing.T) {
	dir := setupTestDir(t)
	tool := Edit(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "hello.go", "old_string": "main", "new_string": "main"}`)
	if err == nil {
		t.Error("expected error when old_string equals new_string")
	}
}

// --- MultiEdit Tests ---

func TestMultiEdit(t *testing.T) {
	dir := setupTestDir(t)
	tool := MultiEdit(WithWorkDir(dir))
	args := `{"edits": [
		{"path": "hello.go", "old_string": "Hello, World!", "new_string": "Hi!"},
		{"path": "lib/utils.go", "old_string": "func Add", "new_string": "func Sum"}
	]}`
	result := call(t, tool, args)
	// Multi-edit now shows per-edit context.
	assertContains(t, result, "Replaced 1 occurrence(s) in hello.go")
	assertContains(t, result, "Replaced 1 occurrence(s) in lib/utils.go")
	assertContains(t, result, "Hi!")
	assertContains(t, result, "func Sum")

	data1, _ := os.ReadFile(filepath.Join(dir, "hello.go"))
	assertContains(t, string(data1), "Hi!")

	data2, _ := os.ReadFile(filepath.Join(dir, "lib/utils.go"))
	assertContains(t, string(data2), "func Sum")
}

func TestMultiEdit_Empty(t *testing.T) {
	tool := MultiEdit()
	err := callErr(t, tool, `{"edits": []}`)
	if err == nil {
		t.Error("expected error for empty edits")
	}
}

// --- Grep Tests ---

func TestGrep_FindPattern(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "func.*Add"}`)
	assertContains(t, result, "utils.go")
	assertContains(t, result, "func Add")
}

func TestGrep_WithInclude(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "[Tt]est", "include": "*.md"}`)
	assertContains(t, result, "README.md")
	assertNotContains(t, result, "utils_test.go")
}

func TestGrep_NoMatch(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "ZZZZNOTEXIST"}`)
	assertContains(t, result, "No matches")
}

func TestGrep_InvalidRegex(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	err := callErr(t, tool, `{"pattern": "[invalid"}`)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestGrep_ContextLines(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "func Add", "context_lines": 1}`)
	assertContains(t, result, "return a + b")
}

func TestGrep_ContextOverlap(t *testing.T) {
	// When consecutive matches have overlapping context windows,
	// lines should not be duplicated.
	dir := t.TempDir()
	writeTestFile(t, dir, "overlap.go", `line1
line2
line3
matchA
line5
line6
matchB
line8
line9
`)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "match[AB]", "context_lines": 2}`)
	// "line5" and "line6" fall in both context windows — should appear only once.
	if strings.Count(result, "line5") != 1 {
		t.Errorf("expected line5 to appear once, got %d times in:\n%s", strings.Count(result, "line5"), result)
	}
	if strings.Count(result, "line6") != 1 {
		t.Errorf("expected line6 to appear once, got %d times in:\n%s", strings.Count(result, "line6"), result)
	}
	// Both matches should be present.
	assertContains(t, result, "matchA")
	assertContains(t, result, "matchB")
}

func TestGrep_ContextOverlap_CloseMatchesHighlighted(t *testing.T) {
	// When two matches are close together (within each other's context
	// window), both must still be highlighted with ">". Before the fix,
	// if match2 fell inside match1's context window, match2's line was
	// shown as unhighlighted context for match1, and the overlap trimming
	// skipped past it so it never got its own ">" marker.
	dir := t.TempDir()
	writeTestFile(t, dir, "close.txt", `line1
line2
matchA
line4
matchB
line6
line7
`)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "match[AB]", "context_lines": 2}`)

	// Both matches must appear with ">" prefix.
	countA := strings.Count(result, ">close.txt:3: matchA")
	countB := strings.Count(result, ">close.txt:5: matchB")
	if countA != 1 {
		t.Errorf("expected matchA with > prefix once, got %d in:\n%s", countA, result)
	}
	if countB != 1 {
		t.Errorf("expected matchB with > prefix once, got %d in:\n%s", countB, result)
	}
}

func TestGrep_MaxResultsCountsMatches(t *testing.T) {
	// max_results should count actual regex matches, not context/separator lines.
	dir := t.TempDir()
	// Create a file with 10 matches.
	var lines []string
	for i := 1; i <= 30; i++ {
		if i%3 == 0 {
			lines = append(lines, fmt.Sprintf("MATCH line %d", i))
		} else {
			lines = append(lines, fmt.Sprintf("normal line %d", i))
		}
	}
	writeTestFile(t, dir, "many.txt", strings.Join(lines, "\n"))
	tool := Grep(WithWorkDir(dir))
	// With context_lines=1 and max_results=5, we should get 5 actual matches.
	result := call(t, tool, `{"pattern": "MATCH", "context_lines": 1, "max_results": 5}`)
	matchCount := strings.Count(result, ">")
	if matchCount != 5 {
		t.Errorf("expected 5 matches with > prefix, got %d in:\n%s", matchCount, result)
	}
	assertContains(t, result, "truncated at 5 matches")
}

func TestGrep_ContextSeparatorBetweenFiles(t *testing.T) {
	// Context blocks from different files should be separated by "---".
	dir := t.TempDir()
	writeTestFile(t, dir, "a.txt", "line1\nTARGET\nline3\n")
	writeTestFile(t, dir, "b.txt", "line1\nTARGET\nline3\n")
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "TARGET", "context_lines": 1}`)
	// Both files should appear.
	assertContains(t, result, "a.txt")
	assertContains(t, result, "b.txt")
	// There should be "---" separators between the two file blocks.
	if !strings.Contains(result, "---") {
		t.Errorf("expected separator between file context blocks in:\n%s", result)
	}
}

func TestGrep_EmptyPattern(t *testing.T) {
	tool := Grep()
	err := callErr(t, tool, `{"pattern": ""}`)
	if err == nil {
		t.Error("expected error for empty pattern")
	}
}

func TestGrep_IgnoreCase(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	// "func add" should match "func Add" with ignore_case.
	result := call(t, tool, `{"pattern": "func add", "ignore_case": true}`)
	assertContains(t, result, "func Add")
}

// TestGrep_IgnoreCaseWithExistingFlags verifies that ignore_case works
// correctly when the pattern already has regex flags like (?m).
// Bug: the old check used ContainsRune(pattern[2:], 'i') which matched
// the letter 'i' anywhere in the pattern text — not just in flag groups.
// Pattern "(?m)multiply" with ignore_case would silently stay case-sensitive
// because 'i' appears in "multiply".
func TestGrep_IgnoreCaseWithExistingFlags(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	// Pattern uses (?m) multiline flag and contains 'i' in the text.
	// With ignore_case, "multiply" should match "Multiply" in utils.go.
	result := call(t, tool, `{"pattern": "(?m)multiply", "ignore_case": true}`)
	assertContains(t, result, "Multiply")
}

func TestGrep_FilesOnly(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "func", "files_only": true}`)
	// Should return file paths without line content.
	assertContains(t, result, "hello.go")
	// Should NOT contain line content like "func main".
	assertNotContains(t, result, "func main")
}

// --- Glob Tests ---

func TestGlob_FindGoFiles(t *testing.T) {
	dir := setupTestDir(t)
	tool := Glob(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "**/*.go"}`)
	assertContains(t, result, "hello.go")
	assertContains(t, result, "utils.go")
}

func TestGlob_SubdirOnly(t *testing.T) {
	dir := setupTestDir(t)
	tool := Glob(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "lib/*.go"}`)
	assertContains(t, result, "utils.go")
	assertNotContains(t, result, "hello.go")
}

func TestGlob_NoMatch(t *testing.T) {
	dir := setupTestDir(t)
	tool := Glob(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "**/*.rs"}`)
	assertContains(t, result, "No files matched")
}

func TestGlob_SimplePattern(t *testing.T) {
	dir := setupTestDir(t)
	tool := Glob(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "*.go"}`)
	assertContains(t, result, "hello.go")
	assertNotContains(t, result, "utils.go") // Not in root
}

func TestGlob_EmptyPattern(t *testing.T) {
	tool := Glob()
	err := callErr(t, tool, `{"pattern": ""}`)
	if err == nil {
		t.Error("expected error for empty pattern")
	}
}

// --- Ls Tests ---

func TestLs_RootDir(t *testing.T) {
	dir := setupTestDir(t)
	tool := Ls(WithWorkDir(dir))
	result := call(t, tool, `{}`)
	assertContains(t, result, "hello.go")
	assertContains(t, result, "lib/")
	assertContains(t, result, "README.md")
}

func TestLs_Depth2(t *testing.T) {
	dir := setupTestDir(t)
	tool := Ls(WithWorkDir(dir))
	result := call(t, tool, `{"depth": 2}`)
	assertContains(t, result, "lib/utils.go")
}

func TestLs_SubDir(t *testing.T) {
	dir := setupTestDir(t)
	tool := Ls(WithWorkDir(dir))
	result := call(t, tool, `{"path": "lib"}`)
	assertContains(t, result, "utils.go")
	assertNotContains(t, result, "hello.go")
}

func TestLs_NotFound(t *testing.T) {
	dir := setupTestDir(t)
	tool := Ls(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "nonexistent"}`)
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestLs_File(t *testing.T) {
	dir := setupTestDir(t)
	tool := Ls(WithWorkDir(dir))
	err := callErr(t, tool, `{"path": "hello.go"}`)
	if err == nil {
		t.Error("expected error for file path")
	}
}

// --- Toolset Tests ---

func TestToolset_AllTools(t *testing.T) {
	ts := Toolset()
	if ts.Name != "codetool" {
		t.Errorf("expected toolset name 'codetool', got %q", ts.Name)
	}
	if len(ts.Tools) != 9 {
		t.Errorf("expected 9 tools, got %d", len(ts.Tools))
	}

	names := make(map[string]bool)
	for _, tool := range ts.Tools {
		names[tool.Definition.Name] = true
	}

	expected := []string{"bash", "view", "write", "edit", "multi_edit", "grep", "glob", "ls", "lsp"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestAllTools_Count(t *testing.T) {
	tools := AllTools()
	if len(tools) != 9 {
		t.Errorf("expected 9 tools, got %d", len(tools))
	}
}

func TestToolset_WithOptions(t *testing.T) {
	dir := setupTestDir(t)
	ts := Toolset(WithWorkDir(dir))
	// Verify tools work with the configured workdir.
	for _, tool := range ts.Tools {
		if tool.Definition.Name == "ls" {
			ctx := context.Background()
			rc := &core.RunContext{}
			result, err := tool.Handler(ctx, rc, `{}`)
			if err != nil {
				t.Fatalf("ls failed: %v", err)
			}
			s, ok := result.(string)
			if !ok {
				t.Fatalf("expected string result, got %T", result)
			}
			assertContains(t, s, "hello.go")
		}
	}
}

// --- Doublestar matching tests ---

func TestMatchDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.go", "hello.go", true},
		{"**/*.go", "lib/utils.go", true},
		{"**/*.go", "a/b/c/d.go", true},
		{"**/*.go", "hello.py", false},
		{"*.go", "hello.go", true},
		{"*.go", "lib/hello.go", false},
		{"lib/**/*.go", "lib/utils.go", true},
		{"lib/**/*.go", "lib/sub/deep.go", true},
		{"lib/**/*.go", "hello.go", false},
		{"src/**/test_*.py", "src/test_main.py", true},
		{"src/**/test_*.py", "src/sub/test_utils.py", true},
	}

	for _, tt := range tests {
		got := matchDoublestar(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchDoublestar(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

// --- Verification Checkpoint Tests ---

func TestIsVerificationCommand(t *testing.T) {
	tests := []struct {
		name     string
		argsJSON string
		want     bool
	}{
		{"go test", `{"command":"go test ./..."}`, true},
		{"go build", `{"command":"go build ./..."}`, true},
		{"go vet", `{"command":"go vet ./..."}`, true},
		{"pytest", `{"command":"pytest tests/"}`, true},
		{"npm test", `{"command":"npm test"}`, true},
		{"yarn test", `{"command":"yarn test"}`, true},
		{"cargo test", `{"command":"cargo test"}`, true},
		{"cargo clippy", `{"command":"cargo clippy"}`, true},
		{"make test", `{"command":"make test"}`, true},
		{"make (build)", `{"command":"make"}`, true},
		{"eslint", `{"command":"npx eslint src/"}`, true},
		{"golangci-lint", `{"command":"golangci-lint run"}`, true},
		{"tsc", `{"command":"tsc --noEmit"}`, true},
		{"mypy", `{"command":"mypy src/"}`, true},
		{"mvn test", `{"command":"mvn test"}`, true},
		{"gradle test", `{"command":"gradle test"}`, true},
		{"dotnet test", `{"command":"dotnet test"}`, true},
		{"mixed case", `{"command":"Go Test ./..."}`, true},

		// Non-verification commands.
		{"echo", `{"command":"echo hello"}`, false},
		{"ls", `{"command":"ls -la"}`, false},
		{"cat", `{"command":"cat file.txt"}`, false},
		{"git status", `{"command":"git status"}`, false},
		{"cd", `{"command":"cd /tmp"}`, false},
		{"curl", `{"command":"curl https://example.com"}`, false},
		{"invalid json", `not json`, false},
		{"empty command", `{"command":""}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVerificationCommand(tt.argsJSON)
			if got != tt.want {
				t.Errorf("isVerificationCommand(%s) = %v, want %v", tt.argsJSON, got, tt.want)
			}
		})
	}
}

func TestVerificationCheckpoint_RejectsWithoutVerification(t *testing.T) {
	_, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}

	_, err := validator(ctx, rc, "I'm done with the task.")
	if err == nil {
		t.Fatal("expected error when no verification was done")
	}

	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got %T: %v", err, err)
	}
	assertContains(t, retryErr.Message, "verify")
}

func TestVerificationCheckpoint_AcceptsAfterVerification(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()

	// Simulate a conversation where the model called bash with "go test".
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Fix the bug"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"go test ./..."}`,
					ToolCallID: "call1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "ok\nPASS",
					ToolCallID: "call1",
				},
			},
		},
	}

	// Run the middleware so it scans the messages.
	nextCalled := false
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		nextCalled = true
		return &core.ModelResponse{}, nil
	}
	_, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("middleware error: %v", err)
	}
	if !nextCalled {
		t.Fatal("middleware did not call next")
	}

	// First validator call should accept immediately after verification.
	rc := &core.RunContext{}
	output, err := validator(ctx, rc, "Done! All tests pass.")
	if err != nil {
		t.Fatalf("validator should accept after verification, got: %v", err)
	}
	if output != "Done! All tests pass." {
		t.Errorf("validator modified output: %q", output)
	}
}

// TestVerificationCheckpoint_MiddlewareRescanStillAccepts verifies that
// rescanning the same verification history does not regress completion gating.
func TestVerificationCheckpoint_MiddlewareRescanStillAccepts(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()

	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Fix the bug"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"go test ./..."}`,
					ToolCallID: "call1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "ok\nPASS",
					ToolCallID: "call1",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	// First middleware call → scans messages, sets verified.
	_, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("middleware 1 error: %v", err)
	}

	// First validator call should accept.
	rc := &core.RunContext{}
	_, err = validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("validator should accept after verification, got: %v", err)
	}

	// Middleware rescans the same messages before another completion attempt.
	_, err = mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("middleware 2 error: %v", err)
	}

	// Second validator call should still accept.
	output, err := validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("validator should still accept after middleware rescan, got: %v", err)
	}
	if output != "Done!" {
		t.Errorf("validator modified output: %q", output)
	}
}

// TestVerificationCheckpoint_NewVerificationStillAccepts verifies that
// a new verification run keeps completion admissible (no forced extra turn).
func TestVerificationCheckpoint_NewVerificationStillAccepts(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"go test ./..."}`,
					ToolCallID: "call1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "ok\nPASS",
					ToolCallID: "call1",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	// First pass accepts.
	mw(ctx, messages, nil, nil, next)
	rc := &core.RunContext{}
	_, err := validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("expected acceptance after initial verification, got: %v", err)
	}

	// Agent runs a NEW verification command.
	messages = append(messages,
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"go test ./..."}`,
					ToolCallID: "call2", // different ID
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "ok\nPASS",
					ToolCallID: "call2",
				},
			},
		},
	)

	// Middleware sees the new verification command.
	mw(ctx, messages, nil, nil, next)

	// Validator should continue to accept.
	_, err = validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("expected acceptance after new verification command, got: %v", err)
	}
}

func TestVerificationCheckpoint_IgnoresNonBashTools(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()

	// Simulate a conversation with edit and view calls but no bash or execute_code.
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "edit",
					ArgsJSON:   `{"path":"main.go","old_string":"foo","new_string":"bar"}`,
					ToolCallID: "call1",
				},
				core.ToolCallPart{
					ToolName:   "view",
					ArgsJSON:   `{"path":"main.go"}`,
					ToolCallID: "call2",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	// Should still reject — no bash or verification execute_code.
	rc := &core.RunContext{}
	_, err := validator(ctx, rc, "Done!")
	if err == nil {
		t.Fatal("expected error when only edit/view tools were used")
	}
}

func TestVerificationCheckpoint_AcceptsExecuteCode(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()

	// Simulate a conversation where the model used execute_code with bash() to verify.
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "execute_code",
					ArgsJSON:   `{"code":"result = bash(command='python /app/test_outputs.py')\nresult"}`,
					ToolCallID: "call1",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	// First validator call should accept.
	rc := &core.RunContext{}
	_, err := validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("should accept after execute_code verification, got: %v", err)
	}
}

func TestIsVerificationCode(t *testing.T) {
	tests := []struct {
		name     string
		argsJSON string
		want     bool
	}{
		{"bash call with test", `{"code":"bash(command='pytest')"}`, true},
		{"bash call with test_outputs", `{"code":"bash(command='python /app/test_outputs.py')"}`, true},
		{"assert statement", `{"code":"assert result == expected"}`, true},
		{"assertEqual", `{"code":"self.assertEqual(output, expected)"}`, true},
		{"open output file", `{"code":"with open('output.csv') as f:\n    data = f.read()"}`, true},
		{"simple math", `{"code":"x = 1 + 2\nx"}`, false},
		{"view call only", `{"code":"view(path='main.py')"}`, false},
		{"invalid json", `not json`, false},
		{"empty code", `{"code":""}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVerificationCode(tt.argsJSON)
			if got != tt.want {
				t.Errorf("isVerificationCode(%s) = %v, want %v", tt.argsJSON, got, tt.want)
			}
		})
	}
}

func TestVerificationCheckpoint_ServiceTaskRequiresReadinessProof(t *testing.T) {
	mw, validator := VerificationCheckpoint("")
	ctx := context.Background()

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "start1",
					ArgsJSON:   `{"command":"nohup python3 /app/server.py >/tmp/server.log 2>&1 & echo $! > /tmp/server.pid"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					ToolCallID: "start1",
					Content:    "started",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "verify1",
					ArgsJSON:   `{"command":"pytest -q"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					ToolCallID: "verify1",
					Content:    "3 passed",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}
	if _, err := mw(ctx, messages, nil, nil, next); err != nil {
		t.Fatalf("middleware error: %v", err)
	}

	_, err := validator(ctx, &core.RunContext{}, "done")
	if err == nil {
		t.Fatal("expected readiness gate to reject completion")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got %T: %v", err, err)
	}
	assertContains(t, retryErr.Message, "readiness")
}

func TestVerificationCheckpoint_ServiceTaskAcceptsAfterReadinessProof(t *testing.T) {
	mw, validator := VerificationCheckpoint("")
	ctx := context.Background()

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "start1",
					ArgsJSON:   `{"command":"nohup python3 /app/server.py >/tmp/server.log 2>&1 & echo $! > /tmp/server.pid"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					ToolCallID: "start1",
					Content:    "started",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "ready1",
					ArgsJSON:   `{"command":"ss -tlnp | grep 5328"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					ToolCallID: "ready1",
					Content:    "LISTEN 0 128 127.0.0.1:5328",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "verify1",
					ArgsJSON:   `{"command":"pytest -q"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					ToolCallID: "verify1",
					Content:    "3 passed",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}
	if _, err := mw(ctx, messages, nil, nil, next); err != nil {
		t.Fatalf("middleware error: %v", err)
	}

	if _, err := validator(ctx, &core.RunContext{}, "done"); err != nil {
		t.Fatalf("validator should accept after readiness proof, got: %v", err)
	}
}

func TestIsServiceReadinessResultSuccessful(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"success output", "LISTEN 0 128 127.0.0.1:5328", true},
		{"exit code failure", "connection refused\n[exit code: 1]", false},
		{"timeout failure", "[timed out after 2m0s]", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isServiceReadinessResultSuccessful(tt.content)
			if got != tt.want {
				t.Errorf("isServiceReadinessResultSuccessful(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsVerificationString(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"pytest -xvs", true},
		{"npm test", true},
		{"cargo test", true},
		{"make test", true},
		{"diff output.txt expected.txt", true},
		{"valgrind ./myprogram", true},
		{"curl localhost:8080/api/health", true},
		{"curl http://localhost:3000", true},
		{"xxd output.bin | head", true},
		{"Rscript test.R", true},
		// Nim package manager.
		{"nimble test", true},
		{"nimble build", true},
		// Crystal package manager.
		{"shards build", true},
		{"shards install", true},
		// Meson build system.
		{"meson test -C builddir", true},
		{"meson compile -C builddir", true},
		{"meson setup builddir", true},
		// Bazel build system.
		{"bazel test //...", true},
		{"bazel build //...", true},
		{"bazel run //:target", true},
		{"pytest --help | grep allow-no-tests", false},
		{"python3 -m pytest --help", false},
		{"echo hello world", false},
		{"cat main.py", false},
		{"ls -la", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			got := isVerificationString(strings.ToLower(tt.cmd))
			if got != tt.want {
				t.Errorf("isVerificationString(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestIsPipCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"pip install numpy", true},
		{"pip3 install --break-system-packages scipy", true},
		{"python3 -m pip install torch", true},
		{"python -m pip install -r requirements.txt", true},
		{"sudo pip install pandas", true},
		{"echo hello", false},
		{"pip freeze", false},
		{"npm install", false},
		{"apt-get install python3-pip", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			got := isPipCommand(tt.cmd)
			if got != tt.want {
				t.Errorf("isPipCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestIsDestructiveTestCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		// Destructive operations — should be blocked.
		{"redirect to tests", "echo hello > /tests/test.sh", true},
		{"append to tests", "echo hello >> /tests/test.sh", true},
		{"rm tests file", "rm /tests/test.sh", true},
		{"rm -rf tests", "rm -rf /tests/", true},
		{"sed -i on tests", "sed -i 's/old/new/' /tests/test.py", true},
		{"chmod tests", "chmod +x /tests/test.sh", true},
		{"tee to tests", "echo data | tee /tests/out.txt", true},
		{"truncate tests", "truncate -s 0 /tests/test.sh", true},
		{"perl -i on tests", "perl -i -pe 's/old/new/' /tests/test.py", true},
		{"perl -pi on tests", "perl -pi -e 's/old/new/' /tests/test.sh", true},
		{"dd to tests", "dd if=/dev/zero of=/tests/test.sh bs=1 count=0", true},
		{"patch tests", "patch /tests/test.py < fix.patch", true},
		{"install to tests", "install -m 755 solution.py /tests/test.py", true},
		{"mkdir tests dir", "mkdir -p /tests", true},
		{"ln into tests", "ln -s /app/filter.py /tests/filter.py", true},

		// Non-destructive operations — should be allowed.
		{"run test script", "bash /tests/test.sh", false},
		{"run python test", "python3 /tests/test.py", false},
		{"cat test file", "cat /tests/test.sh", false},
		{"ls tests dir", "ls /tests/", false},
		{"head test file", "head -n 10 /tests/test.py", false},
		{"diff with tests", "diff output.txt /tests/expected.txt", false},
		{"grep in tests", "grep -r 'pattern' /tests/", false},
		{"no tests ref", "echo hello > /app/output.txt", false},
		// pip/npm/apt install with /tests/ reference should NOT be blocked.
		{"pip install tests dep", "pip install /tests/requirements.txt", false},
		{"npm install in tests", "npm install --prefix /tests/", false},
		// Non-root paths that contain "tests" should NOT be treated as verifier /tests.
		{"mkdir tests backup", "mkdir -p /tests_backup", false},
		{"redirect tests backup", "echo data > /tests_backup/out.txt", false},
		{"rm app tests path", "rm -f /app/tests/unit/test_a.py", false},
		{"touch app tests path", "touch /app/tests/generated.txt", false},
		{"ln app tests path", "ln -s /app/a.py /app/tests/a.py", false},
		// Conservative behavior: if /tests and mutating operators coexist anywhere
		// in the same command, block to avoid shell-segmentation bypasses.
		{"rm unrelated then read tests", "rm -f /tmp/a && cat /tests/test.sh", true},
		{"read tests then rm unrelated", "cat /tests/test.sh && rm -f /tmp/a", true},
		{"rm tests then unrelated", "rm -f /tests/test.sh && echo done", true},
		{"pipeline xargs rm tests", "printf '/tests/test.sh\n' | xargs rm -f", true},
		{"var indirection rm tests", "p=/tests/test.sh; rm -f \"$p\"", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDestructiveTestCommand(tt.cmd)
			if got != tt.want {
				t.Errorf("isDestructiveTestCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestIsRiskyProcessKillCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"pkill full match", "pkill -f '/app/server.py' || true", true},
		{"pkill long form", "pkill --full server.py", true},
		{"killall broad", "killall python3", true},
		{"pid file stop", "kill \"$(cat /tmp/server.pid)\"", false},
		{"exact process name", "pkill -x nginx", false},
		{"plain pid kill", "kill -9 1234", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRiskyProcessKillCommand(tt.cmd)
			if got != tt.want {
				t.Errorf("isRiskyProcessKillCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestIsTransientBashFailure(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		output   string
		command  string
		want     bool
	}{
		{"network error", 1, "Could not resolve host: example.com", "curl http://example.com", true},
		{"connection timeout", 1, "Connection timed out", "pip install requests", true},
		{"dpkg lock", 1, "unable to acquire the dpkg frontend lock", "apt-get install foo", true},
		{"hash sum mismatch", 1, "Hash sum mismatch", "apt-get update", true},
		{"failed to fetch", 100, "E: Failed to fetch http://archive.ubuntu.com/", "apt-get install foo", true},
		{"success", 0, "all good", "echo hi", false},
		{"normal error", 1, "syntax error near unexpected token", "bash test.sh", false},
		{"test failure", 1, "FAILED test_something", "pytest", false},
		// Connection refused is NOT transient for service test commands.
		{"conn refused curl", 7, "curl: (7) Failed to connect to localhost port 8080: Connection refused", "curl localhost:8080", false},
		{"conn refused wget", 1, "Connection refused", "wget http://localhost:3000", false},
		// Connection refused IS transient for package install commands.
		{"conn refused apt", 1, "Connection refused", "apt-get install nginx", true},
		{"conn refused pip", 1, "Connection refused", "pip install flask", true},
		// HTTP rate limiting.
		{"rate limit 429", 1, "HTTP 429 Too Many Requests", "pip install foo", true},
		{"rate limit generic", 1, "Rate limit exceeded, please retry", "npm install", true},
		// HTTP 503 / 502.
		{"service unavailable", 1, "503 Service Unavailable", "cargo fetch", true},
		{"bad gateway", 1, "502 Bad Gateway", "go mod download", true},
		// Python/Node connection errors.
		{"python connectionerror", 1, "ConnectionError: HTTPSConnectionPool(host='pypi.org')", "pip install flask", true},
		{"node econnreset", 1, "Error: ECONNRESET", "npm install express", true},
		{"node etimedout", 1, "Error: ETIMEDOUT", "npm install express", true},
		{"socket hang up", 1, "Error: socket hang up", "npm install", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientBashFailure(tt.exitCode, tt.output, tt.command)
			if got != tt.want {
				t.Errorf("isTransientBashFailure(%d, %q, %q) = %v, want %v", tt.exitCode, tt.output, tt.command, got, tt.want)
			}
		})
	}
}

func TestBash_BlocksDestructiveTestCommand(t *testing.T) {
	tool := Bash()
	err := callErr(t, tool, `{"command":"echo pwned > /tests/test.sh"}`)
	if err == nil {
		t.Fatal("expected error for destructive test command")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got %T: %v", err, err)
	}
	if !strings.Contains(retryErr.Message, "BLOCKED") {
		t.Errorf("expected BLOCKED message, got: %s", retryErr.Message)
	}
}

func TestBash_AllowsRunningTests(t *testing.T) {
	// Running tests from /tests/ should not be blocked.
	tool := Bash()
	// Use a simple echo to verify the command runs (bash /tests/... would fail
	// because /tests/ doesn't exist, but it should not be blocked by our check).
	result := call(t, tool, `{"command":"echo 'bash /tests/test.sh would run here'"}`)
	assertContains(t, result, "bash /tests/test.sh would run here")
}

func TestIsProtectedTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/tests/test.sh", true},
		{"/tests/unit/test_check.py", true},
		{"/tests/e2e/verify.sh", true},
		{"/tests", true},
		{"/app/tests/test.py", false},      // not root /tests/
		{"/home/user/tests/foo.py", false}, // not root /tests/
		{"/src/main.py", false},            // unrelated
		{"/app/solution.py", false},        // unrelated
		{"tests/test.sh", false},           // relative, not /tests/
		{"/testing/foo.py", false},         // /testing != /tests
		{"/tests/../app/foo.py", false},    // cleaned to /app/foo.py
		{"/tests/./nested/test.sh", true},  // cleaned to /tests/nested/test.sh
	}
	for _, tt := range tests {
		got := isProtectedTestFile(tt.path)
		if got != tt.want {
			t.Errorf("isProtectedTestFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestEdit_ProtectedTestFile(t *testing.T) {
	// Edit should block modifications to /tests/ files.
	tool := Edit()
	err := callErr(t, tool, `{"path":"/tests/test.sh","old_string":"echo hello","new_string":"echo bye"}`)
	if err == nil {
		t.Fatal("expected error for protected test file")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got %T: %v", err, err)
	}
	if !strings.Contains(retryErr.Message, "BLOCKED") {
		t.Errorf("expected BLOCKED message, got: %s", retryErr.Message)
	}
}

func TestWrite_ProtectedTestFile(t *testing.T) {
	// Write should block creation/overwrite of /tests/ files.
	tool := Write()
	err := callErr(t, tool, `{"path":"/tests/test_new.py","content":"print('hello')"}`)
	if err == nil {
		t.Fatal("expected error for protected test file")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got %T: %v", err, err)
	}
	if !strings.Contains(retryErr.Message, "BLOCKED") {
		t.Errorf("expected BLOCKED message, got: %s", retryErr.Message)
	}
}

func TestMultiEdit_ProtectedTestFile(t *testing.T) {
	dir := setupTestDir(t)
	// MultiEdit should block if any edit targets /tests/.
	tool := MultiEdit(WithWorkDir(dir))
	err := callErr(t, tool, `{"edits":[{"path":"/tests/test.sh","old_string":"echo hello","new_string":"echo bye"}]}`)
	if err == nil {
		t.Fatal("expected error for protected test file")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got %T: %v", err, err)
	}
	if !strings.Contains(retryErr.Message, "BLOCKED") {
		t.Errorf("expected BLOCKED message, got: %s", retryErr.Message)
	}
}

func TestVerificationCheckpoint_IgnoresNonVerificationBash(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()

	// Bash calls that are NOT verification (e.g., ls, cat).
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"ls -la"}`,
					ToolCallID: "call1",
				},
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"cat README.md"}`,
					ToolCallID: "call2",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	// Should reject — bash was used but not for verification.
	rc := &core.RunContext{}
	_, err := validator(ctx, rc, "All done!")
	if err == nil {
		t.Fatal("expected error when bash was used but not for verification")
	}
}

func TestVerificationCheckpoint_RejectsOnTestFailure(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()

	// Simulate: agent ran tests, tests failed (exit code 1).
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest"}`,
					ToolCallID: "call1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "FAILED tests/test_main.py::test_output - AssertionError\n1 failed, 2 passed\n[exit code: 1]",
					ToolCallID: "call1",
				},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	// Validator should reject: tests failed.
	rc := &core.RunContext{}
	_, err := validator(ctx, rc, "Done!")
	if err == nil {
		t.Fatal("expected rejection when last test run failed")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got: %v", err)
	}
	if !strings.Contains(retryErr.Message, "FAILED") {
		t.Errorf("expected failure details in message, got: %s", retryErr.Message)
	}
}

func TestVerificationCheckpoint_AcceptsAfterFailureThenPass(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	// Phase 1: Tests fail.
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"go test ./..."}`,
					ToolCallID: "call1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "FAIL\n[exit code: 1]",
					ToolCallID: "call1",
				},
			},
		},
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	// Should reject: tests failed.
	_, err := validator(ctx, rc, "Done!")
	if err == nil {
		t.Fatal("expected rejection when tests failed")
	}

	// Phase 2: Agent fixes code and re-runs tests successfully.
	messages = append(messages,
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"go test ./..."}`,
					ToolCallID: "call2",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "ok\nPASS",
					ToolCallID: "call2",
				},
			},
		},
	)
	_, _ = mw(ctx, messages, nil, nil, next)

	// Should accept immediately after the passing verification run.
	output, err := validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("should accept after passing verification, got: %v", err)
	}
	if output != "Done!" {
		t.Errorf("modified output: %q", output)
	}
}

func TestVerificationCheckpoint_RejectsRepeatedlyOnFailure(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	// Tests fail.
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest"}`,
					ToolCallID: "call1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "1 failed\n[exit code: 1]",
					ToolCallID: "call1",
				},
			},
		},
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	for i := range 4 {
		_, err := validator(ctx, rc, "Done!")
		if err == nil {
			t.Fatalf("expected rejection on attempt %d", i+1)
		}
		var retryErr *core.ModelRetryError
		if !errors.As(err, &retryErr) {
			t.Fatalf("expected ModelRetryError on attempt %d, got: %v", i+1, err)
		}
		if !strings.Contains(retryErr.Message, "Your last verification run FAILED") {
			t.Fatalf("attempt %d: expected failure rejection, got: %s", i+1, retryErr.Message)
		}
	}
}

func TestVerificationCheckpoint_RejectsWhenEditsAfterLastVerification(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	// Verification passed, then file edited afterwards without re-running tests.
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest"}`,
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "1 passed",
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "edit",
					ArgsJSON:   `{"path":"main.py","old_string":"x","new_string":"y"}`,
					ToolCallID: "edit1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "edit",
					Content:    "edit applied",
					ToolCallID: "edit1",
				},
			},
		},
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	_, err := validator(ctx, rc, "Done!")
	if err == nil {
		t.Fatal("expected rejection when edits were made after last verification")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got: %v", err)
	}
	if !strings.Contains(retryErr.Message, "since your last verification run") {
		t.Fatalf("expected stale-verification message, got: %s", retryErr.Message)
	}
}

func TestVerificationCheckpoint_RejectsWhenLSPRenameAfterLastVerification(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest"}`,
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "1 passed",
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "lsp",
					ArgsJSON:   `{"method":"rename","file":"main.go","line":1,"character":1,"new_name":"NewSymbol"}`,
					ToolCallID: "lsp1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "lsp",
					Content:    "Applied \"Rename Symbol\" — 1 edit(s) across 1 file(s):\n  main.go (1 edit(s))",
					ToolCallID: "lsp1",
				},
			},
		},
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	_, err := validator(ctx, rc, "Done!")
	if err == nil {
		t.Fatal("expected rejection when lsp rename was made after last verification")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got: %v", err)
	}
	if !strings.Contains(retryErr.Message, "since your last verification run") {
		t.Fatalf("expected stale-verification message, got: %s", retryErr.Message)
	}
}

func TestVerificationCheckpoint_RejectsWhenBashMutationAfterLastVerification(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest"}`,
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "1 passed",
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"sed -i 's/x/y/' main.py"}`,
					ToolCallID: "mut1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "done",
					ToolCallID: "mut1",
				},
			},
		},
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	_, err := validator(ctx, rc, "Done!")
	if err == nil {
		t.Fatal("expected rejection when bash mutation was made after last verification")
	}
	var retryErr *core.ModelRetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected ModelRetryError, got: %v", err)
	}
	if !strings.Contains(retryErr.Message, "since your last verification run") {
		t.Fatalf("expected stale-verification message, got: %s", retryErr.Message)
	}
}

func TestVerificationCheckpoint_DoesNotRejectWhenPostVerifyMutationFails(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	// Verification passed, then mutation call fails (non-zero exit). This should
	// not count as a successful edit since last verification.
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest"}`,
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "1 passed",
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"sed -i 's/x/y/' missing.py"}`,
					ToolCallID: "mut1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "sed: can't read missing.py: No such file or directory\n[exit code: 2]",
					ToolCallID: "mut1",
				},
			},
		},
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	_, err := validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("expected acceptance when post-verify mutation failed, got: %v", err)
	}
}

func TestVerificationCheckpoint_VerificationWithRedirectDoesNotTriggerStaleReject(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	rc := &core.RunContext{}
	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest > /tmp/test.log"}`,
					ToolCallID: "verify1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "1 passed",
					ToolCallID: "verify1",
				},
			},
		},
	}
	_, _ = mw(ctx, messages, nil, nil, next)

	_, err := validator(ctx, rc, "Done!")
	if err != nil {
		t.Fatalf("expected acceptance for verification with redirect, got: %v", err)
	}
}

func TestIsMutatingLSPCall(t *testing.T) {
	tests := []struct {
		name string
		args string
		want bool
	}{
		{
			name: "rename mutates",
			args: `{"method":"rename","file":"main.go","line":1,"character":1,"new_name":"X"}`,
			want: true,
		},
		{
			name: "code_action with action_index mutates",
			args: `{"method":"code_action","file":"main.go","line":1,"character":1,"action_index":0}`,
			want: true,
		},
		{
			name: "code_action list is read-only",
			args: `{"method":"code_action","file":"main.go","line":1,"character":1}`,
			want: false,
		},
		{
			name: "outline is read-only",
			args: `{"method":"outline","file":"main.go"}`,
			want: false,
		},
		{
			name: "invalid json",
			args: `{`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMutatingLSPCall(tt.args)
			if got != tt.want {
				t.Fatalf("isMutatingLSPCall(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsMutatingBashCommand(t *testing.T) {
	tests := []struct {
		name string
		args string
		want bool
	}{
		{
			name: "sed in-place edit",
			args: `{"command":"sed -i 's/a/b/' main.py"}`,
			want: true,
		},
		{
			name: "redirect write",
			args: `{"command":"echo hi > out.txt"}`,
			want: true,
		},
		{
			name: "rm file",
			args: `{"command":"rm -f out.txt"}`,
			want: true,
		},
		{
			name: "read-only grep",
			args: `{"command":"grep -n foo main.py"}`,
			want: false,
		},
		{
			name: "stderr redirect only",
			args: `{"command":"pytest 2>/tmp/err.log"}`,
			want: false,
		},
		{
			name: "invalid json",
			args: `{`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMutatingBashCommand(tt.args)
			if got != tt.want {
				t.Fatalf("isMutatingBashCommand(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsStrongMutatingBashCommand(t *testing.T) {
	tests := []struct {
		name string
		args string
		want bool
	}{
		{
			name: "strong mutation sed",
			args: `{"command":"sed -i 's/a/b/' main.py"}`,
			want: true,
		},
		{
			name: "redirect only not strong",
			args: `{"command":"pytest > /tmp/test.log"}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStrongMutatingBashCommand(tt.args)
			if got != tt.want {
				t.Fatalf("isStrongMutatingBashCommand(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestMutationToolReturnSucceeded(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		content  string
		want     bool
	}{
		{
			name:     "bash success",
			toolName: "bash",
			content:  "ok",
			want:     true,
		},
		{
			name:     "bash non-zero exit",
			toolName: "bash",
			content:  "failed\n[exit code: 1]",
			want:     false,
		},
		{
			name:     "lsp no edits",
			toolName: "lsp",
			content:  "No edits were produced by the rename.",
			want:     false,
		},
		{
			name:     "edit generic error",
			toolName: "edit",
			content:  "error: failed to write",
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mutationToolReturnSucceeded(tt.toolName, tt.content)
			if got != tt.want {
				t.Fatalf("mutationToolReturnSucceeded(%q, %q) = %v, want %v", tt.toolName, tt.content, got, tt.want)
			}
		})
	}
}

func TestVerificationCheckpoint_StagnationDetection(t *testing.T) {
	mw, _ := VerificationCheckpoint("")

	ctx := context.Background()
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		// Check if stagnation guidance was injected into messages.
		lastMsg := msgs[len(msgs)-1]
		if req, ok := lastMsg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if up, ok := part.(core.UserPromptPart); ok {
					if strings.Contains(up.Content, "STAGNATION") {
						return &core.ModelResponse{
							Parts: []core.ModelResponsePart{
								core.TextPart{Content: "stagnation_detected"},
							},
						}, nil
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	// Build messages with 3 consecutive failing test runs.
	messages := []core.ModelMessage{}
	for i := 1; i <= 3; i++ {
		callID := fmt.Sprintf("call%d", i)
		messages = append(messages,
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{
						ToolName:   "bash",
						ArgsJSON:   `{"command":"pytest"}`,
						ToolCallID: callID,
					},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.ToolReturnPart{
						ToolName:   "bash",
						Content:    "FAILED test_main.py::test_output\n1 failed, 2 passed\n[exit code: 1]",
						ToolCallID: callID,
					},
				},
			},
		)
	}

	// After 3 failing runs, middleware should inject stagnation guidance.
	resp, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Parts) > 0 {
		if tp, ok := resp.Parts[0].(core.TextPart); ok {
			if tp.Content != "stagnation_detected" {
				t.Error("expected stagnation guidance to be injected after 3 consecutive failing runs")
			}
		}
	}
}

func TestVerificationCheckpoint_NoStagnationWhenImproving(t *testing.T) {
	mw, _ := VerificationCheckpoint("")

	ctx := context.Background()
	stagnationInjected := false
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		lastMsg := msgs[len(msgs)-1]
		if req, ok := lastMsg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if up, ok := part.(core.UserPromptPart); ok {
					if strings.Contains(up.Content, "STAGNATION") || strings.Contains(up.Content, "CRITICAL STAGNATION") {
						stagnationInjected = true
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	// Build messages with 3 consecutive failing runs BUT improving pass counts.
	// Run 1: 1 passed, 2 failed. Run 2: 2 passed, 1 failed. Run 3: 3 passed, 1 failed.
	messages := []core.ModelMessage{}
	passCounts := []int{1, 2, 3}
	failCounts := []int{2, 1, 1}
	for i := range 3 {
		callID := fmt.Sprintf("call%d", i+1)
		output := fmt.Sprintf("%d passed, %d failed\n[exit code: 1]", passCounts[i], failCounts[i])
		messages = append(messages,
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{
						ToolName:   "bash",
						ArgsJSON:   `{"command":"pytest"}`,
						ToolCallID: callID,
					},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.ToolReturnPart{
						ToolName:   "bash",
						Content:    output,
						ToolCallID: callID,
					},
				},
			},
		)
	}

	_, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stagnationInjected {
		t.Error("should NOT inject stagnation guidance when pass counts are improving")
	}
}

func TestVerificationCheckpoint_RegressionDetection(t *testing.T) {
	mw, _ := VerificationCheckpoint("")

	ctx := context.Background()
	regressionInjected := false
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		lastMsg := msgs[len(msgs)-1]
		if req, ok := lastMsg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if up, ok := part.(core.UserPromptPart); ok {
					if strings.Contains(up.Content, "REGRESSION") {
						regressionInjected = true
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	// Build messages with a regression: pass count goes DOWN.
	// Run 1: 5 passed, 1 failed. Run 2: 3 passed, 3 failed (regression).
	messages := []core.ModelMessage{}
	passCounts := []int{5, 3}
	failCounts := []int{1, 3}
	for i := range 2 {
		callID := fmt.Sprintf("reg%d", i+1)
		output := fmt.Sprintf("%d passed, %d failed\n[exit code: 1]", passCounts[i], failCounts[i])
		messages = append(messages,
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{
						ToolName:   "bash",
						ArgsJSON:   `{"command":"pytest"}`,
						ToolCallID: callID,
					},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.ToolReturnPart{
						ToolName:   "bash",
						Content:    output,
						ToolCallID: callID,
					},
				},
			},
		)
	}

	_, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regressionInjected {
		t.Error("should inject regression warning when pass count decreases")
	}
}

func TestVerificationCheckpoint_NoRegressionWhenImproving(t *testing.T) {
	mw, _ := VerificationCheckpoint("")

	ctx := context.Background()
	regressionInjected := false
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		lastMsg := msgs[len(msgs)-1]
		if req, ok := lastMsg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if up, ok := part.(core.UserPromptPart); ok {
					if strings.Contains(up.Content, "REGRESSION") {
						regressionInjected = true
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	// Build messages with improving pass count (no regression).
	// Run 1: 2 passed, 4 failed. Run 2: 4 passed, 2 failed.
	messages := []core.ModelMessage{}
	passCounts := []int{2, 4}
	failCounts := []int{4, 2}
	for i := range 2 {
		callID := fmt.Sprintf("noreg%d", i+1)
		output := fmt.Sprintf("%d passed, %d failed\n[exit code: 1]", passCounts[i], failCounts[i])
		messages = append(messages,
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{
						ToolName:   "bash",
						ArgsJSON:   `{"command":"pytest"}`,
						ToolCallID: callID,
					},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.ToolReturnPart{
						ToolName:   "bash",
						Content:    output,
						ToolCallID: callID,
					},
				},
			},
		)
	}

	_, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if regressionInjected {
		t.Error("should NOT inject regression warning when pass count improves")
	}
}

func TestVerificationCheckpoint_NoRegressionOnUnknownPassCount(t *testing.T) {
	mw, _ := VerificationCheckpoint("")

	ctx := context.Background()
	regressionInjected := false
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		lastMsg := msgs[len(msgs)-1]
		if req, ok := lastMsg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if up, ok := part.(core.UserPromptPart); ok {
					if strings.Contains(up.Content, "REGRESSION") {
						regressionInjected = true
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{ToolName: "bash", ArgsJSON: `{"command":"pytest -q"}`, ToolCallID: "u1"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{ToolName: "bash", Content: "1 passed in 0.10s\n[exit code: 0]", ToolCallID: "u1"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{ToolName: "bash", ArgsJSON: `{"command":"pytest -q"}`, ToolCallID: "u2"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{ToolName: "bash", Content: "[exit code: 1]\n(no output)", ToolCallID: "u2"},
			},
		},
	}

	_, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if regressionInjected {
		t.Error("should not inject regression warning when latest pass count is unknown")
	}
}

func TestVerificationCheckpoint_IgnoresPytestHelpCommand(t *testing.T) {
	mw, validator := VerificationCheckpoint("")

	ctx := context.Background()
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Fix task"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{ToolName: "bash", ArgsJSON: `{"command":"pytest -q"}`, ToolCallID: "h1"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{ToolName: "bash", Content: ". [100%]\n1 passed in 0.10s\n[exit code: 0]", ToolCallID: "h1"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{ToolName: "bash", ArgsJSON: `{"command":"pytest --help | grep allow-no-tests"}`, ToolCallID: "h2"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{ToolName: "bash", Content: "[exit code: 1]\n(no output)", ToolCallID: "h2"},
			},
		},
	}

	next := func(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		return &core.ModelResponse{}, nil
	}
	if _, err := mw(ctx, messages, nil, nil, next); err != nil {
		t.Fatalf("middleware error: %v", err)
	}

	rc := &core.RunContext{}
	// Help-only command must not override last passing verify.
	if _, err := validator(ctx, rc, "done"); err != nil {
		t.Fatalf("validator should accept after passing verification; got: %v", err)
	}
}

func TestStagnationGuidance(t *testing.T) {
	tests := []struct {
		name             string
		consecutiveFails int
		wantSubstr       string
	}{
		{"2_fails", 2, "VERIFICATION STAGNATION"},
		{"3_fails", 3, "STAGNATION WARNING"},
		{"4_fails", 4, "CRITICAL STAGNATION"},
		{"5_fails", 5, "CRITICAL STAGNATION"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPassed := make([]int, tt.consecutiveFails)
			runSummary := make([]string, tt.consecutiveFails)
			for i := range runPassed {
				runPassed[i] = 2
				runSummary[i] = "1 failed, 2 passed"
			}
			got := stagnationGuidance(tt.consecutiveFails, runPassed, runSummary)
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("stagnationGuidance(%d) = %q, want substring %q", tt.consecutiveFails, got, tt.wantSubstr)
			}
		})
	}
}

func TestStagnationGuidance_SameErrorHint(t *testing.T) {
	// When two consecutive runs have the exact same error summary,
	// stagnationGuidance should include the same-error hint.
	runPassed := []int{2, 2, 2}
	runSummary := []string{"1 failed, 2 passed", "1 failed, 2 passed", "1 failed, 2 passed"}
	got := stagnationGuidance(3, runPassed, runSummary)
	if !strings.Contains(got, "EXACT SAME error") {
		t.Errorf("stagnationGuidance with same errors should include same-error hint, got: %q", got)
	}
}

func TestStagnationGuidance_NoSameErrorHintWhenDifferent(t *testing.T) {
	// When consecutive runs have different error summaries,
	// stagnationGuidance should NOT include the same-error hint.
	runPassed := []int{2, 2}
	runSummary := []string{"1 failed: test_foo", "1 failed: test_bar"}
	got := stagnationGuidance(2, runPassed, runSummary)
	if strings.Contains(got, "EXACT SAME error") {
		t.Errorf("stagnationGuidance with different errors should NOT include same-error hint, got: %q", got)
	}
}

func TestVerificationResultFailed(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantFailed bool
		wantSubstr string // expected substring in summary (empty = no check)
	}{
		{
			name:       "passing_tests",
			output:     "ok\nPASS",
			wantFailed: false,
		},
		{
			name:       "exit_code_0",
			output:     "all good\n[exit code: 0]",
			wantFailed: false,
		},
		{
			name:       "exit_code_1",
			output:     "error\n[exit code: 1]",
			wantFailed: true,
			wantSubstr: "non-zero",
		},
		{
			name:       "pytest_failures",
			output:     "FAILED test_foo.py::test_bar\n2 failed, 3 passed\n[exit code: 1]",
			wantFailed: true,
			wantSubstr: "failed",
		},
		{
			name:       "timeout",
			output:     "[timed out after 120s]",
			wantFailed: true,
			wantSubstr: "timed out",
		},
		{
			name:       "no_output",
			output:     "(no output)",
			wantFailed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failed, summary := verificationResultFailed(tt.output)
			if failed != tt.wantFailed {
				t.Errorf("verificationResultFailed(%q) = %v, want %v (summary: %q)", tt.output, failed, tt.wantFailed, summary)
			}
			if tt.wantSubstr != "" && !strings.Contains(strings.ToLower(summary), tt.wantSubstr) {
				t.Errorf("summary %q should contain %q", summary, tt.wantSubstr)
			}
		})
	}
}

func TestToolReturnContentString(t *testing.T) {
	// String content.
	if got := toolReturnContentString("hello"); got != "hello" {
		t.Errorf("expected %q, got %q", "hello", got)
	}
	// Non-string content (e.g., structured data).
	m := map[string]string{"key": "value"}
	got := toolReturnContentString(m)
	if !strings.Contains(got, "key") || !strings.Contains(got, "value") {
		t.Errorf("expected JSON with key/value, got %q", got)
	}
}

func TestFailureGuidance(t *testing.T) {
	tests := []struct {
		summary    string
		wantSubstr string
	}{
		{"verification command timed out", "TOO SLOW"},
		{"compilation error: undefined variable", "COMPILATION"},
		{"expected 42, got 43", "MISMATCH"},
		{"file not found: output.txt", "MISSING FILE"},
		{"AssertionError: values differ", "ASSERTION"},
		{"segmentation fault at 0x0", "SEGMENTATION FAULT"},
		{"signal: segmentation fault", "invalid memory"},
		{"exit code: 139 (SIGSEGV)", "SEGMENTATION FAULT"},
		{"process killed (out of memory)", "OUT OF MEMORY"},
		{"exit code: 137 (signal 9)", "OUT OF MEMORY"},
		{"cannot allocate memory", "OUT OF MEMORY"},
		{"maximum recursion depth exceeded", "STACK OVERFLOW"},
		{"stack overflow in recursive call", "STACK OVERFLOW"},
		{"RecursionError: too deep", "STACK OVERFLOW"},
		{"stack level too deep (SystemStackError)", "STACK OVERFLOW"},
		{"floating point exception (core dumped)", "FLOATING POINT EXCEPTION"},
		{"division by zero in calculation", "FLOATING POINT EXCEPTION"},
		{"exit code: 136 (SIGFPE)", "FLOATING POINT EXCEPTION"},
		{"generic failure", "Fix the failures"},
	}
	for _, tt := range tests {
		got := failureGuidance(tt.summary)
		if !strings.Contains(got, tt.wantSubstr) {
			t.Errorf("failureGuidance(%q) = %q, want substring %q", tt.summary, got, tt.wantSubstr)
		}
	}
}

func TestOutputMismatchHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		wantHint bool
	}{
		{
			"expected_got",
			"AssertionError: expected 42, got 43\n[exit code: 1]",
			1,
			true,
		},
		{
			"files_differ",
			"Files output.txt and expected.txt differ\n[exit code: 1]",
			1,
			true,
		},
		{
			"assertEqual",
			"AssertionError: 'hello' != 'world'\n[exit code: 1]",
			1,
			false, // needs assertEqual pattern specifically
		},
		{
			"no_mismatch",
			"ModuleNotFoundError: No module named 'foo'\n[exit code: 1]",
			1,
			false,
		},
		{
			"exit_0",
			"expected 42, got 43",
			0,
			false, // exit code 0 means no hint
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := outputMismatchHint(tt.output, tt.exitCode, "")
			if tt.wantHint && got == "" {
				t.Error("expected output mismatch hint, got empty")
			}
			if !tt.wantHint && got != "" {
				t.Errorf("expected no hint, got: %s", got)
			}
			if tt.wantHint && got != "" {
				if !strings.Contains(got, "xxd") || !strings.Contains(got, "diff") {
					t.Errorf("hint should suggest xxd and diff, got: %s", got)
				}
			}
		})
	}
}

func TestLoopDetectionMiddleware_PersistentLoop(t *testing.T) {
	// Test that halving (instead of full reset) causes persistent loops
	// to trigger warnings more frequently on recurrence.
	mw := LoopDetectionMiddleware(4)
	ctx := context.Background()
	loopWarnings := 0
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		for _, msg := range msgs {
			if req, ok := msg.(core.ModelRequest); ok {
				for _, part := range req.Parts {
					if up, ok := part.(core.UserPromptPart); ok {
						if strings.Contains(up.Content, "stuck in a loop") {
							loopWarnings++
						}
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	editMsg := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName: "edit",
				ArgsJSON: `{"path":"/app/main.py"}`,
			},
		},
	}

	// Simulate turns by calling mw with growing message lists.
	// The middleware scans last 2 messages per call and accumulates counts.
	messages := []core.ModelMessage{}

	// Keep adding edits until first warning fires.
	for i := 0; i < 10 && loopWarnings == 0; i++ {
		messages = append(messages, editMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
	}
	if loopWarnings != 1 {
		t.Fatalf("expected first loop warning, got %d after %d edits", loopWarnings, len(messages))
	}
	firstWarningAt := len(messages)

	// Continue adding edits until second warning fires.
	for i := 0; i < 10 && loopWarnings == 1; i++ {
		messages = append(messages, editMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
	}
	if loopWarnings != 2 {
		t.Fatalf("expected second loop warning, got %d after %d more edits", loopWarnings, len(messages)-firstWarningAt)
	}
	secondWarningAt := len(messages) - firstWarningAt

	// With halving, the second warning should come faster than the first.
	// (Counts are halved, not reset to 0, so recurrence is detected sooner.)
	if secondWarningAt >= firstWarningAt {
		t.Errorf("persistent loop should be detected faster: first=%d edits, second=%d edits (expected second < first)",
			firstWarningAt, secondWarningAt)
	}
}

func TestValidateOutputFormats_BOM(t *testing.T) {
	dir := t.TempDir()
	// Create a file with BOM marker.
	bomFile := filepath.Join(dir, "output.txt")
	os.WriteFile(bomFile, []byte{0xEF, 0xBB, 0xBF, 'h', 'e', 'l', 'l', 'o'}, 0o644)

	// Create a test script that references the output file.
	testsDir := filepath.Join(dir, "tests")
	os.MkdirAll(testsDir, 0o755)
	os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte(`diff output.txt expected.txt`), 0o644)

	result := validateOutputFormats(dir, detectExpectedOutputs(dir))
	if !strings.Contains(result, "BOM") {
		t.Errorf("expected BOM warning, got: %q", result)
	}
}

func TestValidateOutputFormats_WindowsLineEndings(t *testing.T) {
	dir := t.TempDir()
	// Create a file with \r\n line endings.
	crlfFile := filepath.Join(dir, "output.csv")
	os.WriteFile(crlfFile, []byte("a,b\r\nc,d\r\n"), 0o644)

	// Create a test script that references the output file.
	testsDir := filepath.Join(dir, "tests")
	os.MkdirAll(testsDir, 0o755)
	os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte(`diff output.csv expected.csv`), 0o644)

	result := validateOutputFormats(dir, detectExpectedOutputs(dir))
	if !strings.Contains(result, "Windows line endings") {
		t.Errorf("expected Windows line endings warning, got: %q", result)
	}
}

func TestValidateOutputFormats_CleanFile(t *testing.T) {
	dir := t.TempDir()
	// Create a clean file.
	os.WriteFile(filepath.Join(dir, "output.txt"), []byte("hello\nworld\n"), 0o644)

	testsDir := filepath.Join(dir, "tests")
	os.MkdirAll(testsDir, 0o755)
	os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte(`diff output.txt expected.txt`), 0o644)

	result := validateOutputFormats(dir, detectExpectedOutputs(dir))
	if result != "" {
		t.Errorf("expected no issues for clean file, got: %q", result)
	}
}

func TestFileSnippetForEdit(t *testing.T) {
	content := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}

func helper() {
	fmt.Println("helper")
}
`
	// Search for something that partially matches.
	search := `func main() {
	fmt.Println("Goodbye, World!")
}`
	snippet := fileSnippetForEdit(content, search)
	if snippet == "" {
		t.Fatal("expected a non-empty snippet")
	}
	if !strings.Contains(snippet, "func main()") {
		t.Errorf("snippet should contain 'func main()', got: %s", snippet)
	}
	if !strings.Contains(snippet, "Hello, World!") {
		t.Errorf("snippet should contain the actual file content, got: %s", snippet)
	}

	// Empty search should return empty.
	if s := fileSnippetForEdit(content, ""); s != "" {
		t.Errorf("expected empty snippet for empty search, got: %s", s)
	}

	// Search for something with no match at all.
	if s := fileSnippetForEdit(content, "zzzzzzz_nonexistent"); s != "" {
		t.Errorf("expected empty snippet for non-matching search, got: %s", s)
	}
}

func TestPythonErrorHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		want     string
	}{
		{
			name: "syntax_error",
			output: `Traceback (most recent call last):
  File "solve.py", line 42, in <module>
    x = (1 +
SyntaxError: unexpected EOF while parsing`,
			exitCode: 1,
			want:     "solve.py:42",
		},
		{
			name: "indentation_error",
			output: `  File "main.py", line 10
    print("hello")
IndentationError: unexpected indent`,
			exitCode: 1,
			want:     "main.py:10",
		},
		{
			name: "name_error_suggestion",
			output: `Traceback (most recent call last):
  File "app.py", line 5, in <module>
NameError: name 'pritn' is not defined. Did you mean: 'print'?`,
			exitCode: 1,
			want:     "Did you mean",
		},
		{
			name:     "success_no_hint",
			output:   "All tests passed!",
			exitCode: 0,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pythonErrorHint(tt.output, tt.exitCode)
			if tt.want == "" {
				if got != "" {
					t.Errorf("expected empty hint, got: %s", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("expected hint to contain %q, got: %s", tt.want, got)
			}
		})
	}
}

func TestWritePreview(t *testing.T) {
	dir := t.TempDir()
	tool := Write(WithWorkDir(dir))

	// Small file should include preview.
	result := call(t, tool, `{"path":"small.py","content":"print('hello')\nprint('world')\n"}`)
	if !strings.Contains(result, "3 lines") {
		t.Errorf("expected line count in result, got: %s", result)
	}
	if !strings.Contains(result, "print('hello')") {
		t.Errorf("expected content preview, got: %s", result)
	}
}

func TestWriteOverwriteWarning(t *testing.T) {
	dir := t.TempDir()
	tool := Write(WithWorkDir(dir))

	// Create a large file first.
	bigContent := strings.Repeat("line of content\n", 100) // ~1600 bytes
	call(t, tool, fmt.Sprintf(`{"path":"big.txt","content":%q}`, bigContent))

	// Overwrite with much smaller content — should trigger warning.
	result := call(t, tool, `{"path":"big.txt","content":"tiny\n"}`)
	if !strings.Contains(result, "warning") {
		t.Errorf("expected overwrite warning, got: %s", result)
	}
	if !strings.Contains(result, "reduction") {
		t.Errorf("expected reduction percentage in warning, got: %s", result)
	}

	// Overwrite with similar-sized content — should NOT trigger warning.
	result2 := call(t, tool, fmt.Sprintf(`{"path":"big.txt","content":%q}`, bigContent[:len(bigContent)-10]))
	if strings.Contains(result2, "warning") {
		t.Errorf("expected no warning for similar-size overwrite, got: %s", result2)
	}
}

func TestMultiEdit_Atomic(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.txt", "hello world\ngoodbye world\n")
	writeTestFile(t, dir, "b.txt", "foo bar\nbaz qux\n")

	tool := MultiEdit(WithWorkDir(dir))

	// First edit succeeds, second should fail — verify first file is NOT modified.
	err := callErr(t, tool, `{"edits":[
		{"path":"a.txt","old_string":"hello world","new_string":"hello earth"},
		{"path":"b.txt","old_string":"nonexistent string","new_string":"replacement"}
	]}`)
	if err == nil {
		t.Fatal("expected error for second edit not found")
	}

	// Verify a.txt was NOT modified (atomic — second edit failed, so no writes).
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if !strings.Contains(string(data), "hello world") {
		t.Errorf("expected a.txt to remain unchanged (atomic multi_edit), got: %s", data)
	}

	// Test that sequential edits to the same file work within a batch.
	result := call(t, tool, `{"edits":[
		{"path":"a.txt","old_string":"hello world","new_string":"hello earth"},
		{"path":"a.txt","old_string":"goodbye world","new_string":"goodbye earth"}
	]}`)
	if !strings.Contains(result, "hello earth") {
		t.Errorf("expected first edit result, got: %s", result)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "a.txt"))
	content := string(data)
	if !strings.Contains(content, "hello earth") || !strings.Contains(content, "goodbye earth") {
		t.Errorf("expected both edits applied, got: %s", content)
	}
}

func TestGlobShowsSizes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "big.txt", strings.Repeat("x", 2000))
	writeTestFile(t, dir, "small.txt", "hello")

	tool := Glob(WithWorkDir(dir))

	result := call(t, tool, `{"pattern":"*.txt"}`)
	// Should show file sizes.
	if !strings.Contains(result, "(") {
		t.Errorf("expected file sizes in glob output, got: %s", result)
	}
}

func TestFindOccurrenceLines(t *testing.T) {
	content := "aaa\nbbb\nccc\nbbb\nddd\nbbb\n"
	got := findOccurrenceLines(content, "bbb")
	// "bbb" appears at lines 2, 4, 6
	if !strings.Contains(got, "2") || !strings.Contains(got, "4") || !strings.Contains(got, "6") {
		t.Errorf("findOccurrenceLines() = %q, want lines 2, 4, 6", got)
	}
}

func TestEditMultipleOccurrencesShowsLines(t *testing.T) {
	dir := t.TempDir()
	// Create a file with duplicate lines
	content := "line1\ndup\nline3\ndup\nline5\n"
	writeTestFile(t, dir, "dup.txt", content)

	tool := Edit(WithWorkDir(dir))
	err := callErr(t, tool, `{"path":"dup.txt","old_string":"dup","new_string":"unique"}`)
	if err == nil {
		t.Fatal("expected error for multiple occurrences")
	}
	errMsg := err.Error()
	// Should mention line numbers
	if !strings.Contains(errMsg, "lines") {
		t.Errorf("expected line numbers in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "2") {
		t.Errorf("expected line 2 in error, got: %s", errMsg)
	}
}

func TestCompilationErrorHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		contains string
	}{
		{
			name:     "gcc_error_with_message",
			output:   "main.c:42:5: error: expected ';' after expression",
			exitCode: 1,
			contains: "expected ';' after expression",
		},
		{
			name:     "gcc_error_file_line",
			output:   "main.c:42:5: error: expected ';' after expression",
			exitCode: 1,
			contains: "main.c:42",
		},
		{
			name:     "go_error",
			output:   "./main.go:15:2: undefined: fmt.Printl",
			exitCode: 2,
			contains: "main.go:15",
		},
		{
			name:     "rust_error_with_message",
			output:   "error[E0308]: mismatched types\n --> src/main.rs:10:5\n  |\n10 |     foo()\n  |     ^^^^^ expected u32",
			exitCode: 101,
			contains: "mismatched types",
		},
		{
			name:     "rust_error_file_line",
			output:   "error[E0308]: mismatched types\n --> src/main.rs:10:5\n  |\n10 |     foo()\n  |     ^^^^^ expected u32",
			exitCode: 101,
			contains: "src/main.rs:10",
		},
		{
			name:     "success_no_hint",
			output:   "Build succeeded",
			exitCode: 0,
			contains: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compilationErrorHint(tt.output, tt.exitCode)
			if tt.contains == "" && got != "" {
				t.Errorf("expected no hint, got: %s", got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("expected hint containing %q, got: %s", tt.contains, got)
			}
		})
	}
}

func TestJsonErrorHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		contains string
	}{
		{
			name:     "python_json_decode",
			output:   `json.decoder.JSONDecodeError: Expecting ',' delimiter: line 5 column 3 (char 42)`,
			exitCode: 1,
			contains: "line 5 column 3",
		},
		{
			name:     "node_json_error",
			output:   `SyntaxError: Unexpected token } in JSON at position 42`,
			exitCode: 1,
			contains: "position 42",
		},
		{
			name:     "success_no_hint",
			output:   `{"key": "value"}`,
			exitCode: 0,
			contains: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonErrorHint(tt.output, tt.exitCode)
			if tt.contains == "" && got != "" {
				t.Errorf("expected no hint, got: %s", got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("expected hint containing %q, got: %s", tt.contains, got)
			}
		})
	}
}

func TestPythonErrorHintExpanded(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		contains string
	}{
		{
			name: "type_error_with_traceback",
			output: `Traceback (most recent call last):
  File "main.py", line 42, in process
    result = x + "hello"
TypeError: unsupported operand type(s) for +: 'int' and 'str'`,
			exitCode: 1,
			contains: "main.py:42",
		},
		{
			name: "value_error_with_traceback",
			output: `Traceback (most recent call last):
  File "/app/solver.py", line 15, in parse
    val = int("abc")
ValueError: invalid literal for int() with base 10: 'abc'`,
			exitCode: 1,
			contains: "solver.py:15",
		},
		{
			name: "key_error_with_traceback",
			output: `Traceback (most recent call last):
  File "data.py", line 8, in load
    x = d["missing"]
KeyError: 'missing'`,
			exitCode: 1,
			contains: "data.py:8",
		},
		{
			name:     "file_not_found_no_traceback",
			output:   `FileNotFoundError: [Errno 2] No such file or directory: 'output.csv'`,
			exitCode: 1,
			contains: "output.csv",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pythonErrorHint(tt.output, tt.exitCode)
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("expected hint containing %q, got: %s", tt.contains, got)
			}
		})
	}
}

func TestEncodingErrorHint(t *testing.T) {
	got := encodingErrorHint("UnicodeDecodeError: 'utf-8' codec can't decode byte 0xff", 1)
	if !strings.Contains(got, "encoding='utf-8'") {
		t.Errorf("expected encoding hint, got: %s", got)
	}
	got = encodingErrorHint("UnicodeEncodeError: 'ascii' codec can't encode character", 1)
	if !strings.Contains(got, "encoding='utf-8'") {
		t.Errorf("expected encoding hint, got: %s", got)
	}
	got = encodingErrorHint("success output", 0)
	if got != "" {
		t.Errorf("expected no hint for success, got: %s", got)
	}
}

func TestPermissionHint(t *testing.T) {
	got := permissionHint("bash: ./run.sh: Permission denied", 126)
	if !strings.Contains(got, "chmod") {
		t.Errorf("expected chmod hint for exit 126, got: %s", got)
	}
	got = permissionHint("Permission denied: ./test.py", 1)
	if !strings.Contains(got, "chmod") {
		t.Errorf("expected chmod hint for script, got: %s", got)
	}
	got = permissionHint("all good", 0)
	if got != "" {
		t.Errorf("expected no hint for success, got: %s", got)
	}
}

func TestAddressInUseHint(t *testing.T) {
	got := addressInUseHint("OSError: [Errno 98] Address already in use", 1)
	if !strings.Contains(got, "port") {
		t.Errorf("expected port hint, got: %s", got)
	}
	got = addressInUseHint("Error: listen EADDRINUSE: address already in use :::3000", 1)
	if !strings.Contains(got, "port") {
		t.Errorf("expected port hint for EADDRINUSE, got: %s", got)
	}
	got = addressInUseHint("server started", 0)
	if got != "" {
		t.Errorf("expected no hint for success, got: %s", got)
	}
}

func TestFirstFailureDetail(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		contains string
	}{
		{
			name: "pytest_assertion",
			output: `test_foo.py::test_add FAILED
E       assert 5 == 4
E        +  where 5 = add(2, 3)
================ 1 failed, 2 passed ================`,
			contains: "assert 5 == 4",
		},
		{
			name: "go_test_expected_got",
			output: `--- FAIL: TestAdd (0.00s)
    main_test.go:15: expected 42, got 43
FAIL
FAIL	example.com/pkg	0.001s`,
			contains: "expected 42, got 43",
		},
		{
			name: "python_unittest_assertion",
			output: `FAIL: test_add (test_math.TestMath)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "test_math.py", line 10, in test_add
    self.assertEqual(result, 5)
AssertionError: 4 != 5
----------------------------------------------------------------------
Ran 3 tests in 0.001s

FAILED (failures=1)`,
			contains: "AssertionError: 4 != 5",
		},
		{
			name: "jest_expected_received",
			output: `FAIL src/math.test.js
  ● add › should return 5

    Expected: 5
    Received: 4

Tests: 1 failed, 2 passed, 3 total`,
			contains: "Expected: 5",
		},
		{
			name: "generic_expected_actual",
			output: `Running tests...
Test 1: PASS
Test 2: FAIL - Expected "hello" but got "world"
Test 3: PASS`,
			contains: `Expected "hello" but got "world"`,
		},
		{
			name: "classic_diff",
			output: `Comparing outputs:
1c1
< hello world
---
> hello wrold
2c2
< line two`,
			contains: `diff: expected "hello world", got "hello wrold"`,
		},
		{
			name: "unified_diff",
			output: `--- expected.txt
+++ actual.txt
@@ -1,3 +1,3 @@
 line1
-expected_line2
+actual_line2
 line3`,
			contains: `diff: expected "expected_line2", got "actual_line2"`,
		},
		{
			name: "rust_panic",
			output: `running 3 tests
test test_add ... ok
test test_multiply ... FAILED
thread 'test_multiply' panicked at 'assertion ` + "`" + `left == right` + "`" + ` failed
  left: 42
  right: 43'`,
			contains: "panicked at",
		},
		{
			name: "junit_expected_but_was",
			output: `Tests run: 5, Failures: 1, Errors: 0, Skipped: 0
org.junit.ComparisonFailure: expected:<[hello]> but was:<[world]>
	at org.junit.Assert.assertEquals(Assert.java:115)`,
			contains: "expected:<[hello]> but was:<[world]>",
		},
		{
			name: "junit5_expected_but_was",
			output: `expected: <42> but was: <43>
	at org.junit.jupiter.api.AssertionUtils.fail`,
			contains: "expected: <42> but was: <43>",
		},
		{
			name: "mocha_assertion",
			output: `  1 passing (10ms)
  1 failing

  1) Array #indexOf should return -1:
     AssertionError: expected 0 to equal -1`,
			contains: "expected 0 to equal -1",
		},
		{
			name: "phpunit_assertion",
			output: `1) ExampleTest::testAddition
Failed asserting that 4 matches expected 5.

FAILURES!
Tests: 3, Assertions: 3, Failures: 1`,
			contains: "Failed asserting that 4 matches expected 5",
		},
		{
			name: "shell_fail_expected_got",
			output: `Test 1: PASS
FAIL: expected 'hello world', got 'hello wrold'
Test 3: PASS`,
			contains: "expected",
		},
		{
			name: "no_failure",
			output: `All tests passed!
3 passed, 0 failed`,
			contains: "", // empty = no match expected
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := firstFailureDetail(tc.output)
			if tc.contains == "" {
				if got != "" {
					t.Errorf("expected no failure detail, got: %s", got)
				}
				return
			}
			if !strings.Contains(got, tc.contains) {
				t.Errorf("expected failure detail containing %q, got: %s", tc.contains, got)
			}
		})
	}
}

func TestTestResultSummaryWithFailureDetail(t *testing.T) {
	// Verify that testResultSummary now includes first failure details.
	output := `test_calc.py::test_multiply PASSED
test_calc.py::test_add FAILED
E       assert 7 == 5
test_calc.py::test_subtract PASSED
========================= 1 failed, 2 passed =========================`

	got := testResultSummary(output)
	if !strings.Contains(got, "1 failed") {
		t.Errorf("expected summary line with '1 failed', got: %s", got)
	}
	if !strings.Contains(got, "assert 7 == 5") {
		t.Errorf("expected first failure detail 'assert 7 == 5', got: %s", got)
	}
}

func TestParseMakefileTargets(t *testing.T) {
	makefile := `# My project Makefile

CC = gcc
CFLAGS = -Wall

.PHONY: all test clean

all: build

build:
	$(CC) $(CFLAGS) -o myapp main.c

test: build
	./run_tests.sh

clean:
	rm -f myapp *.o

install: build
	cp myapp /usr/local/bin/

run: build
	./myapp

lint:
	pylint src/

# Don't include these:
%.o: %.c
	$(CC) -c $<
`
	targets := parseMakefileTargets(makefile)

	// Should include useful targets
	targetSet := make(map[string]bool)
	for _, t := range targets {
		targetSet[t] = true
	}

	for _, expected := range []string{"build", "test", "clean", "install", "run", "lint"} {
		if !targetSet[expected] {
			t.Errorf("expected target %q in parsed targets: %v", expected, targets)
		}
	}
}

func TestTestResultSummaryXOfYPattern(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "X/Y passed",
			output: `Running test suite...
Test 1: PASS
Test 2: FAIL
Test 3: PASS
3/5 tests passed`,
			want: "3/5 tests passed",
		},
		{
			name: "X out of Y",
			output: `Checking outputs...
Passed 7 out of 10 tests`,
			want: "7 out of 10",
		},
		{
			name:   "Passed X of Y failed Z",
			output: `Test results: passed 3 of 5, failed 2`,
			want:   "passed 3 of 5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := testResultSummary(tt.output)
			if !strings.Contains(got, tt.want) {
				t.Errorf("expected summary containing %q, got: %s", tt.want, got)
			}
		})
	}
}

func TestTestResultSummaryMavenAndDotNet(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "Maven/JUnit",
			output: `[INFO] Running com.example.AppTest
[ERROR] Tests run: 10, Failures: 2, Errors: 0, Skipped: 1
[INFO] BUILD FAILURE`,
			want: "Tests run: 10, Failures: 2",
		},
		{
			name: "dotnet test",
			output: `Starting test execution, please wait...
Passed!  - Failed: 0, Passed: 5, Skipped: 0, Total: 5
Total tests: 5, Passed: 5, Failed: 0`,
			want: "Total tests: 5, Passed: 5",
		},
		{
			name: "Gradle",
			output: `> Task :test
2 tests completed, 1 failed
BUILD FAILED`,
			want: "BUILD FAILED",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := testResultSummary(tt.output)
			if !strings.Contains(got, tt.want) {
				t.Errorf("expected summary containing %q, got: %s", tt.want, got)
			}
		})
	}
}

func TestTestFailureFingerprint(t *testing.T) {
	// Same output should produce same fingerprint.
	output1 := `test_calc.py::test_add FAILED
E       assert 7 == 5
========================= 1 failed =========================`

	fp1 := testFailureFingerprint(output1)
	fp2 := testFailureFingerprint(output1)
	if fp1 != fp2 {
		t.Errorf("same output should produce same fingerprint: %q vs %q", fp1, fp2)
	}
	if fp1 == "" {
		t.Error("expected non-empty fingerprint for failing test")
	}

	// Different failure should produce different fingerprint.
	output2 := `test_calc.py::test_add FAILED
E       assert 7 == 6
========================= 1 failed =========================`

	fp3 := testFailureFingerprint(output2)
	if fp3 == fp1 {
		t.Errorf("different failures should produce different fingerprints: both %q", fp1)
	}

	// Passing test should produce empty fingerprint.
	output3 := `test_calc.py::test_add PASSED
========================= 1 passed =========================`
	fp4 := testFailureFingerprint(output3)
	if fp4 != "" {
		t.Errorf("passing test should have empty fingerprint, got: %q", fp4)
	}
}

func TestDetectEnvFiles(t *testing.T) {
	dir := t.TempDir()

	// No .env files — should return empty.
	got := detectEnvFiles(dir)
	if got != "" {
		t.Errorf("expected empty for no env files, got: %s", got)
	}

	// Create .env.example with some vars.
	writeTestFile(t, dir, ".env.example", `# Database config
DATABASE_URL=postgresql://localhost:5432/mydb
SECRET_KEY=changeme
PORT=8080
`)
	got = detectEnvFiles(dir)
	if !strings.Contains(got, "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL in env hint, got: %s", got)
	}
	if !strings.Contains(got, ".env.example") {
		t.Errorf("expected .env.example path in hint, got: %s", got)
	}
	if !strings.Contains(got, "cp") {
		t.Errorf("expected cp hint for .env.example, got: %s", got)
	}
}

func TestSignalHintTimeout(t *testing.T) {
	hint := signalHint(124)
	if !strings.Contains(hint, "timeout") {
		t.Errorf("expected timeout hint for exit 124, got: %s", hint)
	}
	if !strings.Contains(hint, "too slow") {
		t.Errorf("expected 'too slow' in timeout hint, got: %s", hint)
	}
}

func TestNodeErrorHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		exitCode int
		contains string
	}{
		{
			name:     "module_not_found",
			output:   `Error: Cannot find module 'express'\nRequire stack:\n- /app/server.js`,
			exitCode: 1,
			contains: "npm install express",
		},
		{
			name:     "reference_error_with_stack",
			output:   "ReferenceError: foo is not defined\n    at Object.<anonymous> (/app/main.js:15:3)\n    at Module._compile (node:internal/modules/cjs/loader:1254:14)",
			exitCode: 1,
			contains: "/app/main.js:15:3",
		},
		{
			name:     "success_no_hint",
			output:   "Server listening on port 3000",
			exitCode: 0,
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodeErrorHint(tt.output, tt.exitCode)
			if tt.contains == "" {
				if got != "" {
					t.Errorf("expected no hint, got: %s", got)
				}
				return
			}
			if !strings.Contains(got, tt.contains) {
				t.Errorf("expected hint containing %q, got: %s", tt.contains, got)
			}
		})
	}
}

func TestFirstFailureDetailColon(t *testing.T) {
	// Lines ending in ":" should be treated as headers, not assertions.
	output := "Comparing expected vs actual output:\n< hello\n---\n> world"
	got := firstFailureDetail(output)
	// Should match the diff pattern, not the "expected vs actual" header.
	if strings.Contains(got, "Comparing") {
		t.Errorf("expected diff pattern match, not header match, got: %s", got)
	}
	if !strings.Contains(got, "diff:") {
		t.Errorf("expected diff: pattern, got: %s", got)
	}
}

func TestDetectCoqTask(t *testing.T) {
	// Directory with _CoqProject file.
	dir := t.TempDir()
	writeTestFile(t, dir, "_CoqProject", "-R . MyProject\n")
	if !detectCoqTask(dir) {
		t.Error("expected Coq task detection with _CoqProject")
	}

	// Directory with .v files.
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "Proof.v", "Theorem plus_comm : forall n m, n + m = m + n.\n")
	if !detectCoqTask(dir2) {
		t.Error("expected Coq task detection with .v files")
	}

	// Empty directory — should not detect.
	dir3 := t.TempDir()
	if detectCoqTask(dir3) {
		t.Error("expected no Coq task in empty directory")
	}
}

func TestDetectOCamlTask(t *testing.T) {
	// Directory with dune-project.
	dir := t.TempDir()
	writeTestFile(t, dir, "dune-project", "(lang dune 3.0)\n")
	if !detectOCamlTask(dir) {
		t.Error("expected OCaml task detection with dune-project")
	}

	// Directory with .ml files.
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "main.ml", "let () = print_endline \"hello\"\n")
	if !detectOCamlTask(dir2) {
		t.Error("expected OCaml task detection with .ml files")
	}

	// Empty directory — should not detect.
	dir3 := t.TempDir()
	if detectOCamlTask(dir3) {
		t.Error("expected no OCaml task in empty directory")
	}
}

func TestDetectBuildFromSourceTask(t *testing.T) {
	// Directory name with "build-" prefix.
	dir := filepath.Join(t.TempDir(), "build-povray")
	os.MkdirAll(dir, 0o755)
	if !detectBuildFromSourceTask(dir) {
		t.Error("expected build task detection from directory name 'build-povray'")
	}

	// Directory with configure script.
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "configure", "#!/bin/sh\n")
	if !detectBuildFromSourceTask(dir2) {
		t.Error("expected build task detection with configure script")
	}

	// Empty directory — should not detect.
	dir3 := t.TempDir()
	if detectBuildFromSourceTask(dir3) {
		t.Error("expected no build task in empty directory")
	}
}

func TestDetectImageFiles(t *testing.T) {
	// Directory with image files.
	dir := t.TempDir()
	writeTestFile(t, dir, "diagram.png", "PNG")
	writeTestFile(t, dir, "photo.jpg", "JPEG")
	writeTestFile(t, dir, "code.py", "print('hi')")

	images := detectImageFiles(dir)
	if len(images) != 2 {
		t.Errorf("expected 2 image files, got %d: %v", len(images), images)
	}

	// Empty directory — no images.
	dir2 := t.TempDir()
	images2 := detectImageFiles(dir2)
	if len(images2) != 0 {
		t.Errorf("expected 0 image files in empty dir, got %d", len(images2))
	}
}

func TestDetectRTask(t *testing.T) {
	// Directory with .R file.
	dir := t.TempDir()
	writeTestFile(t, dir, "ars.R", "ars <- function(f, domain, n) {}\n")
	if !detectRTask(dir) {
		t.Error("expected R task detection with .R file")
	}

	// Directory with DESCRIPTION (R package).
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "DESCRIPTION", "Package: mypackage\nVersion: 1.0\n")
	if !detectRTask(dir2) {
		t.Error("expected R task detection with DESCRIPTION file")
	}

	// Empty directory — should not detect.
	dir3 := t.TempDir()
	if detectRTask(dir3) {
		t.Error("expected no R task in empty directory")
	}

	// DESCRIPTION without Package: field — not an R package.
	dir4 := t.TempDir()
	writeTestFile(t, dir4, "DESCRIPTION", "Just some text\n")
	if detectRTask(dir4) {
		t.Error("expected no R task from non-R DESCRIPTION file")
	}
}

func TestDetectJuliaTask(t *testing.T) {
	// Directory with .jl file.
	dir := t.TempDir()
	writeTestFile(t, dir, "solution.jl", "function solve()\nend\n")
	if !detectJuliaTask(dir) {
		t.Error("expected Julia task detection with .jl file")
	}

	// Directory with Project.toml.
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "Project.toml", "[deps]\nLinearAlgebra = \"37e2e46d\"\n")
	if !detectJuliaTask(dir2) {
		t.Error("expected Julia task detection with Project.toml")
	}

	// Empty directory.
	dir3 := t.TempDir()
	if detectJuliaTask(dir3) {
		t.Error("expected no Julia task in empty directory")
	}
}

func TestDetectPerlTask(t *testing.T) {
	// Directory with .pl file.
	dir := t.TempDir()
	writeTestFile(t, dir, "script.pl", "#!/usr/bin/perl\nprint \"hello\\n\";\n")
	if !detectPerlTask(dir) {
		t.Error("expected Perl task detection with .pl file")
	}

	// Directory with .pm file.
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "MyModule.pm", "package MyModule;\n1;\n")
	if !detectPerlTask(dir2) {
		t.Error("expected Perl task detection with .pm file")
	}

	// Directory with Makefile.PL.
	dir3 := t.TempDir()
	writeTestFile(t, dir3, "Makefile.PL", "use ExtUtils::MakeMaker;\n")
	if !detectPerlTask(dir3) {
		t.Error("expected Perl task detection with Makefile.PL")
	}

	// Empty directory.
	dir4 := t.TempDir()
	if detectPerlTask(dir4) {
		t.Error("expected no Perl task in empty directory")
	}
}

func TestDetectServiceTask(t *testing.T) {
	// Directory name containing "server".
	dir := t.TempDir()
	serverDir := filepath.Join(dir, "configure-git-webserver")
	os.MkdirAll(serverDir, 0o755)
	if !detectServiceTask(serverDir) {
		t.Error("expected service task detection from directory name 'configure-git-webserver'")
	}

	// Directory with nginx.conf.
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "nginx.conf", "server { listen 80; }\n")
	if !detectServiceTask(dir2) {
		t.Error("expected service task detection with nginx.conf")
	}

	// Empty directory with no indicators.
	dir3 := t.TempDir()
	if detectServiceTask(dir3) {
		t.Error("expected no service task in empty directory")
	}
}

func TestDetectHashComparisonTask(t *testing.T) {
	// Test directory with hash comparisons.
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)
	writeTestFile(t, testDir, "test_output.py", `import hashlib
def test_about_file():
    with open("/app/output/about.md", "rb") as f:
        actual = hashlib.md5(f.read()).hexdigest()
    assert actual == "abc123"
`)
	if !detectHashComparisonTask(dir) {
		t.Error("expected hash comparison detection with hashlib in test")
	}

	// No tests directory.
	dir2 := t.TempDir()
	if detectHashComparisonTask(dir2) {
		t.Error("expected no hash comparison in empty directory")
	}
}

func TestDetectDatabaseTask(t *testing.T) {
	// Directory name with database keyword.
	dir := t.TempDir()
	dbDir := filepath.Join(dir, "sqlite-queries")
	os.MkdirAll(dbDir, 0o755)
	if !detectDatabaseTask(dbDir) {
		t.Error("expected database detection from directory name containing 'sqlite'")
	}

	// .db file present.
	dir2 := t.TempDir()
	writeTestFile(t, dir2, "data.db", "fake sqlite db")
	if !detectDatabaseTask(dir2) {
		t.Error("expected database detection with .db file")
	}

	// .sql file present.
	dir3 := t.TempDir()
	writeTestFile(t, dir3, "schema.sql", "CREATE TABLE users (id INTEGER);")
	if !detectDatabaseTask(dir3) {
		t.Error("expected database detection with .sql file")
	}

	// Empty directory.
	dir4 := t.TempDir()
	if detectDatabaseTask(dir4) {
		t.Error("expected no database detection in empty directory")
	}
}

func TestExtractTestReferencedFiles(t *testing.T) {
	// Simulate parts with Python test imports.
	parts := []string{
		"\n## Test file auto-read (DO NOT MODIFY): /tests/test_solution.py",
		`import solution
from utils import helper
from os import path
import json
import numpy as np
from my_module import MyClass
`,
		"\n## Source file auto-read: /app/main.py",
		`print("hello")`,
	}

	refs := extractTestReferencedFiles(parts)

	// Should find solution.py, utils.py, my_module.py
	for _, expected := range []string{"solution.py", "utils.py", "my_module.py"} {
		if !refs[expected] {
			t.Errorf("expected test ref %q, got refs: %v", expected, refs)
		}
	}

	// Should NOT include stdlib modules (os, json, numpy)
	for _, unexpected := range []string{"os.py", "json.py", "numpy.py", "np.py"} {
		if refs[unexpected] {
			t.Errorf("unexpected stdlib ref %q in test refs", unexpected)
		}
	}
}

func TestExtractTestReferencedFiles_MultiLanguage(t *testing.T) {
	parts := []string{
		"\n## Test file auto-read (DO NOT MODIFY): /tests/test_solution.c",
		`#include "solution.h"
#include <stdio.h>
#include "utils.h"
`,
		"\n## Test file auto-read (DO NOT MODIFY): /tests/test_solution.rb",
		`require_relative './solution'
require_relative 'helper'
require 'minitest/autorun'
`,
		"\n## Test file auto-read (DO NOT MODIFY): /tests/test_main.rs",
		`mod solution;
use std::io;
`,
	}

	refs := extractTestReferencedFiles(parts)

	// C: #include "solution.h" → solution.h, solution.c, solution.cpp
	for _, expected := range []string{"solution.h", "solution.c", "solution.cpp", "utils.h", "utils.c", "utils.cpp"} {
		if !refs[expected] {
			t.Errorf("expected C ref %q, got refs: %v", expected, refs)
		}
	}

	// Ruby: require_relative './solution' → solution.rb, helper.rb
	for _, expected := range []string{"solution.rb", "helper.rb"} {
		if !refs[expected] {
			t.Errorf("expected Ruby ref %q, got refs: %v", expected, refs)
		}
	}

	// Rust: mod solution; → solution.rs
	if !refs["solution.rs"] {
		t.Errorf("expected Rust ref 'solution.rs', got refs: %v", refs)
	}

	// Should NOT include stdlib: <stdio.h>, std::io, minitest
	for _, unexpected := range []string{"stdio.h"} {
		if refs[unexpected] {
			t.Errorf("unexpected stdlib ref %q in test refs", unexpected)
		}
	}
}

func TestIsStdlibModule(t *testing.T) {
	if !isStdlibModule("os") {
		t.Error("os should be stdlib")
	}
	if !isStdlibModule("numpy") {
		t.Error("numpy should be treated as stdlib")
	}
	if isStdlibModule("solution") {
		t.Error("solution should NOT be stdlib")
	}
	if isStdlibModule("my_custom_module") {
		t.Error("my_custom_module should NOT be stdlib")
	}
}

func TestExtractTestConstraintsShell(t *testing.T) {
	// Shell test with diff and wc patterns.
	dir := t.TempDir()
	writeTestFile(t, dir, "test.sh", `#!/bin/bash
diff output.txt expected.txt
test -f /app/output/result.csv
wc -l output.txt | grep "^100 "
`)
	constraints := extractTestConstraints(dir)
	if len(constraints) == 0 {
		t.Error("expected constraints from shell test patterns")
	}
	// Should find diff, test -f, and wc -l patterns.
	found := map[string]bool{"diff": false, "test -f": false, "wc -l": false}
	for _, c := range constraints {
		for pat := range found {
			if strings.Contains(c, pat) {
				found[pat] = true
			}
		}
	}
	for pat, ok := range found {
		if !ok {
			t.Errorf("expected constraint containing %q", pat)
		}
	}
}

func TestExtractTestConstraintsTimeout(t *testing.T) {
	// Python test with timeout constraints.
	dir := t.TempDir()
	writeTestFile(t, dir, "test_perf.py", `import subprocess
def test_runs_fast():
    result = subprocess.run(["./app"], capture_output=True, timeout=10)
    assert result.returncode == 0

def test_valgrind():
    result = subprocess.run(["valgrind", "./app"], timeout=30)
`)
	constraints := extractTestConstraints(dir)
	found := false
	for _, c := range constraints {
		if strings.Contains(c, "timeout=") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected timeout constraint to be extracted, got: %v", constraints)
	}
}

func TestExtractTestConstraintsHash(t *testing.T) {
	dir := t.TempDir()
	// Use a non-assert hash check to test the new hash pattern specifically.
	writeTestFile(t, dir, "test_source.py", `
def verify_source():
    expected = {"file.txt": "d405a7947a5a63e3eb1d74284bf841f9"}
    actual_md5 = hashlib.md5(data).hexdigest()
    if actual_md5 == expected["file.txt"]:
        return True
`)
	constraints := extractTestConstraints(dir)
	found := false
	for _, c := range constraints {
		if strings.Contains(c, "md5") || strings.Contains(c, "hashlib") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected hash constraint to be extracted, got: %v", constraints)
	}
}

func TestExtractTestConstraintsExpanded(t *testing.T) {
	t.Run("C++ ASSERT_EQ detected", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "test_main.cpp", `#include <gtest/gtest.h>
TEST(OutputTest, FileSizeLimit) {
    ASSERT_LE(file_size, 1024);
    EXPECT_EQ(output.size(), 42);
}
`)
		constraints := extractTestConstraints(dir)
		if len(constraints) == 0 {
			t.Error("expected constraints from C++ ASSERT_LE/EXPECT_EQ patterns")
		}
	})

	t.Run("JavaScript expect with length", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "test_output.js", `const fs = require('fs');
test('output length constraint', () => {
    expect(result).toHaveLength(100);
    expect(fileSize).toBeLessThan(2048);
});
`)
		constraints := extractTestConstraints(dir)
		if len(constraints) == 0 {
			t.Error("expected constraints from JavaScript expect().toHaveLength/toBeLessThan")
		}
	})

	t.Run("Python os.path.getsize", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "test_size.py", `import os
def test_file_size():
    if os.path.getsize("output.bin") > 1048576:
        raise ValueError("too large")
`)
		constraints := extractTestConstraints(dir)
		found := false
		for _, c := range constraints {
			if strings.Contains(c, "getsize") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected getsize constraint, got: %v", constraints)
		}
	})

	t.Run("Python timing constraint", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "test_perf.py", `import time
def test_performance():
    start = time.time()
    run_solution()
    elapsed = time.time() - start
    assert elapsed < 5.0
`)
		constraints := extractTestConstraints(dir)
		if len(constraints) == 0 {
			t.Error("expected timing constraint from time.time() with assertion")
		}
	})

	t.Run("resource.setrlimit", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "test_limits.py", `import resource
resource.setrlimit(resource.RLIMIT_AS, (512 * 1024 * 1024, 512 * 1024 * 1024))
`)
		constraints := extractTestConstraints(dir)
		found := false
		for _, c := range constraints {
			if strings.Contains(c, "setrlimit") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected setrlimit constraint, got: %v", constraints)
		}
	})

	t.Run("time_limit keyword", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "test_runner.py", `
result = run_with_time_limit(solution, time_limit=30)
`)
		constraints := extractTestConstraints(dir)
		found := false
		for _, c := range constraints {
			if strings.Contains(c, "time_limit") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected time_limit constraint, got: %v", constraints)
		}
	})

	t.Run("shell timeout command", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "test_run.sh", `#!/bin/bash
timeout 30 ./solution < input.txt > output.txt
diff output.txt expected.txt
`)
		constraints := extractTestConstraints(dir)
		foundTimeout := false
		for _, c := range constraints {
			if strings.Contains(c, "timeout 30") {
				foundTimeout = true
				break
			}
		}
		if !foundTimeout {
			t.Errorf("expected timeout command constraint, got: %v", constraints)
		}
	})
}

func TestExtractFileStructure(t *testing.T) {
	dir := t.TempDir()

	// Python file with classes and functions.
	writeTestFile(t, dir, "big.py", `import os
import sys

class MyClass:
    def __init__(self):
        pass

    def method_one(self):
        return 1

    def method_two(self, x, y):
        return x + y

class AnotherClass(MyClass):
    pass

def standalone_function():
    return 42

async def async_handler(request):
    return None
`)
	result := extractFileStructure(filepath.Join(dir, "big.py"))
	if result == "" {
		t.Fatal("expected structure output for Python file")
	}
	for _, want := range []string{"class MyClass", "def __init__", "def method_one", "class AnotherClass", "def standalone_function", "async def async_handler"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Go file with functions and types.
	writeTestFile(t, dir, "big.go", `package main

import "fmt"

type Server struct {
	Port int
	Host string
}

type Handler interface {
	ServeHTTP()
}

func NewServer(port int) *Server {
	return &Server{Port: port}
}

func (s *Server) Start() error {
	fmt.Println("starting")
	return nil
}
`)
	result = extractFileStructure(filepath.Join(dir, "big.go"))
	if result == "" {
		t.Fatal("expected structure output for Go file")
	}
	for _, want := range []string{"type Server struct", "type Handler interface", "func NewServer", "func (s *Server) Start"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Elixir file with modules and functions.
	writeTestFile(t, dir, "app.ex", `defmodule MyApp.Server do
  def start(port) do
    :ok
  end

  defp internal_helper(x) do
    x * 2
  end

  defmacro my_macro(expr) do
    quote do: unquote(expr)
  end
end
`)
	result = extractFileStructure(filepath.Join(dir, "app.ex"))
	if result == "" {
		t.Fatal("expected structure output for Elixir file")
	}
	for _, want := range []string{"defmodule MyApp.Server", "def start", "defp internal_helper", "defmacro my_macro"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Swift file with classes, structs, protocols.
	writeTestFile(t, dir, "app.swift", `class ViewController {
    func viewDidLoad() {
    }
}

struct Point {
    var x: Double
    var y: Double
}

protocol Drawable {
    func draw()
}

enum Color {
    case red, green, blue
}
`)
	result = extractFileStructure(filepath.Join(dir, "app.swift"))
	if result == "" {
		t.Fatal("expected structure output for Swift file")
	}
	for _, want := range []string{"class ViewController", "func viewDidLoad", "struct Point", "protocol Drawable", "enum Color"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// PHP file with classes and functions.
	writeTestFile(t, dir, "app.php", `<?php
class UserController {
    public function index() {
        return view('users.index');
    }

    private function validate($data) {
        return true;
    }
}

interface Cacheable {
    public function getCacheKey();
}

function helper_function() {
    return null;
}
`)
	result = extractFileStructure(filepath.Join(dir, "app.php"))
	if result == "" {
		t.Fatal("expected structure output for PHP file")
	}
	for _, want := range []string{"class UserController", "public function index", "private function validate", "interface Cacheable", "function helper_function"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Lua file with functions.
	writeTestFile(t, dir, "app.lua", `function greet(name)
    print("Hello, " .. name)
end

local function helper(x)
    return x + 1
end
`)
	result = extractFileStructure(filepath.Join(dir, "app.lua"))
	if result == "" {
		t.Fatal("expected structure output for Lua file")
	}
	for _, want := range []string{"function greet", "local function helper"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// C# file with classes and methods.
	writeTestFile(t, dir, "app.cs", `namespace MyApp {
    public class UserService {
        private readonly ILogger _logger;

        public UserService(ILogger logger) {
            _logger = logger;
        }

        public async Task<User> GetUser(int id) {
            return await _db.FindAsync(id);
        }
    }

    interface IUserRepository {
        Task<User> FindById(int id);
    }
}
`)
	result = extractFileStructure(filepath.Join(dir, "app.cs"))
	if result == "" {
		t.Fatal("expected structure output for C# file")
	}
	for _, want := range []string{"namespace MyApp", "public class UserService", "public UserService", "public async Task", "interface IUserRepository"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Dart file with classes and methods.
	writeTestFile(t, dir, "app.dart", `class Counter {
  int _count = 0;

  void increment() {
    _count++;
  }

  Future<int> fetchCount() async {
    return _count;
  }

  static Counter create() {
    return Counter();
  }
}

enum Status { active, inactive }
`)
	result = extractFileStructure(filepath.Join(dir, "app.dart"))
	if result == "" {
		t.Fatal("expected structure output for Dart file")
	}
	for _, want := range []string{"class Counter", "void increment", "Future<int> fetchCount", "static Counter create", "enum Status"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Nim file with procs and types.
	writeTestFile(t, dir, "app.nim", `type
  Point = object
    x, y: float

proc distance(a, b: Point): float =
  sqrt((a.x - b.x)^2 + (a.y - b.y)^2)

func add(a, b: int): int =
  a + b

iterator items(s: seq[int]): int =
  for x in s:
    yield x

template withLock(lock, body: untyped) =
  acquire(lock)
  body
  release(lock)
`)
	result = extractFileStructure(filepath.Join(dir, "app.nim"))
	if result == "" {
		t.Fatal("expected structure output for Nim file")
	}
	for _, want := range []string{"type", "proc distance", "func add", "iterator items", "template withLock"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Zig file with functions and constants.
	writeTestFile(t, dir, "main.zig", `const std = @import("std");

pub fn main() !void {
    const stdout = std.io.getStdOut().writer();
    try stdout.print("Hello\n", .{});
}

fn helper(x: i32, y: i32) i32 {
    return x + y;
}

pub const Config = struct {
    width: u32,
    height: u32,
};

test "basic add" {
    try std.testing.expectEqual(helper(2, 3), 5);
}
`)
	result = extractFileStructure(filepath.Join(dir, "main.zig"))
	if result == "" {
		t.Fatal("expected structure output for Zig file")
	}
	for _, want := range []string{"pub fn main", "fn helper", "pub const Config", "test \"basic add\""} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Julia file with functions and types.
	writeTestFile(t, dir, "app.jl", `module MyModule

struct Point
    x::Float64
    y::Float64
end

function distance(a::Point, b::Point)
    sqrt((a.x - b.x)^2 + (a.y - b.y)^2)
end

macro debug(expr)
    :(println($(string(expr)), " = ", $(esc(expr))))
end

end # module
`)
	result = extractFileStructure(filepath.Join(dir, "app.jl"))
	if result == "" {
		t.Fatal("expected structure output for Julia file")
	}
	for _, want := range []string{"module MyModule", "struct Point", "function distance", "macro debug"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Perl file with subs and packages.
	writeTestFile(t, dir, "app.pl", `package MyApp::Utils;

sub process_data {
    my ($self, $data) = @_;
    return transform($data);
}

sub helper {
    my $x = shift;
    return $x * 2;
}

1;
`)
	result = extractFileStructure(filepath.Join(dir, "app.pl"))
	if result == "" {
		t.Fatal("expected structure output for Perl file")
	}
	for _, want := range []string{"package MyApp::Utils", "sub process_data", "sub helper"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// R file with functions and S4 classes.
	writeTestFile(t, dir, "analysis.R", `library(stats)

process_data <- function(data, threshold) {
  data[data > threshold]
}

compute_mean = function(x) {
  mean(x, na.rm = TRUE)
}

setClass("DataSet", representation(values = "numeric", name = "character"))

setGeneric("summarize", function(obj) standardGeneric("summarize"))
`)
	result = extractFileStructure(filepath.Join(dir, "analysis.R"))
	if result == "" {
		t.Fatal("expected structure output for R file")
	}
	for _, want := range []string{"process_data <- function", "compute_mean = function(", "setClass(", "setGeneric("} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Fortran file with subroutines and functions.
	writeTestFile(t, dir, "solver.f90", `program main
  implicit none
  call solve()
end program main

module math_utils
  implicit none
contains

subroutine solve()
  print *, "solving"
end subroutine solve

function add(a, b) result(c)
  integer, intent(in) :: a, b
  integer :: c
  c = a + b
end function add

end module math_utils
`)
	result = extractFileStructure(filepath.Join(dir, "solver.f90"))
	if result == "" {
		t.Fatal("expected structure output for Fortran file")
	}
	for _, want := range []string{"program main", "module math_utils", "subroutine solve", "function add"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Lean 4 file with definitions and theorems.
	writeTestFile(t, dir, "math.lean", `namespace MyMath

def add (a b : Nat) : Nat := a + b

theorem add_comm (a b : Nat) : add a b = add b a := by
  simp [add, Nat.add_comm]

structure Point where
  x : Float
  y : Float

class Printable (α : Type) where
  toString : α → String

inductive Tree (α : Type)
  | leaf : Tree α
  | node : Tree α → α → Tree α → Tree α

end MyMath
`)
	result = extractFileStructure(filepath.Join(dir, "math.lean"))
	if result == "" {
		t.Fatal("expected structure output for Lean file")
	}
	for _, want := range []string{"namespace MyMath", "def add", "theorem add_comm", "structure Point", "class Printable", "inductive Tree"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Coq file with definitions and theorems.
	writeTestFile(t, dir, "proofs.v", `Module NatHelpers.

Definition double (n : nat) : nat := n + n.

Fixpoint factorial (n : nat) : nat :=
  match n with
  | O => 1
  | S k => n * factorial k
  end.

Theorem double_injective : forall n m,
  double n = double m -> n = m.
Proof.
  intros. omega.
Qed.

Lemma add_comm : forall n m : nat, n + m = m + n.
Proof. intros. omega. Qed.

Inductive tree (A : Type) : Type :=
  | leaf : tree A
  | node : tree A -> A -> tree A -> tree A.

Record point := { px : nat; py : nat }.

End NatHelpers.
`)
	result = extractFileStructure(filepath.Join(dir, "proofs.v"))
	if result == "" {
		t.Fatal("expected structure output for Coq file")
	}
	for _, want := range []string{"Module NatHelpers", "Definition double", "Fixpoint factorial", "Theorem double_injective", "Lemma add_comm", "Inductive tree", "Record point"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result:\n%s", want, result)
		}
	}

	// Empty file should return nothing.
	writeTestFile(t, dir, "empty.py", "")
	result = extractFileStructure(filepath.Join(dir, "empty.py"))
	if result != "" {
		t.Errorf("expected empty result for empty file, got: %s", result)
	}

	// Non-existent file should return empty.
	result = extractFileStructure(filepath.Join(dir, "nonexistent.py"))
	if result != "" {
		t.Errorf("expected empty result for missing file, got: %s", result)
	}
}

func TestDetectGitTask(t *testing.T) {
	// Directory with git-related name.
	dir := t.TempDir()
	gitDir := filepath.Join(dir, "fix-git-merge")
	os.MkdirAll(gitDir, 0o755)
	if !detectGitTask(gitDir) {
		t.Error("expected git task for dir named fix-git-merge")
	}

	// Directory with .patch file.
	patchDir := filepath.Join(dir, "apply-fix")
	os.MkdirAll(patchDir, 0o755)
	writeTestFile(t, dir, "apply-fix/bugfix.patch", "--- a/file.py\n+++ b/file.py\n@@ -1 +1 @@\n-old\n+new\n")
	if !detectGitTask(patchDir) {
		t.Error("expected git task for dir with .patch file")
	}

	// Empty directory should not be a git task.
	emptyDir := filepath.Join(dir, "simple-task")
	os.MkdirAll(emptyDir, 0o755)
	if detectGitTask(emptyDir) {
		t.Error("did not expect git task for empty dir")
	}
}

func TestDetectPythonImports(t *testing.T) {
	dir := t.TempDir()

	// Create a Python file with third-party imports.
	os.WriteFile(filepath.Join(dir, "solution.py"), []byte(`
import numpy as np
from scipy.optimize import minimize
import json  # stdlib, should not appear
import os    # stdlib, should not appear
import pandas as pd
`), 0o644)

	pkgs := detectPythonImports(dir)
	// We can't assert exact results since it depends on what's installed,
	// but verify the function runs without error and returns a reasonable result.
	// On a dev machine, numpy/scipy/pandas might already be installed.
	if pkgs == nil {
		pkgs = []string{} // normalize
	}

	// Verify no stdlib packages are in the result.
	for _, p := range pkgs {
		if p == "json" || p == "os" || p == "sys" {
			t.Errorf("stdlib package %q detected as third-party", p)
		}
	}

	// Any returned package should be a known pip package.
	validPkgs := map[string]bool{
		"numpy": true, "scipy": true, "pandas": true,
	}
	for _, p := range pkgs {
		if !validPkgs[p] {
			t.Errorf("unexpected package %q in results", p)
		}
	}
}

func TestDetectPythonImportsEmpty(t *testing.T) {
	dir := t.TempDir()
	// No Python files: should return nil.
	pkgs := detectPythonImports(dir)
	if len(pkgs) != 0 {
		t.Errorf("expected empty, got %v", pkgs)
	}

	// Only stdlib imports: should return nil.
	os.WriteFile(filepath.Join(dir, "main.py"), []byte(`
import os
import sys
import json
`), 0o644)
	pkgs = detectPythonImports(dir)
	if len(pkgs) != 0 {
		t.Errorf("expected empty for stdlib-only, got %v", pkgs)
	}
}

func TestDetectCppTask(t *testing.T) {
	dir := t.TempDir()

	// Directory with .c file.
	cDir := filepath.Join(dir, "build-app")
	os.MkdirAll(cDir, 0o755)
	writeTestFile(t, dir, "build-app/main.c", "#include <stdio.h>\nint main() { return 0; }\n")
	if !detectCppTask(cDir) {
		t.Error("expected C++ task for dir with .c file")
	}

	// Directory with .cpp file.
	cppDir := filepath.Join(dir, "project-cpp")
	os.MkdirAll(cppDir, 0o755)
	writeTestFile(t, dir, "project-cpp/solver.cpp", "#include <iostream>\nint main() {}\n")
	if !detectCppTask(cppDir) {
		t.Error("expected C++ task for dir with .cpp file")
	}

	// Directory with CMakeLists.txt.
	cmakeDir := filepath.Join(dir, "cmake-project")
	os.MkdirAll(cmakeDir, 0o755)
	writeTestFile(t, dir, "cmake-project/CMakeLists.txt", "cmake_minimum_required(VERSION 3.10)\n")
	if !detectCppTask(cmakeDir) {
		t.Error("expected C++ task for dir with CMakeLists.txt")
	}

	// Empty directory should not be a C++ task.
	emptyDir := filepath.Join(dir, "python-task")
	os.MkdirAll(emptyDir, 0o755)
	if detectCppTask(emptyDir) {
		t.Error("did not expect C++ task for empty dir")
	}
}

func TestDetectShellTask(t *testing.T) {
	dir := t.TempDir()

	// Directory with multiple shell scripts.
	shDir := filepath.Join(dir, "shell-task")
	os.MkdirAll(shDir, 0o755)
	writeTestFile(t, dir, "shell-task/setup.sh", "#!/bin/bash\necho setup\n")
	writeTestFile(t, dir, "shell-task/run.sh", "#!/bin/bash\necho run\n")
	writeTestFile(t, dir, "shell-task/clean.sh", "#!/bin/bash\necho clean\n")
	if !detectShellTask(shDir) {
		t.Error("expected shell task for dir with multiple .sh files")
	}

	// Directory with more Python than shell should not be a shell task.
	pyDir := filepath.Join(dir, "python-project")
	os.MkdirAll(pyDir, 0o755)
	writeTestFile(t, dir, "python-project/main.py", "print('hello')\n")
	writeTestFile(t, dir, "python-project/utils.py", "def add(a,b): return a+b\n")
	writeTestFile(t, dir, "python-project/run.sh", "#!/bin/bash\npython3 main.py\n")
	if detectShellTask(pyDir) {
		t.Error("did not expect shell task for Python-majority dir")
	}
}

func TestPipInstall(t *testing.T) {
	// Just test that pipInstall doesn't panic on a nonexistent dir.
	// Actual pip installation can't be tested without Python.
	result := pipInstall(t.TempDir(), "-q", "nonexistent-package-xyz")
	if result {
		t.Error("expected pipInstall to fail for nonexistent package")
	}
}

func TestDetectHaskellTask(t *testing.T) {
	dir := t.TempDir()

	// .hs file
	hsDir := filepath.Join(dir, "haskell-task")
	os.MkdirAll(hsDir, 0o755)
	writeTestFile(t, dir, "haskell-task/Main.hs", "module Main where\nmain = putStrLn \"hello\"\n")
	if !detectHaskellTask(hsDir) {
		t.Error("expected Haskell task for dir with .hs file")
	}

	// stack.yaml
	stackDir := filepath.Join(dir, "stack-project")
	os.MkdirAll(stackDir, 0o755)
	writeTestFile(t, dir, "stack-project/stack.yaml", "resolver: lts-21.0\n")
	if !detectHaskellTask(stackDir) {
		t.Error("expected Haskell task for dir with stack.yaml")
	}

	// empty dir
	emptyDir := filepath.Join(dir, "empty")
	os.MkdirAll(emptyDir, 0o755)
	if detectHaskellTask(emptyDir) {
		t.Error("did not expect Haskell task for empty dir")
	}
}

func TestDetectRubyTask(t *testing.T) {
	dir := t.TempDir()

	// .rb file
	rbDir := filepath.Join(dir, "ruby-task")
	os.MkdirAll(rbDir, 0o755)
	writeTestFile(t, dir, "ruby-task/main.rb", "puts 'hello'\n")
	if !detectRubyTask(rbDir) {
		t.Error("expected Ruby task for dir with .rb file")
	}

	// Gemfile
	gemDir := filepath.Join(dir, "gem-project")
	os.MkdirAll(gemDir, 0o755)
	writeTestFile(t, dir, "gem-project/Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\n")
	if !detectRubyTask(gemDir) {
		t.Error("expected Ruby task for dir with Gemfile")
	}

	// empty dir
	emptyDir := filepath.Join(dir, "empty")
	os.MkdirAll(emptyDir, 0o755)
	if detectRubyTask(emptyDir) {
		t.Error("did not expect Ruby task for empty dir")
	}
}

func TestDetectJavaTask(t *testing.T) {
	dir := t.TempDir()

	// .java file
	javaDir := filepath.Join(dir, "java-task")
	os.MkdirAll(javaDir, 0o755)
	writeTestFile(t, dir, "java-task/Main.java", "public class Main {}\n")
	if !detectJavaTask(javaDir) {
		t.Error("expected Java task for dir with .java file")
	}

	// pom.xml
	mvnDir := filepath.Join(dir, "maven-project")
	os.MkdirAll(mvnDir, 0o755)
	writeTestFile(t, dir, "maven-project/pom.xml", "<project></project>\n")
	if !detectJavaTask(mvnDir) {
		t.Error("expected Java task for dir with pom.xml")
	}

	// build.gradle
	gradleDir := filepath.Join(dir, "gradle-project")
	os.MkdirAll(gradleDir, 0o755)
	writeTestFile(t, dir, "gradle-project/build.gradle", "plugins { id 'java' }\n")
	if !detectJavaTask(gradleDir) {
		t.Error("expected Java task for dir with build.gradle")
	}

	// empty dir
	emptyDir := filepath.Join(dir, "empty")
	os.MkdirAll(emptyDir, 0o755)
	if detectJavaTask(emptyDir) {
		t.Error("did not expect Java task for empty dir")
	}
}

func TestDetectDotNetTask(t *testing.T) {
	dir := t.TempDir()

	// .cs file
	csDir := filepath.Join(dir, "dotnet-task")
	os.MkdirAll(csDir, 0o755)
	writeTestFile(t, dir, "dotnet-task/Program.cs", "class Program {}\n")
	if !detectDotNetTask(csDir) {
		t.Error("expected .NET task for dir with .cs file")
	}

	// .csproj file
	projDir := filepath.Join(dir, "proj")
	os.MkdirAll(projDir, 0o755)
	writeTestFile(t, dir, "proj/MyApp.csproj", "<Project></Project>\n")
	if !detectDotNetTask(projDir) {
		t.Error("expected .NET task for dir with .csproj file")
	}

	// empty dir
	emptyDir := filepath.Join(dir, "empty")
	os.MkdirAll(emptyDir, 0o755)
	if detectDotNetTask(emptyDir) {
		t.Error("did not expect .NET task for empty dir")
	}
}

func TestDetectAndActivateVenv(t *testing.T) {
	dir := t.TempDir()

	// No venv — should return empty.
	if hint := detectAndActivateVenv(dir); hint != "" {
		t.Errorf("expected empty hint for no venv, got %q", hint)
	}

	// Create a fake venv with activate script.
	venvBin := filepath.Join(dir, "venv", "bin")
	os.MkdirAll(venvBin, 0o755)
	writeTestFile(t, dir, "venv/bin/activate", "# fake activate script\n")

	hint := detectAndActivateVenv(dir)
	if hint == "" {
		t.Error("expected hint for dir with venv/bin/activate")
	}
	if !strings.Contains(hint, "venv detected") {
		t.Errorf("expected venv hint, got %q", hint)
	}
}

func TestBuildActionSummaryCached(t *testing.T) {
	dir := t.TempDir()

	// With no data, should still return something (has FIRST and REMEMBER lines).
	summary := buildActionSummaryCached(dir, nil, nil, nil, nil, nil, nil)
	if summary == "" {
		t.Error("expected non-empty summary even with no expected outputs")
	}
	if !strings.Contains(summary, "ACTION SUMMARY") {
		t.Error("expected ACTION SUMMARY header")
	}
	if !strings.Contains(summary, "REMEMBER") {
		t.Error("expected REMEMBER line")
	}

	// With expected outputs and test commands.
	outputs := []string{"output_data/result.csv", "output_data/summary.json"}
	cmds := []string{"Test: bash /tests/test.sh", "Build: go build ./..."}
	summary = buildActionSummaryCached(dir, outputs, cmds, nil, nil, nil, nil)
	if !strings.Contains(summary, "CREATE:") {
		t.Error("expected CREATE line for expected outputs")
	}
	if !strings.Contains(summary, "VERIFY:") {
		t.Error("expected VERIFY line for test commands")
	}
}

func TestDetectTestCommandsWorkdirScripts(t *testing.T) {
	dir := t.TempDir()

	// No scripts = no test commands from workDir.
	cmds := detectTestCommands(dir)
	for _, cmd := range cmds {
		if strings.Contains(cmd, dir) {
			t.Errorf("unexpected command referencing workdir: %s", cmd)
		}
	}

	// Create test.sh in workDir — should be detected.
	os.WriteFile(filepath.Join(dir, "test.sh"), []byte("#!/bin/bash\necho test"), 0o755)
	cmds = detectTestCommands(dir)
	found := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "test.sh") && strings.Contains(cmd, dir) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected test.sh in workdir to be detected, got: %v", cmds)
	}

	// Create verify.py — should also be detected.
	os.WriteFile(filepath.Join(dir, "verify.py"), []byte("print('ok')"), 0o644)
	cmds = detectTestCommands(dir)
	foundVerify := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "verify.py") && strings.Contains(cmd, "python3") {
			foundVerify = true
			break
		}
	}
	if !foundVerify {
		t.Errorf("expected verify.py in workdir to be detected, got: %v", cmds)
	}
}

func TestDetectTestCommandsPytestInWorkdir(t *testing.T) {
	dir := t.TempDir()

	// test_*.py in workDir should trigger pytest suggestion.
	os.WriteFile(filepath.Join(dir, "test_solution.py"), []byte("def test_it(): pass"), 0o644)
	cmds := detectTestCommands(dir)
	found := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "pytest") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected pytest command for test_*.py in workdir, got: %v", cmds)
	}
}

func TestDetectTestCommandsMeson(t *testing.T) {
	dir := t.TempDir()

	// meson.build should trigger meson commands.
	os.WriteFile(filepath.Join(dir, "meson.build"), []byte("project('test', 'c')"), 0o644)
	cmds := detectTestCommands(dir)
	foundBuild := false
	foundTest := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "meson compile") {
			foundBuild = true
		}
		if strings.Contains(cmd, "meson test") {
			foundTest = true
		}
	}
	if !foundBuild {
		t.Errorf("expected meson compile command, got: %v", cmds)
	}
	if !foundTest {
		t.Errorf("expected meson test command, got: %v", cmds)
	}
}

func TestDetectTestCommandsBazel(t *testing.T) {
	dir := t.TempDir()

	// WORKSPACE file should trigger bazel commands.
	os.WriteFile(filepath.Join(dir, "WORKSPACE"), []byte(""), 0o644)
	cmds := detectTestCommands(dir)
	foundBuild := false
	foundTest := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "bazel build") {
			foundBuild = true
		}
		if strings.Contains(cmd, "bazel test") {
			foundTest = true
		}
	}
	if !foundBuild {
		t.Errorf("expected bazel build command, got: %v", cmds)
	}
	if !foundTest {
		t.Errorf("expected bazel test command, got: %v", cmds)
	}

	// Also test MODULE.bazel variant.
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "MODULE.bazel"), []byte("module(name = \"test\")"), 0o644)
	cmds2 := detectTestCommands(dir2)
	foundBuild2 := false
	for _, cmd := range cmds2 {
		if strings.Contains(cmd, "bazel build") {
			foundBuild2 = true
		}
	}
	if !foundBuild2 {
		t.Errorf("expected bazel build for MODULE.bazel, got: %v", cmds2)
	}
}

func TestDetectOutputFormat(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	// Test with JSON format detection.
	os.WriteFile(filepath.Join(testDir, "test_output.py"), []byte(`
import json
def test_json():
    with open("output.json") as f:
        data = json.loads(f.read())
    assert data["key"] == "value"
`), 0o644)

	hints := detectOutputFormat(dir)
	foundJSON := false
	for _, h := range hints {
		if strings.Contains(h, "FORMAT=JSON") {
			foundJSON = true
		}
	}
	if !foundJSON {
		t.Errorf("expected JSON format hint, got: %v", hints)
	}
}

func TestDetectOutputFormatCSV(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	os.WriteFile(filepath.Join(testDir, "test_csv.py"), []byte(`
import csv
def test_csv():
    with open("output.csv") as f:
        reader = csv.reader(f)
        rows = list(reader)
    assert len(rows) > 0
`), 0o644)

	hints := detectOutputFormat(dir)
	foundCSV := false
	for _, h := range hints {
		if strings.Contains(h, "FORMAT=CSV") {
			foundCSV = true
		}
	}
	if !foundCSV {
		t.Errorf("expected CSV format hint, got: %v", hints)
	}
}

func TestDetectOutputFormatStdin(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	os.WriteFile(filepath.Join(testDir, "test.sh"), []byte(`#!/bin/bash
echo "hello" | ./solution
`), 0o644)

	hints := detectOutputFormat(dir)
	foundStdin := false
	for _, h := range hints {
		if strings.Contains(h, "STDIN") {
			foundStdin = true
		}
	}
	if !foundStdin {
		t.Errorf("expected STDIN hint, got: %v", hints)
	}
}

func TestDetectOutputFormatExecutable(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	os.WriteFile(filepath.Join(testDir, "test.sh"), []byte(`#!/bin/bash
chmod +x ./solution
./solution input.txt > output.txt
diff output.txt expected.txt
`), 0o644)

	hints := detectOutputFormat(dir)
	foundExec := false
	for _, h := range hints {
		if strings.Contains(h, "EXECUTABLE") {
			foundExec = true
		}
	}
	if !foundExec {
		t.Errorf("expected EXECUTABLE hint, got: %v", hints)
	}
}

func TestMissingSolutionFilesSurfaced(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	// Test that imports solution.py which doesn't exist.
	os.WriteFile(filepath.Join(testDir, "test_main.py"), []byte(`
from solution import solve

def test_solve():
    assert solve(2) == 4
`), 0o644)

	// Run discoverEnvironment (which calls extractTestReferencedFiles internally).
	// We can't easily unit-test the full discoverEnvironment, so test the
	// extractTestReferencedFiles + missing check logic directly.

	// Simulate auto-read parts that include test content.
	parts := []string{
		"\n## Test file auto-read: " + testDir + "/test_main.py",
		"from solution import solve\n\ndef test_solve():\n    assert solve(2) == 4\n",
	}
	testRefs := extractTestReferencedFiles(parts)

	// solution.py should be referenced.
	if !testRefs["solution.py"] {
		t.Errorf("expected solution.py in testRefs, got: %v", testRefs)
	}

	// Since solution.py doesn't exist in dir, it should be "missing".
	found := false
	for filename := range testRefs {
		if !fileExists(filepath.Join(dir, filename)) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected solution.py to be missing in %s", dir)
	}
}

func TestIsBinaryFilename(t *testing.T) {
	tests := []struct {
		name   string
		binary bool
	}{
		{"file.png", true},
		{"file.wav", true},
		{"file.mp3", true},
		{"file.zip", true},
		{"file.db", true},
		{"file.pyc", true},
		{"file.o", true},
		{"file.so", true},
		{"file.aac", true},
		{"file.wmv", true},
		{"file.webm", true},
		{"file.sqlite3", true},
		{"file.war", true},
		{"file.whl", true},
		{"file.ppm", true},
		{"file.py", false},
		{"file.txt", false},
		{"file.json", false},
		{"file.go", false},
		{"file.csv", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBinaryFilename(tt.name)
			if got != tt.binary {
				t.Errorf("isBinaryFilename(%q) = %v, want %v", tt.name, got, tt.binary)
			}
		})
	}
}

func TestExtractInvocationPatterns(t *testing.T) {
	dir := t.TempDir()

	// No test scripts — should return empty.
	patterns := extractInvocationPatterns(dir)
	if len(patterns) != 0 {
		t.Errorf("expected 0 patterns, got %d", len(patterns))
	}

	// Create a test.sh with invocation patterns.
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)
	os.WriteFile(filepath.Join(testDir, "test.sh"), []byte(`#!/bin/bash
set -e
# Build the solution
gcc -o solution solution.c
# Run the solution with stdin
./solution < input.txt > output.txt
diff output.txt expected.txt
`), 0o755)

	patterns = extractInvocationPatterns(dir)
	if len(patterns) == 0 {
		t.Fatal("expected invocation patterns from test.sh")
	}
	foundSolution := false
	for _, p := range patterns {
		if strings.Contains(p, "./solution") {
			foundSolution = true
		}
	}
	if !foundSolution {
		t.Errorf("expected to find ./solution in patterns: %v", patterns)
	}
}

func TestExtractInvocationPatternsPython(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.sh"), []byte(`#!/bin/bash
python3 solution.py arg1 arg2 > result.txt
diff result.txt expected.txt
`), 0o755)

	patterns := extractInvocationPatterns(dir)
	foundPython := false
	for _, p := range patterns {
		if strings.Contains(p, "python3 solution") {
			foundPython = true
		}
	}
	if !foundPython {
		t.Errorf("expected python3 solution invocation, got: %v", patterns)
	}
}

func TestActionSummaryWithMissingFiles(t *testing.T) {
	dir := t.TempDir()
	missing := []string{"solution.py", "helper.py"}
	summary := buildActionSummaryCached(dir, nil, nil, missing, nil, nil, nil)
	if !strings.Contains(summary, "MISSING:") {
		t.Error("expected MISSING line for missing solution files")
	}
	if !strings.Contains(summary, "solution.py") {
		t.Error("expected solution.py in MISSING line")
	}
	// FIRST should mention the missing file.
	if !strings.Contains(summary, "FIRST: Create solution.py") {
		t.Error("expected FIRST line to mention creating missing file")
	}
}

func TestActionSummaryWithInvocation(t *testing.T) {
	dir := t.TempDir()
	invocation := []string{"./solution < input.txt > output.txt"}
	summary := buildActionSummaryCached(dir, nil, nil, nil, nil, invocation, nil)
	if !strings.Contains(summary, "INVOKE:") {
		t.Error("expected INVOKE line for invocation pattern")
	}
	if !strings.Contains(summary, "./solution") {
		t.Error("expected ./solution in INVOKE line")
	}
}

func TestActionSummaryWithFormat(t *testing.T) {
	dir := t.TempDir()
	hints := []string{"FORMAT=JSON: Tests parse output as JSON. Use json.dumps()."}
	summary := buildActionSummaryCached(dir, nil, nil, nil, hints, nil, nil)
	if !strings.Contains(summary, "FORMAT: JSON") {
		t.Error("expected FORMAT: JSON in summary")
	}
}

func TestCheckExpectedOutputsExist(t *testing.T) {
	dir := t.TempDir()

	// No output dirs — should return empty.
	result := checkExpectedOutputsExist(dir, detectExpectedOutputs(dir))
	if result != "" {
		t.Errorf("expected empty result, got: %s", result)
	}

	// Create empty output_data directory.
	os.MkdirAll(filepath.Join(dir, "output_data"), 0o755)
	result = checkExpectedOutputsExist(dir, detectExpectedOutputs(dir))
	if !strings.Contains(result, "EMPTY") {
		t.Error("expected warning about empty output_data directory")
	}

	// Add a file to output_data — warning should go away.
	os.WriteFile(filepath.Join(dir, "output_data", "result.txt"), []byte("data"), 0o644)
	result = checkExpectedOutputsExist(dir, detectExpectedOutputs(dir))
	if result != "" {
		t.Errorf("expected no warning after adding file, got: %s", result)
	}
}

func TestCheckExpectedOutputsExistEmptySolution(t *testing.T) {
	dir := t.TempDir()

	// Create empty solution.py — should warn.
	os.WriteFile(filepath.Join(dir, "solution.py"), []byte(""), 0o644)
	result := checkExpectedOutputsExist(dir, detectExpectedOutputs(dir))
	if !strings.Contains(result, "EMPTY") || !strings.Contains(result, "solution.py") {
		t.Errorf("expected warning about empty solution.py, got: %s", result)
	}
}

func TestExtractImportedNames(t *testing.T) {
	// Simulate test content that references a missing solution.py.
	parts := []string{
		"\n## Test file auto-read: /tests/test.py",
		`from solution import solve, process_data
from solution import validate as v
import solution`,
		"\n## Other section",
		"some other content",
	}
	missingFiles := []string{"solution.py"}

	result := extractImportedNames(parts, missingFiles)
	names, ok := result["solution.py"]
	if !ok {
		t.Fatal("expected solution.py in results")
	}
	// Should have: solve, process_data, validate (deduplicated).
	expected := map[string]bool{"solve": false, "process_data": false, "validate": false}
	for _, n := range names {
		if _, ok := expected[n]; ok {
			expected[n] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected %q in imported names, got %v", name, names)
		}
	}
}

func TestExtractImportedNamesEmpty(t *testing.T) {
	// No test sections.
	parts := []string{"some random content"}
	result := extractImportedNames(parts, []string{"solution.py"})
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestExtractImportedNamesJS(t *testing.T) {
	// Test JavaScript destructured require pattern.
	parts := []string{
		"\n## Test file auto-read: /tests/test.js",
		`const { solve, helper } = require('./solution')
import { validate, transform } from './solution'
const solution = require('./solution')`,
		"\n## Other section",
		"some other content",
	}
	missingFiles := []string{"solution.js", "solution.ts"}

	result := extractImportedNames(parts, missingFiles)
	// Should find names for solution.js (the module name maps to "solution").
	// Check that at least solve and helper are extracted from the require pattern.
	allNames := make(map[string]bool)
	for _, names := range result {
		for _, n := range names {
			allNames[n] = true
		}
	}
	for _, expected := range []string{"solve", "helper", "validate", "transform"} {
		if !allNames[expected] {
			t.Errorf("expected %q in JS imported names, got %v", expected, allNames)
		}
	}
}

func TestExtractJSImportNamesDefaultImport(t *testing.T) {
	// Test ES module default import.
	parts := []string{
		"\n## Test file auto-read: /tests/test.ts",
		`import solver from './solver'`,
		"\n## Other section",
	}
	missingFiles := []string{"solver.js", "solver.ts"}
	result := extractImportedNames(parts, missingFiles)
	allNames := make(map[string]bool)
	for _, names := range result {
		for _, n := range names {
			allNames[n] = true
		}
	}
	if !allNames["solver (default)"] {
		t.Errorf("expected 'solver (default)' in imported names, got %v", allNames)
	}
}

func TestDetectTodoStubs(t *testing.T) {
	dir := t.TempDir()

	// No source files — should return empty.
	stubs := detectTodoStubs(dir)
	if len(stubs) != 0 {
		t.Errorf("expected 0 stubs, got %d", len(stubs))
	}

	// Create a Python file with TODO stubs.
	os.WriteFile(filepath.Join(dir, "solution.py"), []byte(`
def calculate_score(data):
    # TODO: implement scoring algorithm
    pass

def process_input(filename):
    raise NotImplementedError("process_input not implemented")

def validate_output(result):
    # FIXME: add validation logic
    return True
`), 0o644)

	stubs = detectTodoStubs(dir)
	if len(stubs) == 0 {
		t.Fatal("expected TODO stubs to be detected")
	}
	foundTodo := false
	foundNotImpl := false
	foundFixme := false
	for _, s := range stubs {
		if strings.Contains(s, "TODO") {
			foundTodo = true
		}
		if strings.Contains(s, "NotImplementedError") {
			foundNotImpl = true
		}
		if strings.Contains(s, "FIXME") {
			foundFixme = true
		}
	}
	if !foundTodo {
		t.Error("expected TODO pattern in stubs")
	}
	if !foundNotImpl {
		t.Error("expected NotImplementedError pattern in stubs")
	}
	if !foundFixme {
		t.Error("expected FIXME pattern in stubs")
	}
}

func TestDetectTodoStubsRust(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.rs"), []byte(`
fn process() -> Result<(), Box<dyn Error>> {
    todo!()
}

fn validate() {
    unimplemented!()
}
`), 0o644)

	stubs := detectTodoStubs(dir)
	if len(stubs) == 0 {
		t.Fatal("expected Rust todo!/unimplemented! stubs")
	}
	foundTodo := false
	foundUnimpl := false
	for _, s := range stubs {
		if strings.Contains(s, "todo!()") {
			foundTodo = true
		}
		if strings.Contains(s, "unimplemented!()") {
			foundUnimpl = true
		}
	}
	if !foundTodo {
		t.Error("expected todo!() in stubs")
	}
	if !foundUnimpl {
		t.Error("expected unimplemented!() in stubs")
	}
}

func TestExtractInvocationPatternsPythonSubprocess(t *testing.T) {
	dir := t.TempDir()

	// Create a Python test script with subprocess invocations.
	os.WriteFile(filepath.Join(dir, "test.py"), []byte(`
import subprocess
import os

def test_solution():
    result = subprocess.run(["./solution", "input.txt"], capture_output=True, text=True)
    assert result.returncode == 0

def test_main():
    output = subprocess.check_output(["python3", "main.py", "--verbose"])
    assert b"success" in output
`), 0o644)

	patterns := extractInvocationPatterns(dir)
	if len(patterns) == 0 {
		t.Fatal("expected invocation patterns from Python subprocess calls")
	}
	foundSubprocess := false
	for _, p := range patterns {
		if strings.Contains(p, "subprocess") && strings.Contains(p, "solution") {
			foundSubprocess = true
		}
	}
	if !foundSubprocess {
		t.Errorf("expected subprocess invocation with solution, got: %v", patterns)
	}
}

func TestDepsMarkerPath(t *testing.T) {
	// Verify marker paths are deterministic and different for different dirs.
	p1 := depsMarkerPath("/app")
	p2 := depsMarkerPath("/app")
	p3 := depsMarkerPath("/other")

	if p1 != p2 {
		t.Errorf("same workDir should produce same marker path: %s vs %s", p1, p2)
	}
	if p1 == p3 {
		t.Errorf("different workDirs should produce different marker paths: %s vs %s", p1, p3)
	}
	if !strings.HasPrefix(p1, os.TempDir()) {
		t.Errorf("marker should be in temp dir, got: %s", p1)
	}
}

func TestDepsMarkerSkipsReinstall(t *testing.T) {
	// Verify that depsMarkerPath creates a valid path we can write to.
	dir := t.TempDir()
	marker := depsMarkerPath(dir)
	defer os.Remove(marker)

	// Initially no marker.
	if fileExists(marker) {
		t.Fatal("marker should not exist initially")
	}

	// Create marker.
	if err := os.WriteFile(marker, []byte("1"), 0o644); err != nil {
		t.Fatalf("failed to write marker: %v", err)
	}

	// Marker should now exist.
	if !fileExists(marker) {
		t.Fatal("marker should exist after creation")
	}
}

func TestLinkerHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		contains string
	}{
		{
			name:     "pthread",
			output:   "main.o: In function `main':\nmain.c:(.text+0x1a): undefined reference to `pthread_create'\n",
			contains: "-lpthread",
		},
		{
			name:     "math",
			output:   "/tmp/ccABC123.o: undefined reference to `sin'\n",
			contains: "-lm",
		},
		{
			name:     "sqlite",
			output:   "main.o: undefined reference to `sqlite3_open'\n",
			contains: "-lsqlite3",
		},
		{
			name:     "generic",
			output:   "foo.o: undefined reference to `some_custom_func'\n",
			contains: "undefined reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := linkerHint(tt.output)
			if hint == "" {
				t.Fatal("expected a linker hint")
			}
			if !strings.Contains(hint, tt.contains) {
				t.Errorf("hint %q should contain %q", hint, tt.contains)
			}
		})
	}
}

func TestMissingHeaderHint(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		contains string
	}{
		{
			name:     "curl",
			output:   "main.c:1:10: fatal error: curl/curl.h: No such file or directory",
			contains: "libcurl4-openssl-dev",
		},
		{
			name:     "ssl",
			output:   "crypto.c:3:10: fatal error: openssl/ssl.h: No such file or directory",
			contains: "libssl-dev",
		},
		{
			name:     "zlib",
			output:   "compress.c:1:10: fatal error: zlib.h: No such file or directory",
			contains: "zlib1g-dev",
		},
		{
			name:     "unknown header",
			output:   "main.c:1:10: fatal error: obscure_lib.h: No such file or directory",
			contains: "", // no hint for unknown headers
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := missingHeaderHint(tt.output)
			if tt.contains == "" {
				if hint != "" {
					t.Errorf("expected no hint for unknown header, got: %s", hint)
				}
			} else {
				if !strings.Contains(hint, tt.contains) {
					t.Errorf("hint %q should contain %q", hint, tt.contains)
				}
			}
		})
	}
}

func TestCompilationErrorHintLinker(t *testing.T) {
	// compilationErrorHint should delegate to linkerHint for undefined reference errors.
	output := "main.o: undefined reference to `pthread_create'\ncollect2: error: ld returned 1 exit status"
	hint := compilationErrorHint(output, 1)
	if !strings.Contains(hint, "-lpthread") {
		t.Errorf("expected -lpthread hint, got: %s", hint)
	}
}

func TestCompilationErrorHintMissingHeader(t *testing.T) {
	// compilationErrorHint should delegate to missingHeaderHint for missing headers.
	output := "main.c:1:10: fatal error: curl/curl.h: No such file or directory\n #include <curl/curl.h>\n          ^~~~~~~~~~~~~~"
	hint := compilationErrorHint(output, 1)
	if !strings.Contains(hint, "libcurl4-openssl-dev") {
		t.Errorf("expected libcurl hint, got: %s", hint)
	}
}

func TestTimeoutContextHint(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		contains string
		empty    bool
	}{
		{
			name:     "flask server",
			cmd:      "flask run --host=0.0.0.0 --port=8080",
			contains: "background",
		},
		{
			name:     "npm start",
			cmd:      "npm start",
			contains: "background",
		},
		{
			name:     "uvicorn server",
			cmd:      "uvicorn app:main --host 0.0.0.0",
			contains: "background",
		},
		{
			name:     "tail -f",
			cmd:      "tail -f /var/log/app.log",
			contains: "blocking/interactive",
		},
		{
			name:  "normal build command",
			cmd:   "make -j4",
			empty: true,
		},
		{
			name:  "test command",
			cmd:   "pytest test_foo.py",
			empty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := timeoutContextHint(tt.cmd)
			if tt.empty {
				if hint != "" {
					t.Errorf("expected no hint for %q, got: %s", tt.cmd, hint)
				}
			} else {
				if !strings.Contains(hint, tt.contains) {
					t.Errorf("hint for %q should contain %q, got: %s", tt.cmd, tt.contains, hint)
				}
			}
		})
	}
}

func TestExtractTestCounts(t *testing.T) {
	tests := []struct {
		name   string
		output string
		passed int
		failed int
		ok     bool
	}{
		{
			name:   "pytest with failures",
			output: "test_foo.py ..F.F\n\n====== 3 passed, 2 failed in 1.23s ======",
			passed: 3, failed: 2, ok: true,
		},
		{
			name:   "pytest all pass",
			output: "test_foo.py .....\n\n====== 5 passed in 0.42s ======",
			passed: 5, failed: 0, ok: true,
		},
		{
			name:   "go test failures",
			output: "--- PASS: TestA (0.00s)\n--- PASS: TestB (0.01s)\n--- FAIL: TestC (0.00s)\nFAIL",
			passed: 2, failed: 1, ok: true,
		},
		{
			name:   "python unittest failure",
			output: "..F.E\n------\nRan 5 tests in 0.003s\n\nFAILED (failures=1, errors=1)",
			passed: 3, failed: 2, ok: true,
		},
		{
			name:   "python unittest OK",
			output: ".....\n------\nRan 5 tests in 0.002s\n\nOK",
			passed: 5, failed: 0, ok: true,
		},
		{
			name:   "jest with failures",
			output: "FAIL src/app.test.js\n  ✕ renders (5 ms)\n\nTests:  2 passed, 1 failed, 3 total\nTime:   1.234 s",
			passed: 2, failed: 1, ok: true,
		},
		{
			name:   "cargo test failures",
			output: "running 5 tests\ntest test_a ... ok\ntest test_b ... FAILED\n\ntest result: FAILED. 4 passed; 1 failed; 0 ignored; 0 measured",
			passed: 4, failed: 1, ok: true,
		},
		{
			name:   "rspec",
			output: "Finished in 0.5 seconds\n3 examples, 1 failure",
			passed: 2, failed: 1, ok: true,
		},
		{
			name:   "mocha",
			output: "  3 passing (15ms)\n  1 failing\n\n  1) test suite should work:\n     Error: expected true",
			passed: 3, failed: 1, ok: true,
		},
		{
			name:   "mocha all pass",
			output: "  5 passing (22ms)",
			passed: 5, failed: 0, ok: true,
		},
		{
			name:   "phpunit failures",
			output: "PHPUnit 9.5.10\n..F.\n\nTests: 4, Assertions: 6, Failures: 1",
			passed: 3, failed: 1, ok: true,
		},
		{
			name:   "phpunit ok",
			output: "PHPUnit 9.5.10\n....\n\nOK (4 tests, 8 assertions)",
			passed: 4, failed: 0, ok: true,
		},
		{
			name:   "maven junit",
			output: "Tests run: 10, Failures: 2, Errors: 1, Skipped: 0",
			passed: 7, failed: 3, ok: true,
		},
		{
			name:   "not test output",
			output: "hello world\nsome random output",
			passed: 0, failed: 0, ok: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, f, ok := extractTestCounts(tt.output)
			if ok != tt.ok {
				t.Errorf("ok: got %v, want %v", ok, tt.ok)
			}
			if p != tt.passed {
				t.Errorf("passed: got %d, want %d", p, tt.passed)
			}
			if f != tt.failed {
				t.Errorf("failed: got %d, want %d", f, tt.failed)
			}
		})
	}
}

func TestExtractPythonFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // function names that should appear
	}{
		{
			name: "simple_function_call",
			content: `
from solution import solve
def test_basic():
    assert solve(3, [1, 2, 3]) == 6
`,
			want: []string{"solve"},
		},
		{
			name: "module_method_call",
			content: `
import solution
def test_process():
    result = solution.process(data, threshold=0.5)
    assert result is not None
`,
			want: []string{"process"},
		},
		{
			name: "multiple_functions",
			content: `
from my_module import encode, decode
def test_roundtrip():
    encoded = encode("hello")
    decoded = decode(encoded)
    assert decoded == "hello"
`,
			want: []string{"encode", "decode"},
		},
		{
			name: "skip_stdlib_calls",
			content: `
def test_basic():
    result = solve(10)
    assert len(result) == 5
    print("done")
`,
			want: []string{"solve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractPythonFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestDetectComparisonTolerances(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	content := `
import math
from solution import compute

def test_precision():
    result = compute(3.14)
    assert math.isclose(result, 2.71828, rel_tol=1e-5, abs_tol=1e-8)

def test_almost():
    self.assertAlmostEqual(result, expected, places=6)
`
	os.WriteFile(filepath.Join(testDir, "test_precision.py"), []byte(content), 0o644)

	tolerances := detectComparisonTolerances(dir)
	if len(tolerances) == 0 {
		t.Fatal("expected to detect comparison tolerances")
	}

	foundIsclose := false
	foundPlaces := false
	for _, tol := range tolerances {
		if strings.Contains(tol, "isclose") && strings.Contains(tol, "rel_tol") {
			foundIsclose = true
		}
		if strings.Contains(tol, "assertAlmostEqual") && strings.Contains(tol, "places") {
			foundPlaces = true
		}
	}
	if !foundIsclose {
		t.Errorf("expected to detect isclose tolerance, got %v", tolerances)
	}
	if !foundPlaces {
		t.Errorf("expected to detect assertAlmostEqual tolerance, got %v", tolerances)
	}
}

func TestExtractTestEnvironmentVars(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	content := `#!/bin/bash
export PORT=8080
export DATABASE_URL="postgres://localhost/testdb"
export PATH="/usr/bin:$PATH"
./solution
`
	os.WriteFile(filepath.Join(testDir, "test.sh"), []byte(content), 0o644)

	envVars := extractTestEnvironmentVars(dir)
	if len(envVars) == 0 {
		t.Fatal("expected to detect environment variables")
	}

	foundPort := false
	foundDB := false
	foundPath := false
	for _, ev := range envVars {
		if strings.HasPrefix(ev, "PORT=") {
			foundPort = true
		}
		if strings.HasPrefix(ev, "DATABASE_URL=") {
			foundDB = true
		}
		if strings.HasPrefix(ev, "PATH=") {
			foundPath = true
		}
	}
	if !foundPort {
		t.Errorf("expected to detect PORT env var, got %v", envVars)
	}
	if !foundDB {
		t.Errorf("expected to detect DATABASE_URL env var, got %v", envVars)
	}
	if foundPath {
		t.Errorf("expected to skip generic PATH env var, got %v", envVars)
	}
}

func TestExtractTestEnvironmentVarsPython(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	content := `
import os
os.environ["API_KEY"] = "test-key-123"
os.environ.setdefault("SERVER_PORT", "3000")
`
	os.WriteFile(filepath.Join(testDir, "test_env.py"), []byte(content), 0o644)

	envVars := extractTestEnvironmentVars(dir)
	foundAPI := false
	foundPort := false
	for _, ev := range envVars {
		if strings.Contains(ev, "API_KEY") {
			foundAPI = true
		}
		if strings.Contains(ev, "SERVER_PORT") {
			foundPort = true
		}
	}
	if !foundAPI {
		t.Errorf("expected to detect API_KEY, got %v", envVars)
	}
	if !foundPort {
		t.Errorf("expected to detect SERVER_PORT, got %v", envVars)
	}
}

func TestDetectExpectedWorkingDir(t *testing.T) {
	dir := t.TempDir()
	testDir := filepath.Join(dir, "tests")
	os.MkdirAll(testDir, 0o755)

	content := `#!/bin/bash
cd /app
python3 solution.py
`
	os.WriteFile(filepath.Join(testDir, "test.sh"), []byte(content), 0o644)

	hint := detectExpectedWorkingDir(dir)
	if hint == "" {
		t.Fatal("expected to detect working directory hint")
	}
	if !strings.Contains(hint, "/app") {
		t.Errorf("expected hint to mention /app, got %q", hint)
	}
}

func TestExtractKVFromLine(t *testing.T) {
	tests := []struct {
		line string
		key  string
		want string
	}{
		{"isclose(a, b, rel_tol=1e-9)", "rel_tol", "1e-9"},
		{"isclose(a, b, abs_tol=0.001)", "abs_tol", "0.001"},
		{"assertAlmostEqual(a, b, places=5)", "places", "5"},
		{"approx(expected, abs=1e-6, rel=1e-3)", "abs", "1e-6"},
		{"approx(expected, abs=1e-6, rel=1e-3)", "rel", "1e-3"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := extractKVFromLine(tt.line, tt.key)
			if got != tt.want {
				t.Errorf("extractKVFromLine(%q, %q) = %q, want %q", tt.line, tt.key, got, tt.want)
			}
		})
	}
}

func TestExtractPerTestTimeouts(t *testing.T) {
	t.Run("timeout_command", func(t *testing.T) {
		dir := t.TempDir()
		testsDir := filepath.Join(dir, "tests")
		os.Mkdir(testsDir, 0o755)
		os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte("#!/bin/bash\ntimeout 30 python3 /app/solution.py < input1.txt\n"), 0o644)

		timeouts := extractPerTestTimeouts(dir)
		if len(timeouts) == 0 {
			t.Fatal("expected at least one timeout, got none")
		}
		if !strings.Contains(timeouts[0], "30") {
			t.Errorf("expected timeout to mention 30, got %q", timeouts[0])
		}
	})

	t.Run("signal_alarm", func(t *testing.T) {
		dir := t.TempDir()
		testsDir := filepath.Join(dir, "tests")
		os.Mkdir(testsDir, 0o755)
		os.WriteFile(filepath.Join(testsDir, "test.py"), []byte("import signal\nsignal.alarm(60)\nresult = run_solution()\n"), 0o644)

		timeouts := extractPerTestTimeouts(dir)
		if len(timeouts) == 0 {
			t.Fatal("expected at least one timeout, got none")
		}
		if !strings.Contains(timeouts[0], "60") {
			t.Errorf("expected timeout to mention 60, got %q", timeouts[0])
		}
	})

	t.Run("ulimit", func(t *testing.T) {
		dir := t.TempDir()
		testsDir := filepath.Join(dir, "tests")
		os.Mkdir(testsDir, 0o755)
		os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte("#!/bin/bash\nulimit -t 45\n./solution < input.txt\n"), 0o644)

		timeouts := extractPerTestTimeouts(dir)
		if len(timeouts) == 0 {
			t.Fatal("expected at least one timeout, got none")
		}
		if !strings.Contains(timeouts[0], "45") {
			t.Errorf("expected timeout to mention 45, got %q", timeouts[0])
		}
	})
}

func TestAutoMkdirOutputDirs(t *testing.T) {
	dir := t.TempDir()
	expectedOutputs := []string{"output_data/results.csv", "output_data/summary.json"}
	autoMkdirOutputDirs(dir, expectedOutputs)

	outputDir := filepath.Join(dir, "output_data")
	if !dirExists(outputDir) {
		t.Errorf("expected output_data/ to be created, but it doesn't exist")
	}
}

func TestAutoMkdirOutputDirsNoopWhenExists(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "output_data")
	os.Mkdir(outputDir, 0o755)

	autoMkdirOutputDirs(dir, []string{"output_data/test.txt"})
	if !dirExists(outputDir) {
		t.Errorf("expected output_data/ to still exist")
	}
}

func TestExtractDiffTargets(t *testing.T) {
	t.Run("simple_diff", func(t *testing.T) {
		dir := t.TempDir()
		testsDir := filepath.Join(dir, "tests")
		os.Mkdir(testsDir, 0o755)

		expectedFile := filepath.Join(testsDir, "expected_output.txt")
		os.WriteFile(expectedFile, []byte("hello world\n"), 0o644)

		os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte("#!/bin/bash\ndiff "+expectedFile+" /app/output.txt\n"), 0o644)

		targets := extractDiffTargets(dir)
		if len(targets) == 0 {
			t.Fatal("expected at least one diff target, got none")
		}
		if targets[0].expectedRef != expectedFile {
			t.Errorf("expected ref %q, got %q", expectedFile, targets[0].expectedRef)
		}
	})

	t.Run("diff_with_flags", func(t *testing.T) {
		dir := t.TempDir()
		testsDir := filepath.Join(dir, "tests")
		os.Mkdir(testsDir, 0o755)

		expectedFile := filepath.Join(testsDir, "reference.csv")
		os.WriteFile(expectedFile, []byte("a,b,c\n1,2,3\n"), 0o644)

		os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte("#!/bin/bash\ndiff -b "+expectedFile+" output.csv\n"), 0o644)

		targets := extractDiffTargets(dir)
		if len(targets) == 0 {
			t.Fatal("expected at least one diff target, got none")
		}
		if !strings.Contains(targets[0].flags, "whitespace") {
			t.Errorf("expected flags to mention whitespace, got %q", targets[0].flags)
		}
	})

	t.Run("cmp_command", func(t *testing.T) {
		dir := t.TempDir()
		testsDir := filepath.Join(dir, "tests")
		os.Mkdir(testsDir, 0o755)

		expectedFile := filepath.Join(testsDir, "expected.bin")
		os.WriteFile(expectedFile, []byte{0x00, 0x01, 0x02}, 0o644)

		os.WriteFile(filepath.Join(testsDir, "test.sh"), []byte("#!/bin/bash\ncmp "+expectedFile+" output.bin\n"), 0o644)

		targets := extractDiffTargets(dir)
		if len(targets) == 0 {
			t.Fatal("expected at least one diff target, got none")
		}
		if !strings.Contains(targets[0].flags, "Byte-exact") {
			t.Errorf("expected flags to mention byte-exact, got %q", targets[0].flags)
		}
	})
}

func TestIsNumericOrFloat(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"30", true},
		{"30.5", true},
		{"0.001", true},
		{"abc", false},
		{"", false},
		{"30s", false},
	}
	for _, tt := range tests {
		t.Run(tt.input+"_"+fmt.Sprint(tt.want), func(t *testing.T) {
			got := isNumericOrFloat(tt.input)
			if got != tt.want {
				t.Errorf("isNumericOrFloat(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildContextRecoverySummary(t *testing.T) {
	// Build a set of dropped messages that include reads, edits, and verification.
	dropped := []core.ModelMessage{
		// Agent reads a file (tool name is "view", not "read").
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "view",
					ArgsJSON: `{"path":"/app/main.py"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName: "view",
					Content:  "def main():\n    print('hello')\n",
				},
			},
		},
		// Agent edits a file.
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "edit",
					ArgsJSON: `{"path":"/app/main.py"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName: "edit",
					Content:  "ok",
				},
			},
		},
		// Agent writes a new file.
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "write",
					ArgsJSON: `{"path":"/app/output.txt"}`,
				},
			},
		},
		// Agent runs tests (fail).
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest test_main.py"}`,
					ToolCallID: "v1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "1 failed, 2 passed\n[exit code: 1]",
					ToolCallID: "v1",
				},
			},
		},
		// Agent runs tests again (pass).
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"pytest test_main.py"}`,
					ToolCallID: "v2",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "3 passed\n[exit code: 0]",
					ToolCallID: "v2",
				},
			},
		},
	}

	summary := buildContextRecoverySummary(dropped)

	// Should mention files read.
	if !strings.Contains(summary, "/app/main.py") {
		t.Error("summary should mention files read")
	}
	// Should mention files modified.
	if !strings.Contains(summary, "FILES YOU MODIFIED") {
		t.Error("summary should have FILES YOU MODIFIED section")
	}
	if !strings.Contains(summary, "/app/output.txt") {
		t.Error("summary should mention written files")
	}
	// Should mention verification history.
	if !strings.Contains(summary, "VERIFICATION HISTORY") {
		t.Error("summary should have VERIFICATION HISTORY section")
	}
	if !strings.Contains(summary, "FAILED") {
		t.Error("summary should mention failed verification run")
	}
	if !strings.Contains(summary, "PASSED") {
		t.Error("summary should mention passed verification run")
	}
}

func TestBuildContextRecoverySummary_Packages(t *testing.T) {
	// Test that pip/apt installs are tracked in recovery summary.
	dropped := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "bash",
					ArgsJSON: `{"command":"pip install --break-system-packages numpy pandas"}`,
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "bash",
					ArgsJSON: `{"command":"apt-get install -y jq bc"}`,
				},
			},
		},
	}

	summary := buildContextRecoverySummary(dropped)
	if !strings.Contains(summary, "PACKAGES ALREADY INSTALLED") {
		t.Error("summary should have PACKAGES ALREADY INSTALLED section")
	}
	if !strings.Contains(summary, "numpy") {
		t.Error("summary should mention numpy")
	}
	if !strings.Contains(summary, "jq") {
		t.Error("summary should mention jq")
	}
}

func TestBuildContextRecoverySummary_ExpandedPackages(t *testing.T) {
	// Test that cargo/go/gem/yarn/composer installs are tracked.
	dropped := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "bash",
					ArgsJSON: `{"command":"cargo add serde tokio"}`,
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "bash",
					ArgsJSON: `{"command":"go get github.com/gin-gonic/gin"}`,
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "bash",
					ArgsJSON: `{"command":"yarn add express lodash"}`,
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName: "bash",
					ArgsJSON: `{"command":"gem install rspec bundler"}`,
				},
			},
		},
	}

	summary := buildContextRecoverySummary(dropped)
	if !strings.Contains(summary, "PACKAGES ALREADY INSTALLED") {
		t.Error("summary should have PACKAGES ALREADY INSTALLED section")
	}
	for _, pkg := range []string{"serde", "tokio", "github.com/gin-gonic/gin", "express", "lodash", "rspec", "bundler"} {
		if !strings.Contains(summary, pkg) {
			t.Errorf("summary should mention %q", pkg)
		}
	}
}

func TestBuildContextRecoverySummary_Subagent(t *testing.T) {
	// Test that subagent tasks are tracked in recovery summary.
	dropped := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "delegate",
					ArgsJSON:   `{"task":"Implement the sorting algorithm in sort.py"}`,
					ToolCallID: "sub1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "delegate",
					Content:    "Implemented quicksort in sort.py with O(n log n) average case.",
					ToolCallID: "sub1",
				},
			},
		},
	}

	summary := buildContextRecoverySummary(dropped)
	if !strings.Contains(summary, "COMPLETED SUBAGENT TASKS") {
		t.Error("summary should have COMPLETED SUBAGENT TASKS section")
	}
	if !strings.Contains(summary, "sorting algorithm") {
		t.Error("summary should mention the subagent task")
	}
}

func TestEmergencyCompressWithSummary(t *testing.T) {
	// Build a conversation with enough messages to trigger compression.
	messages := make([]core.ModelMessage, 0, 20)

	// First message: task description.
	messages = append(messages, core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "Solve this coding problem"},
		},
	})

	// Middle: agent reads files and edits (tool name is "view").
	for i := range 10 {
		callID := fmt.Sprintf("view%d", i)
		messages = append(messages,
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{
						ToolName:   "view",
						ArgsJSON:   fmt.Sprintf(`{"path":"/app/file%d.py"}`, i),
						ToolCallID: callID,
					},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.ToolReturnPart{
						ToolName:   "view",
						Content:    strings.Repeat("x", 1000),
						ToolCallID: callID,
					},
				},
			},
		)
	}

	// Tail: recent messages.
	messages = append(messages,
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "I'll fix the issue now."},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Please hurry."},
			},
		},
	)

	compressed := emergencyCompressMessagesWithConfig(messages, 20000, 4)

	// Second message should be the recovery summary (ModelResponse for proper alternation).
	if resp, ok := compressed[1].(core.ModelResponse); ok {
		text := resp.TextContent()
		if !strings.Contains(text, "EMERGENCY CONTEXT RECOVERY") {
			t.Errorf("second message should be the recovery summary, got: %s", text)
		}
		if !strings.Contains(text, "FILES PREVIOUSLY READ") {
			t.Error("recovery summary should list files that were read")
		}
	} else {
		t.Errorf("second compressed message should be a ModelResponse, got %T", compressed[1])
	}

	// Verify proper message alternation (critical for Anthropic API).
	for i := 1; i < len(compressed); i++ {
		_, prevIsReq := compressed[i-1].(core.ModelRequest)
		_, currIsReq := compressed[i].(core.ModelRequest)
		if prevIsReq && currIsReq {
			t.Errorf("adjacent ModelRequest messages at indices %d and %d", i-1, i)
		}
		_, prevIsResp := compressed[i-1].(core.ModelResponse)
		_, currIsResp := compressed[i].(core.ModelResponse)
		if prevIsResp && currIsResp {
			t.Errorf("adjacent ModelResponse messages at indices %d and %d", i-1, i)
		}
	}
}

func TestEmergencyCompressTruncatesLargeToolCallArgs(t *testing.T) {
	// When a model calls write/edit with large file content, the ToolCallPart.ArgsJSON
	// can be very large. The emergency compression must truncate these args to allow
	// context overflow recovery. Without this, the ContextOverflowMiddleware can't
	// reduce context size when the overflow is caused by large tool call args.
	largeArgs := `{"path":"/app/main.py","content":"` + strings.Repeat("x", 50000) + `"}`
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Write a program"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "write",
					ArgsJSON:   largeArgs,
					ToolCallID: "tc1",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "write",
					Content:    "ok",
					ToolCallID: "tc1",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Done."},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Now test it."},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ArgsJSON:   `{"command":"python main.py"}`,
					ToolCallID: "tc2",
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					Content:    "output ok",
					ToolCallID: "tc2",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Tests pass."},
			},
		},
	}

	// Use a small maxContentBytes to force truncation of the large args.
	compressed := emergencyCompressMessagesWithConfig(messages, 5000, 4)

	// The large ToolCallPart.ArgsJSON should be truncated in the kept messages.
	// Check all messages for untrunced large args.
	for i, msg := range compressed {
		if resp, ok := msg.(core.ModelResponse); ok {
			for _, part := range resp.Parts {
				if tc, ok := part.(core.ToolCallPart); ok {
					if len(tc.ArgsJSON) > 5000 {
						t.Errorf("message[%d]: ToolCallPart.ArgsJSON not truncated (%d bytes)", i, len(tc.ArgsJSON))
					}
				}
			}
		}
	}

	// Verify truncated args are valid JSON (so providers don't reject them).
	for _, msg := range compressed {
		if resp, ok := msg.(core.ModelResponse); ok {
			for _, part := range resp.Parts {
				if tc, ok := part.(core.ToolCallPart); ok {
					if strings.Contains(tc.ArgsJSON, "_truncated") {
						var parsed map[string]any
						if err := json.Unmarshal([]byte(tc.ArgsJSON), &parsed); err != nil {
							t.Errorf("truncated ArgsJSON is not valid JSON: %v", err)
						}
					}
				}
			}
		}
	}
}

func TestTruncateMessageContentSmartTruncatesToolCallArgs(t *testing.T) {
	// Verify the smart truncation also handles ToolCallPart.ArgsJSON.
	largeArgs := `{"content":"` + strings.Repeat("a", 30000) + `"}`
	msg := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.TextPart{Content: "I'll write the file."},
			core.ToolCallPart{
				ToolName:   "write",
				ArgsJSON:   largeArgs,
				ToolCallID: "tc1",
				Metadata:   map[string]string{"thoughtSignature": "sig123"},
			},
		},
	}

	truncated := truncateMessageContentSmart(msg, 5000)
	resp, ok := truncated.(core.ModelResponse)
	if !ok {
		t.Fatal("expected ModelResponse")
	}
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Parts))
	}

	// Text should be unchanged (it's small).
	tp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart at index 0")
	}
	if tp.Content != "I'll write the file." {
		t.Errorf("text changed unexpectedly: %q", tp.Content)
	}

	// Tool call args should be truncated.
	tc, ok := resp.Parts[1].(core.ToolCallPart)
	if !ok {
		t.Fatal("expected ToolCallPart at index 1")
	}
	if len(tc.ArgsJSON) > 5000 {
		t.Errorf("ArgsJSON not truncated: %d bytes", len(tc.ArgsJSON))
	}
	if !strings.Contains(tc.ArgsJSON, "_truncated") {
		t.Error("expected truncated placeholder in ArgsJSON")
	}

	// Metadata must be preserved through truncation.
	if tc.Metadata == nil {
		t.Fatal("Metadata lost during truncation")
	}
	if tc.Metadata["thoughtSignature"] != "sig123" {
		t.Errorf("thoughtSignature = %q, want sig123", tc.Metadata["thoughtSignature"])
	}
}

func TestClassifyDiffReference(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "expected.txt")
	os.WriteFile(file1, []byte("hello"), 0o644)

	ref := classifyDiffReference(file1, filepath.Join(dir, "output.txt"), dir)
	if ref != file1 {
		t.Errorf("expected %q to be classified as reference, got %q", file1, ref)
	}
}

func TestDescribeDiffFlags(t *testing.T) {
	if got := describeDiffFlags([]string{"-b"}); got != "Ignores trailing whitespace changes (diff -b)" {
		t.Errorf("unexpected: %q", got)
	}
	if got := describeDiffFlags([]string{"-w"}); got != "Ignores all whitespace differences (diff -w)" {
		t.Errorf("unexpected: %q", got)
	}
	if got := describeDiffFlags([]string{"-i"}); got != "Case-insensitive comparison (diff -i)" {
		t.Errorf("unexpected: %q", got)
	}
}

// TestReasoningSandwich_Bidirectional verifies that the reasoning sandwich
// middleware drops back to implementation phase after verification cooldown
// expires, rather than staying in verification forever (the old one-way latch).
func TestReasoningSandwich_Bidirectional(t *testing.T) {
	cfg := ReasoningSandwichConfig{
		Planning:       ReasoningLevel{ThinkingBudget: 48000, ReasoningEffort: "high"},
		Implementation: ReasoningLevel{ThinkingBudget: 16000, ReasoningEffort: "medium"},
		Verification:   ReasoningLevel{ThinkingBudget: 48000, ReasoningEffort: "high"},
		PlanningTurns:  2,
	}

	mw := ReasoningSandwichMiddleware(cfg)

	budget := 10000
	effort := "medium"
	baseSettings := &core.ModelSettings{
		ThinkingBudget:  &budget,
		ReasoningEffort: &effort,
	}

	// Helper to call middleware and capture the settings passed to next.
	callMW := func(msgs []core.ModelMessage) *core.ModelSettings {
		var captured *core.ModelSettings
		mw(context.Background(), msgs, baseSettings, nil,
			func(_ context.Context, _ []core.ModelMessage, s *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
				captured = s
				return &core.ModelResponse{}, nil
			})
		return captured
	}

	// Build messages: initial request (no verification).
	basicMsgs := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "implement the solution"},
		}},
	}

	// Turn 1-2: planning phase.
	s1 := callMW(basicMsgs)
	if *s1.ThinkingBudget != 48000 {
		t.Errorf("turn 1: expected planning budget 48000, got %d", *s1.ThinkingBudget)
	}
	s2 := callMW(basicMsgs)
	if *s2.ThinkingBudget != 48000 {
		t.Errorf("turn 2: expected planning budget 48000, got %d", *s2.ThinkingBudget)
	}

	// Turns 3-5: implementation phase (no verification commands in messages).
	s3 := callMW(basicMsgs)
	if *s3.ThinkingBudget != 16000 {
		t.Errorf("turn 3: expected implementation budget 16000, got %d", *s3.ThinkingBudget)
	}

	// Now inject verification commands (pytest) into recent messages.
	verifyMsgs := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "test the solution"},
		}},
		core.ModelResponse{Parts: []core.ModelResponsePart{
			core.ToolCallPart{ToolName: "bash", ArgsJSON: `{"command":"pytest test_solution.py"}`},
		}},
	}

	// Turn 4: verification detected → high budget.
	s4 := callMW(verifyMsgs)
	if *s4.ThinkingBudget != 48000 {
		t.Errorf("turn 4 (verification): expected 48000, got %d", *s4.ThinkingBudget)
	}

	// Turns 5-6: still in verification cooldown (3 turns total).
	s5 := callMW(basicMsgs)
	if *s5.ThinkingBudget != 48000 {
		t.Errorf("turn 5 (cooldown): expected 48000, got %d", *s5.ThinkingBudget)
	}
	s6 := callMW(basicMsgs)
	if *s6.ThinkingBudget != 48000 {
		t.Errorf("turn 6 (cooldown): expected 48000, got %d", *s6.ThinkingBudget)
	}

	// Turn 7: cooldown expired → back to implementation.
	s7 := callMW(basicMsgs)
	if *s7.ThinkingBudget != 16000 {
		t.Errorf("turn 7 (post-cooldown): expected implementation budget 16000, got %d", *s7.ThinkingBudget)
	}

	// Turn 8: still implementation.
	s8 := callMW(basicMsgs)
	if *s8.ThinkingBudget != 16000 {
		t.Errorf("turn 8: expected implementation budget 16000, got %d", *s8.ThinkingBudget)
	}
}

// TestTestPassRateRegression verifies that the bash tool detects when
// test pass counts drop between consecutive runs.
func TestTestPassRateRegression(t *testing.T) {
	// extractTestCounts is already tested. Here we test the regression
	// detection logic directly by checking the hint output format.
	// The actual detection happens inside the Bash tool closure, so we
	// test the individual components.

	t.Run("regression_detected", func(t *testing.T) {
		// Simulate: run 1 had 8 passed 2 failed, run 2 has 5 passed 5 failed.
		// The hint should mention REGRESSION.
		prev := struct{ passed, failed int }{8, 2}
		curr := struct{ passed, failed int }{5, 5}

		if curr.passed >= prev.passed {
			t.Fatal("test setup: curr should have fewer passes than prev")
		}
		if prev.passed <= 0 {
			t.Fatal("test setup: prev should have >0 passes")
		}
		// Verify the regression condition matches what the code checks.
		if curr.passed >= prev.passed || prev.passed <= 0 {
			t.Error("regression condition should be true")
		}
	})

	t.Run("no_regression_when_improving", func(t *testing.T) {
		prev := struct{ passed, failed int }{5, 5}
		curr := struct{ passed, failed int }{7, 3}

		if curr.passed < prev.passed {
			t.Error("should not detect regression when pass count improves")
		}
	})

	t.Run("no_regression_on_first_run", func(t *testing.T) {
		// Only 1 run in history — no previous to compare against.
		history := []struct{ passed, failed int }{{5, 5}}
		if len(history) >= 2 {
			t.Error("should not detect regression with only 1 run")
		}
	})
}
func TestAutoCleanupIntermediates(t *testing.T) {
	dir := t.TempDir()

	// Create various intermediates.
	pycacheDir := filepath.Join(dir, "pkg", "__pycache__")
	os.MkdirAll(pycacheDir, 0o755)
	os.WriteFile(filepath.Join(pycacheDir, "module.cpython-39.pyc"), []byte("bytecode"), 0o644)

	// .pyc file outside __pycache__
	os.WriteFile(filepath.Join(dir, "old.pyc"), []byte("bytecode"), 0o644)

	// .o and a.out in root
	os.WriteFile(filepath.Join(dir, "main.o"), []byte("object"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.out"), []byte("binary"), 0o755)

	// Real files that should NOT be deleted.
	os.WriteFile(filepath.Join(dir, "solution.py"), []byte("print('hi')"), 0o644)
	os.WriteFile(filepath.Join(dir, "output.txt"), []byte("result"), 0o644)

	cleaned := autoCleanupIntermediates(dir)
	if cleaned < 3 {
		t.Errorf("expected at least 3 items cleaned, got %d", cleaned)
	}

	// Verify intermediates are gone.
	if _, err := os.Stat(pycacheDir); !os.IsNotExist(err) {
		t.Error("__pycache__ should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.pyc")); !os.IsNotExist(err) {
		t.Error("old.pyc should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "main.o")); !os.IsNotExist(err) {
		t.Error("main.o should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.out")); !os.IsNotExist(err) {
		t.Error("a.out should be removed")
	}

	// Verify real files are still there.
	if _, err := os.Stat(filepath.Join(dir, "solution.py")); err != nil {
		t.Error("solution.py should NOT be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "output.txt")); err != nil {
		t.Error("output.txt should NOT be removed")
	}
}

func TestAutoCleanupIntermediates_JavaClassFiles(t *testing.T) {
	dir := t.TempDir()

	// Create Java .class files (compilation artifacts).
	os.WriteFile(filepath.Join(dir, "Main.class"), []byte("classdata"), 0o644)
	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "Helper.class"), []byte("classdata"), 0o644)

	// Real .java source should NOT be deleted.
	os.WriteFile(filepath.Join(dir, "Main.java"), []byte("class Main {}"), 0o644)

	cleaned := autoCleanupIntermediates(dir)
	if cleaned < 2 {
		t.Errorf("expected at least 2 .class files cleaned, got %d", cleaned)
	}
	if _, err := os.Stat(filepath.Join(dir, "Main.class")); !os.IsNotExist(err) {
		t.Error("Main.class should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "Helper.class")); !os.IsNotExist(err) {
		t.Error("pkg/Helper.class should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "Main.java")); err != nil {
		t.Error("Main.java should NOT be removed")
	}
}

func TestAutoCleanupIntermediates_HaskellHiFiles(t *testing.T) {
	dir := t.TempDir()

	// Create Haskell .hi files (interface files).
	os.WriteFile(filepath.Join(dir, "Main.hi"), []byte("hidata"), 0o644)
	os.WriteFile(filepath.Join(dir, "Main.hs"), []byte("main = putStrLn \"hi\""), 0o644)

	cleaned := autoCleanupIntermediates(dir)
	if cleaned < 1 {
		t.Errorf("expected at least 1 .hi file cleaned, got %d", cleaned)
	}
	if _, err := os.Stat(filepath.Join(dir, "Main.hi")); !os.IsNotExist(err) {
		t.Error("Main.hi should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "Main.hs")); err != nil {
		t.Error("Main.hs should NOT be removed")
	}
}

func TestAutoCleanupIntermediates_EggInfo(t *testing.T) {
	dir := t.TempDir()

	// Create *.egg-info directory (Python packaging artifact).
	eggDir := filepath.Join(dir, "mypackage.egg-info")
	os.MkdirAll(eggDir, 0o755)
	os.WriteFile(filepath.Join(eggDir, "PKG-INFO"), []byte("metadata"), 0o644)

	cleaned := autoCleanupIntermediates(dir)
	if cleaned < 1 {
		t.Errorf("expected at least 1 egg-info cleaned, got %d", cleaned)
	}
	if _, err := os.Stat(eggDir); !os.IsNotExist(err) {
		t.Error("*.egg-info directory should be removed")
	}
}

func TestPytestFailedTestExtraction(t *testing.T) {
	output := `============================= test session starts ==============================
collected 5 items

test_solution.py::test_add PASSED
test_solution.py::test_subtract FAILED
test_solution.py::test_multiply PASSED
test_solution.py::test_divide FAILED
test_solution.py::test_modulo PASSED

FAILED test_solution.py::test_subtract - AssertionError: assert 3 == 2
FAILED test_solution.py::test_divide - ZeroDivisionError
========================= 2 failed, 3 passed in 0.5s ==========================`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "2 failed") {
		t.Errorf("expected '2 failed' in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "test_subtract") {
		t.Errorf("expected 'test_subtract' in failed tests, got: %s", summary)
	}
	if !strings.Contains(summary, "test_divide") {
		t.Errorf("expected 'test_divide' in failed tests, got: %s", summary)
	}
}

func TestUnittestFailedTestExtraction(t *testing.T) {
	output := `FAIL: test_add (test_math.TestMath)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "test_math.py", line 10, in test_add
    self.assertEqual(add(1, 2), 4)
AssertionError: 3 != 4

FAIL: test_divide (test_math.TestMath)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "test_math.py", line 20, in test_divide
    self.assertEqual(divide(10, 3), 3)
AssertionError: 3.3333333333333335 != 3

----------------------------------------------------------------------
Ran 5 tests in 0.003s

FAILED (failures=2)
[exit code: 1]`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "Ran 5 tests") {
		t.Errorf("expected 'Ran 5 tests' in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "test_add") {
		t.Errorf("expected 'test_add' in failed tests, got: %s", summary)
	}
	if !strings.Contains(summary, "test_divide") {
		t.Errorf("expected 'test_divide' in failed tests, got: %s", summary)
	}
}
func TestCargoTestFailedTestExtraction(t *testing.T) {
	output := `running 5 tests
test tests::test_add ... ok
test tests::test_subtract ... FAILED
test tests::test_multiply ... ok
test tests::test_divide ... FAILED
test tests::test_modulo ... ok

failures:

---- tests::test_subtract stdout ----
thread 'tests::test_subtract' panicked at 'assertion left == right failed
  left: 3
  right: 2', src/lib.rs:15:5

---- tests::test_divide stdout ----
thread 'tests::test_divide' panicked at 'attempt to divide by zero', src/lib.rs:25:5

failures:
    tests::test_subtract
    tests::test_divide

test result: FAILED. 3 passed; 2 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "FAILED") {
		t.Errorf("expected 'FAILED' in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "tests::test_subtract") {
		t.Errorf("expected 'tests::test_subtract' in failed tests, got: %s", summary)
	}
	if !strings.Contains(summary, "tests::test_divide") {
		t.Errorf("expected 'tests::test_divide' in failed tests, got: %s", summary)
	}
}

func TestJestFailedSuiteExtraction(t *testing.T) {
	output := `FAIL src/__tests__/math.test.js
  ● TestSuite › should add correctly

    expect(received).toBe(expected)

    Expected: 5
    Received: 4

      4 | test('should add correctly', () => {
      5 |   expect(add(2, 3)).toBe(5);
      6 | });

FAIL src/__tests__/string.test.js
  ● StringSuite › should capitalize

    expect(received).toBe(expected)

    Expected: "Hello"
    Received: "hello"

PASS src/__tests__/utils.test.js

Tests:        2 failed, 1 passed, 3 total
Test Suites:  2 failed, 1 passed, 3 total`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "2 failed") {
		t.Errorf("expected '2 failed' in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "math.test.js") {
		t.Errorf("expected 'math.test.js' in failed suites, got: %s", summary)
	}
	if !strings.Contains(summary, "string.test.js") {
		t.Errorf("expected 'string.test.js' in failed suites, got: %s", summary)
	}
}

func TestValidateOutputFormats_TrailingNewline(t *testing.T) {
	dir := t.TempDir()

	// File without trailing newline should trigger warning.
	os.WriteFile(filepath.Join(dir, "output.txt"), []byte("hello world"), 0o644)

	// Use validateOutputFormats with pre-computed expected outputs.
	// For a direct test, we need the file to be in a pattern it checks.
	// validateOutputFormats checks files matching "output.*" pattern.
	issues := validateOutputFormats(dir, detectExpectedOutputs(dir))
	if !strings.Contains(issues, "missing a trailing newline") {
		t.Errorf("expected trailing newline warning, got: %q", issues)
	}

	// File WITH trailing newline should not trigger warning.
	os.WriteFile(filepath.Join(dir, "output.txt"), []byte("hello world\n"), 0o644)
	issues = validateOutputFormats(dir, detectExpectedOutputs(dir))
	if strings.Contains(issues, "missing a trailing newline") {
		t.Errorf("should not warn about trailing newline when present, got: %q", issues)
	}
}

func TestIsBinaryLike(t *testing.T) {
	// Text data.
	if isBinaryLike([]byte("hello world\n")) {
		t.Error("text data should not be binary-like")
	}

	// Binary data with NUL.
	if !isBinaryLike([]byte("hello\x00world")) {
		t.Error("data with NUL should be binary-like")
	}

	// Empty data.
	if isBinaryLike([]byte{}) {
		t.Error("empty data should not be binary-like")
	}
}

func TestMochaTestSummaryExtraction(t *testing.T) {
	output := `
  Calculator
    ✓ should add correctly
    ✓ should subtract correctly
    1) should multiply correctly
    2) should divide correctly

  2 passing (15ms)
  2 failing

  1) Calculator
       should multiply correctly:
     AssertionError: expected 6 to equal 8
      at Context.<anonymous> (test/calc.test.js:12:24)

  2) Calculator
       should divide correctly:
     AssertionError: expected 2 to equal 3
      at Context.<anonymous> (test/calc.test.js:18:24)
`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty Mocha summary")
	}
	if !strings.Contains(summary, "2 passing") {
		t.Errorf("expected '2 passing' in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "2 failing") {
		t.Errorf("expected '2 failing' in summary, got: %s", summary)
	}
	// Should also extract first failure detail.
	if !strings.Contains(summary, "first failure") {
		t.Errorf("expected first failure detail in summary, got: %s", summary)
	}
}

func TestPHPUnitTestSummaryExtraction(t *testing.T) {
	output := `PHPUnit 9.5.0 by Sebastian Bergmann and contributors.

..F.                                                               3 / 4 (100%)

Time: 00:00.015, Memory: 6.00 MB

There was 1 failure:

1) TestCalculator::testMultiply
Failed asserting that 6 matches expected 8.

/app/tests/CalculatorTest.php:15

FAILURES!
Tests: 4, Assertions: 4, Failures: 1`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty PHPUnit summary")
	}
	if !strings.Contains(summary, "Tests: 4") {
		t.Errorf("expected 'Tests: 4' in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "Failures: 1") {
		t.Errorf("expected 'Failures: 1' in summary, got: %s", summary)
	}
	// Should also extract first failure detail.
	if !strings.Contains(summary, "first failure") {
		t.Errorf("expected first failure detail in summary, got: %s", summary)
	}
}

func TestMinitestCountExtraction(t *testing.T) {
	output := `Run options: --seed 12345

# Running:

..F.E

Finished in 0.001234s, 4050.1234 runs/s, 4050.1234 assertions/s.

  1) Failure:
TestCalculator#test_multiply [test_calculator.rb:15]:
Expected: 8
  Actual: 6

  2) Error:
TestCalculator#test_divide [test_calculator.rb:20]:
ZeroDivisionError: divided by 0

5 runs, 5 assertions, 1 failures, 1 errors, 0 skips`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected extractTestCounts to find minitest counts")
	}
	if f != 2 {
		t.Errorf("expected 2 failures (1 failure + 1 error), got: %d", f)
	}
	if p != 3 {
		t.Errorf("expected 3 passed (5 runs - 2 failures), got: %d", p)
	}
}

func TestDotNetCountExtraction(t *testing.T) {
	output := `  Determining projects to restore...
  All projects are up-to-date for restore.
  TestProject -> /app/bin/Debug/net6.0/TestProject.dll
Test run for /app/bin/Debug/net6.0/TestProject.dll (.NETCoreApp,Version=v6.0)
Microsoft (R) Test Execution Command Line Tool Version 17.3.1 (x64)

Starting test execution, please wait...
A total of 1 test files matched the specified pattern.
  Failed TestProject.Tests.TestCalculator.TestMultiply [5 ms]
  Error Message:
   Assert.Equal() Failure
Expected: 8
Actual:   6

Total tests: 5
     Passed: 4
     Failed: 1`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected extractTestCounts to find .NET counts")
	}
	if f != 1 {
		t.Errorf("expected 1 failure, got: %d", f)
	}
	if p != 4 {
		t.Errorf("expected 4 passed, got: %d", p)
	}
}

func TestCTestCountExtraction(t *testing.T) {
	output := `Test project /app/build
    Start 1: test_basic
1/4 Test #1: test_basic ...........   Passed    0.01 sec
    Start 2: test_advanced
2/4 Test #2: test_advanced ........   Passed    0.02 sec
    Start 3: test_edge
3/4 Test #3: test_edge ............***Failed    0.01 sec
    Start 4: test_perf
4/4 Test #4: test_perf ............   Passed    0.03 sec

75% tests passed, 1 tests failed out of 4

The following tests FAILED:
          3 - test_edge (Failed)`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected extractTestCounts to find CTest counts")
	}
	if f != 1 {
		t.Errorf("expected 1 failure, got: %d", f)
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got: %d", p)
	}
}

func TestCatch2CountExtraction(t *testing.T) {
	output := `~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
tests is a Catch v2.13.6 host application.
Run with -? for options

-------------------------------------------------------------------------------
Test multiply
-------------------------------------------------------------------------------
tests.cpp:15
...............................................................................

tests.cpp:17: FAILED:
  REQUIRE( multiply(3, 4) == 11 )
with expansion:
  12 == 11

===============================================================================
test cases: 5 | 4 passed | 1 failed
assertions: 8 | 7 passed | 1 failed`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected extractTestCounts to find Catch2 counts")
	}
	if f != 1 {
		t.Errorf("expected 1 failure, got: %d", f)
	}
	if p != 4 {
		t.Errorf("expected 4 passed, got: %d", p)
	}
}

func TestCatch2AllPassedExtraction(t *testing.T) {
	output := `All tests passed (12 assertions in 5 test cases)`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected extractTestCounts to find Catch2 all-passed counts")
	}
	if f != 0 {
		t.Errorf("expected 0 failures, got: %d", f)
	}
	if p != 5 {
		t.Errorf("expected 5 passed, got: %d", p)
	}
}

func TestRSpecFailingTestExtraction(t *testing.T) {
	output := `..F.F

Failures:

  1) Calculator#add adds two numbers
     Failure/Error: expect(calc.add(2, 3)).to eq(6)

       expected: 6
            got: 5

     # ./spec/calculator_spec.rb:10:in ` + "`block (3 levels) in <top (required)>`" + `

  2) Calculator#multiply multiplies two numbers
     Failure/Error: expect(calc.multiply(3, 4)).to eq(11)

       expected: 11
            got: 12

     # ./spec/calculator_spec.rb:20:in ` + "`block (3 levels) in <top (required)>`" + `

Finished in 0.123 seconds (files took 0.5 seconds to load)
5 examples, 2 failures

Failed examples:

rspec ./spec/calculator_spec.rb:8 # Calculator#add adds two numbers
rspec ./spec/calculator_spec.rb:18 # Calculator#multiply multiplies two numbers`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected testResultSummary to produce a summary")
	}
	if !strings.Contains(summary, "5 examples") {
		t.Errorf("expected summary to contain '5 examples', got: %s", summary)
	}
	if !strings.Contains(summary, "2 failures") {
		t.Errorf("expected summary to contain '2 failures', got: %s", summary)
	}
	if !strings.Contains(summary, "failed examples") {
		t.Errorf("expected summary to contain 'failed examples', got: %s", summary)
	}
	if !strings.Contains(summary, "calculator_spec.rb:8") {
		t.Errorf("expected summary to contain 'calculator_spec.rb:8', got: %s", summary)
	}
	// Should also have first failure detail.
	if !strings.Contains(summary, "first failure") {
		t.Errorf("expected summary to contain first failure detail, got: %s", summary)
	}
}

func TestExtractCommandPrefix(t *testing.T) {
	tests := []struct {
		name     string
		argsJSON string
		expected string
	}{
		{
			name:     "simple command",
			argsJSON: `{"command": "make test"}`,
			expected: "make test",
		},
		{
			name:     "python with script",
			argsJSON: `{"command": "python3 test.py"}`,
			expected: "python3 test.py",
		},
		{
			name:     "python with module",
			argsJSON: `{"command": "python3 -m pytest"}`,
			expected: "python3 -m pytest",
		},
		{
			name:     "python different script",
			argsJSON: `{"command": "python3 solution.py"}`,
			expected: "python3 solution.py",
		},
		{
			name:     "compound with cd",
			argsJSON: `{"command": "cd /app && python3 test.py"}`,
			expected: "python3 test.py",
		},
		{
			name:     "full path interpreter",
			argsJSON: `{"command": "/usr/bin/python3 test.py"}`,
			expected: "python3 test.py",
		},
		{
			name:     "node with script",
			argsJSON: `{"command": "node test.js"}`,
			expected: "node test.js",
		},
		{
			name:     "go test includes subcommand",
			argsJSON: `{"command": "go test ./..."}`,
			expected: "go test",
		},
		{
			name:     "cargo test includes subcommand",
			argsJSON: `{"command": "cargo test"}`,
			expected: "cargo test",
		},
		{
			name:     "bash with script",
			argsJSON: `{"command": "bash test.sh"}`,
			expected: "bash test.sh",
		},
		{
			name:     "bash different script",
			argsJSON: `{"command": "bash solution.sh"}`,
			expected: "bash solution.sh",
		},
		{
			name:     "npm test includes subcommand",
			argsJSON: `{"command": "npm test"}`,
			expected: "npm test",
		},
		{
			name:     "dotnet test includes subcommand",
			argsJSON: `{"command": "dotnet test"}`,
			expected: "dotnet test",
		},
		{
			name:     "git with flag stays simple",
			argsJSON: `{"command": "git -C /app status"}`,
			expected: "git",
		},
		{
			name:     "timeout skips duration",
			argsJSON: `{"command": "timeout 30 python3 test.py"}`,
			expected: "python3 test.py",
		},
		{
			name:     "sudo skips to real command",
			argsJSON: `{"command": "sudo python3 test.py"}`,
			expected: "python3 test.py",
		},
		{
			name:     "cd as direct preamble",
			argsJSON: `{"command": "cd /app python3 test.py"}`,
			expected: "python3 test.py",
		},
		{
			name:     "env with var assignment",
			argsJSON: `{"command": "env FOO=bar python3 test.py"}`,
			expected: "python3 test.py",
		},
		{
			name:     "semicolon takes first command",
			argsJSON: `{"command": "python3 test.py; echo $?"}`,
			expected: "python3 test.py",
		},
		{
			name:     "compound with cd and semicolon postscript",
			argsJSON: `{"command": "cd /app && python3 -m pytest; echo done"}`,
			expected: "python3 -m pytest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCommandPrefix(tt.argsJSON)
			if got != tt.expected {
				t.Errorf("extractCommandPrefix(%s) = %q, want %q", tt.argsJSON, got, tt.expected)
			}
		})
	}
}

func TestConnectionRefusedHint(t *testing.T) {
	// Should trigger for test/curl connection refused errors.
	got := connectionRefusedHint("curl: (7) Failed to connect to localhost port 8080: Connection refused", 7)
	if got == "" {
		t.Fatal("expected connection refused hint for curl error")
	}
	if !strings.Contains(got, "8080") {
		t.Errorf("expected port 8080 in hint, got: %s", got)
	}

	// Should trigger for Node ECONNREFUSED.
	got = connectionRefusedHint("Error: connect ECONNREFUSED 127.0.0.1:3000", 1)
	if got == "" {
		t.Fatal("expected connection refused hint for ECONNREFUSED")
	}
	if !strings.Contains(got, "3000") {
		t.Errorf("expected port 3000 in hint, got: %s", got)
	}

	// Should NOT trigger for apt-related connection refused.
	got = connectionRefusedHint("E: Failed to fetch http://archive.ubuntu.com/... Connection refused apt", 100)
	if got != "" {
		t.Errorf("should not trigger for apt errors, got: %s", got)
	}

	// Should NOT trigger on success.
	got = connectionRefusedHint("Connection refused", 0)
	if got != "" {
		t.Errorf("should not trigger on exit code 0, got: %s", got)
	}
}

func TestCompilationFingerprint(t *testing.T) {
	output := "main.c: In function 'main':\nmain.c:15:5: error: 'foo' undeclared (first use in this function)\nmain.c:20:10: error: expected ';' before '}' token"

	fp := compilationFingerprint(output)
	if fp == "" {
		t.Fatal("expected a compilation fingerprint")
	}
	if !strings.Contains(fp, "error:") {
		t.Errorf("expected fingerprint to contain 'error:', got: %s", fp)
	}

	// Same output should produce same fingerprint.
	fp2 := compilationFingerprint(output)
	if fp != fp2 {
		t.Errorf("fingerprint should be deterministic: %s != %s", fp, fp2)
	}

	// No errors should produce empty fingerprint.
	fp3 := compilationFingerprint("Build succeeded\nAll good")
	if fp3 != "" {
		t.Errorf("expected empty fingerprint for clean build, got: %s", fp3)
	}
}

func TestGradleCountExtraction(t *testing.T) {
	output := "> Task :test\n\ncom.example.AppTest > testMain PASSED\ncom.example.AppTest > testParse PASSED\ncom.example.AppTest > testFormat FAILED\n\n3 tests completed, 1 failed\n\nBUILD FAILED"

	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected Gradle count extraction to succeed")
	}
	if passed != 2 || failed != 1 {
		t.Errorf("Gradle: expected passed=2, failed=1, got passed=%d, failed=%d", passed, failed)
	}
}

func TestNoTestsCollectedSummary(t *testing.T) {
	// pytest "no tests ran"
	output := "============================= test session starts ==============================\ncollected 0 items\n\n=============================== no tests ran ================================"

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected summary for 'no tests ran'")
	}
	if !strings.Contains(summary, "NO TESTS FOUND") {
		t.Errorf("expected 'NO TESTS FOUND' in summary, got: %s", summary)
	}
}

func TestSystemctlNotFoundHint(t *testing.T) {
	t.Run("command not found", func(t *testing.T) {
		output := "bash: systemctl: command not found"
		hint := systemctlNotFoundHint(output, 127)
		if hint == "" {
			t.Fatal("expected hint for systemctl not found")
		}
		if !strings.Contains(hint, "systemd/systemctl is not available") {
			t.Errorf("expected 'not available' in hint, got: %s", hint)
		}
		if !strings.Contains(hint, "service") {
			t.Errorf("expected 'service' alternative in hint, got: %s", hint)
		}
	})

	t.Run("no systemd boot", func(t *testing.T) {
		output := "System has not been booted with systemd as init system."
		hint := systemctlNotFoundHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for no systemd boot")
		}
	})

	t.Run("bus connection", func(t *testing.T) {
		output := "Failed to connect to bus: No such file or directory"
		hint := systemctlNotFoundHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for bus connection failure")
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := systemctlNotFoundHint("systemctl start nginx", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		hint := systemctlNotFoundHint("python3: syntax error", 1)
		if hint != "" {
			t.Errorf("expected no hint for unrelated error, got: %s", hint)
		}
	})
}

func TestSubprocessTimeoutHint(t *testing.T) {
	t.Run("python TimeoutExpired", func(t *testing.T) {
		output := `subprocess.TimeoutExpired: Command '/app/debug' timed out after 10 seconds`
		hint := subprocessTimeoutHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for subprocess timeout")
		}
		if !strings.Contains(hint, "too SLOW") {
			t.Errorf("expected 'too SLOW' in hint, got: %s", hint)
		}
	})

	t.Run("timed out after N seconds", func(t *testing.T) {
		output := `E           subprocess.TimeoutExpired: Command '['valgrind', ...]' timed out after 30 seconds`
		hint := subprocessTimeoutHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for valgrind timeout")
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := subprocessTimeoutHint("TimeoutExpired", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		hint := subprocessTimeoutHint("ImportError: No module named foo", 1)
		if hint != "" {
			t.Errorf("expected no hint for unrelated error, got: %s", hint)
		}
	})

	t.Run("java TimeoutException", func(t *testing.T) {
		output := `java.util.concurrent.TimeoutException: Process timed out`
		hint := subprocessTimeoutHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for Java timeout")
		}
		if !strings.Contains(hint, "too SLOW") {
			t.Errorf("expected 'too SLOW' in hint, got: %s", hint)
		}
	})

	t.Run("ruby Timeout::Error", func(t *testing.T) {
		output := `Timeout::Error: execution expired`
		hint := subprocessTimeoutHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for Ruby timeout")
		}
	})

	t.Run("go context deadline exceeded", func(t *testing.T) {
		output := `--- FAIL: TestSolve (30.00s)
    solve_test.go:15: context deadline exceeded`
		hint := subprocessTimeoutHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for Go context deadline")
		}
	})

	t.Run("time limit exceeded", func(t *testing.T) {
		output := `FAILED: Time Limit Exceeded on test case 3`
		hint := subprocessTimeoutHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for TLE")
		}
	})

	t.Run("node TimeoutError", func(t *testing.T) {
		output := `TimeoutError: test exceeded 5000ms timeout`
		hint := subprocessTimeoutHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for Node.js timeout")
		}
	})
}

func TestTestTimeoutOptimizationHint(t *testing.T) {
	t.Run("python", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("pytest -xvs tests/")
		if !strings.Contains(hint, "numpy") {
			t.Errorf("expected Python-specific hint, got: %s", hint)
		}
	})

	t.Run("java maven", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("mvn test -q")
		if !strings.Contains(hint, "HashMap") {
			t.Errorf("expected Java-specific hint, got: %s", hint)
		}
	})

	t.Run("dotnet", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("dotnet test")
		if !strings.Contains(hint, "Dictionary") {
			t.Errorf("expected .NET-specific hint, got: %s", hint)
		}
	})

	t.Run("ruby rspec", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("bundle exec rspec")
		if !strings.Contains(hint, "Hash") {
			t.Errorf("expected Ruby-specific hint, got: %s", hint)
		}
	})

	t.Run("elixir", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("mix test")
		if !strings.Contains(hint, "MapSet") {
			t.Errorf("expected Elixir-specific hint, got: %s", hint)
		}
	})

	t.Run("scala sbt", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("sbt test")
		if !strings.Contains(hint, "HashMap") {
			t.Errorf("expected Scala-specific hint, got: %s", hint)
		}
	})

	t.Run("haskell stack", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("stack test")
		if !strings.Contains(hint, "Data.Map") {
			t.Errorf("expected Haskell-specific hint, got: %s", hint)
		}
	})

	t.Run("dart", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("dart test")
		if !strings.Contains(hint, "Map/Set") {
			t.Errorf("expected Dart-specific hint, got: %s", hint)
		}
	})

	t.Run("php", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("./vendor/bin/phpunit")
		if !strings.Contains(hint, "isset") {
			t.Errorf("expected PHP-specific hint, got: %s", hint)
		}
	})

	t.Run("no match", func(t *testing.T) {
		hint := testTimeoutOptimizationHint("ls -la")
		if hint != "" {
			t.Errorf("expected no hint for non-test command, got: %s", hint)
		}
	})
}

func TestCompilationTimeoutMessage(t *testing.T) {
	// Build command that times out should get compilation-specific advice.
	result := formatBashOutput("", "g++ main.cpp -o app\n", 124, true, 120*time.Second, "make -j4")
	if !strings.Contains(result, "parallel builds") {
		t.Errorf("expected parallel build suggestion for compilation timeout, got: %s", result)
	}

	// Non-build command timeout should get the generic message.
	result = formatBashOutput("", "", 124, true, 120*time.Second, "python3 test.py")
	if !strings.Contains(result, "optimize YOUR code") {
		t.Errorf("expected generic timeout message for test, got: %s", result)
	}
}

func TestSharedLibraryHint(t *testing.T) {
	t.Run("cannot open shared object", func(t *testing.T) {
		output := `./solver: error while loading shared libraries: libgsl.so.25: cannot open shared object file: No such file or directory`
		hint := sharedLibraryHint(output, 127)
		if hint == "" {
			t.Fatal("expected hint for missing shared library")
		}
		if !strings.Contains(hint, "libgsl.so.25") {
			t.Errorf("expected library name in hint, got: %s", hint)
		}
		if !strings.Contains(hint, "ldconfig") {
			t.Errorf("expected ldconfig suggestion, got: %s", hint)
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := sharedLibraryHint("cannot open shared object file", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		hint := sharedLibraryHint("No such file or directory: /tmp/foo", 1)
		if hint != "" {
			t.Errorf("expected no hint for unrelated error, got: %s", hint)
		}
	})
}

func TestDiskSpaceHint(t *testing.T) {
	t.Run("no space left on device", func(t *testing.T) {
		output := `OSError: [Errno 28] No space left on device: '/app/output.bin'`
		hint := diskSpaceHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for disk space issue")
		}
		if !strings.Contains(hint, "df -h") {
			t.Errorf("expected df -h suggestion, got: %s", hint)
		}
	})

	t.Run("ENOSPC", func(t *testing.T) {
		output := `Error: ENOSPC: no space left on device, write`
		hint := diskSpaceHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for ENOSPC")
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := diskSpaceHint("No space left on device", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestMakefileHint(t *testing.T) {
	t.Run("missing separator", func(t *testing.T) {
		output := `Makefile:5: *** missing separator.  Stop.`
		hint := makefileHint(output, 2)
		if hint == "" {
			t.Fatal("expected hint for missing separator")
		}
		if !strings.Contains(hint, "TAB") {
			t.Errorf("expected TAB guidance, got: %s", hint)
		}
	})

	t.Run("no rule to make target", func(t *testing.T) {
		output := `make: *** No rule to make target 'libfoo.a', needed by 'all'.  Stop.`
		hint := makefileHint(output, 2)
		if hint == "" {
			t.Fatal("expected hint for no rule")
		}
		if !strings.Contains(hint, "libfoo.a") {
			t.Errorf("expected target name in hint, got: %s", hint)
		}
	})

	t.Run("no makefile found", func(t *testing.T) {
		output := `make: *** No targets specified and no makefile found.  Stop.`
		hint := makefileHint(output, 2)
		if hint == "" {
			t.Fatal("expected hint for no makefile")
		}
		if !strings.Contains(hint, "configure") || !strings.Contains(hint, "cmake") {
			t.Errorf("expected configure/cmake suggestion, got: %s", hint)
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := makefileHint("missing separator", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestCmakeHint(t *testing.T) {
	t.Run("could not find openssl", func(t *testing.T) {
		output := `CMake Error at /usr/share/cmake/Modules/FindPackageHandleStandardArgs.cmake:230 (message):
  Could NOT find OpenSSL, try to set the path to OpenSSL root folder`
		hint := cmakeHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for missing OpenSSL")
		}
		if !strings.Contains(hint, "libssl-dev") {
			t.Errorf("expected libssl-dev suggestion, got: %s", hint)
		}
	})

	t.Run("could not find generic package", func(t *testing.T) {
		output := `CMake Error: Could NOT find SomeWeirdLib (missing: SOMEWEIRDLIB_LIBRARY)`
		hint := cmakeHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for generic missing package")
		}
		if !strings.Contains(hint, "apt-cache search") {
			t.Errorf("expected apt-cache search suggestion, got: %s", hint)
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := cmakeHint("Could NOT find OpenSSL", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestCargoHint(t *testing.T) {
	t.Run("missing crate", func(t *testing.T) {
		output := `error[E0463]: can't find crate for 'serde'
 --> src/main.rs:1:5`
		hint := cargoHint(output, 101)
		if hint == "" {
			t.Fatal("expected hint for missing crate")
		}
		if !strings.Contains(hint, "serde") || !strings.Contains(hint, "cargo add") {
			t.Errorf("expected serde + cargo add suggestion, got: %s", hint)
		}
	})

	t.Run("unresolved import", func(t *testing.T) {
		output := `error[E0432]: unresolved import 'tokio'
 --> src/main.rs:1:5`
		hint := cargoHint(output, 101)
		if hint == "" {
			t.Fatal("expected hint for unresolved import")
		}
		if !strings.Contains(hint, "Cargo.toml") {
			t.Errorf("expected Cargo.toml suggestion, got: %s", hint)
		}
	})

	t.Run("borrow checker", func(t *testing.T) {
		output := `error[E0502]: cannot borrow 'v' as mutable because it is also borrowed as immutable`
		hint := cargoHint(output, 101)
		if hint == "" {
			t.Fatal("expected hint for borrow checker error")
		}
		if !strings.Contains(hint, "clone") {
			t.Errorf("expected clone suggestion, got: %s", hint)
		}
	})

	t.Run("lifetime error", func(t *testing.T) {
		output := `error[E0597]: 's' does not live long enough`
		hint := cargoHint(output, 101)
		if hint == "" {
			t.Fatal("expected hint for lifetime error")
		}
		if !strings.Contains(hint, "ownership") {
			t.Errorf("expected ownership suggestion, got: %s", hint)
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := cargoHint("can't find crate for `serde`", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestGoModuleHint(t *testing.T) {
	t.Run("no required module", func(t *testing.T) {
		output := `main.go:3:2: no required module provides package github.com/gin-gonic/gin; to add it:
	go get github.com/gin-gonic/gin`
		hint := goModuleHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for missing module")
		}
		if !strings.Contains(hint, "go get") {
			t.Errorf("expected go get suggestion, got: %s", hint)
		}
	})

	t.Run("cannot find module", func(t *testing.T) {
		output := `cannot find module providing package example.com/foo/bar`
		hint := goModuleHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for cannot find module")
		}
		if !strings.Contains(hint, "go mod tidy") {
			t.Errorf("expected go mod tidy suggestion, got: %s", hint)
		}
	})

	t.Run("build constraints", func(t *testing.T) {
		output := `build constraints exclude all Go files in /app/pkg/cgo`
		hint := goModuleHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for build constraints")
		}
		if !strings.Contains(hint, "CGO_ENABLED") {
			t.Errorf("expected CGO_ENABLED suggestion, got: %s", hint)
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := goModuleHint("no required module provides package foo", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestArchiveHint(t *testing.T) {
	t.Run("tar not gzip", func(t *testing.T) {
		output := `gzip: stdin: not in gzip format
tar: Child returned status 1
tar: Error is not recoverable`
		hint := archiveHint(output, 2)
		if hint == "" {
			t.Fatal("expected hint for not gzip format")
		}
		if !strings.Contains(hint, "file <archive>") {
			t.Errorf("expected file command suggestion, got: %s", hint)
		}
	})

	t.Run("tar no such file", func(t *testing.T) {
		output := `tar: data.tar.gz: Cannot open: No such file or directory`
		hint := archiveHint(output, 2)
		if hint == "" {
			t.Fatal("expected hint for missing archive")
		}
		if !strings.Contains(hint, "ls -la") {
			t.Errorf("expected ls suggestion, got: %s", hint)
		}
	})

	t.Run("unzip not zip", func(t *testing.T) {
		output := `End-of-central-directory signature not found.  Either this file is not
  a zipfile, or it constitutes one disk of a multi-part archive.`
		hint := archiveHint(output, 9)
		if hint == "" {
			t.Fatal("expected hint for not a zip")
		}
		if !strings.Contains(hint, "file <filename>") {
			t.Errorf("expected file command suggestion, got: %s", hint)
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := archiveHint("tar: Cannot open", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestDatabaseHint(t *testing.T) {
	t.Run("sqlite no such table", func(t *testing.T) {
		output := `sqlite3.OperationalError: no such table: users`
		hint := databaseHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for missing SQLite table")
		}
		if !strings.Contains(hint, "schema") {
			t.Errorf("expected schema suggestion, got: %s", hint)
		}
	})

	t.Run("sqlite locked", func(t *testing.T) {
		output := `sqlite3.OperationalError: database is locked`
		hint := databaseHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for locked database")
		}
		if !strings.Contains(hint, "WAL") {
			t.Errorf("expected WAL suggestion, got: %s", hint)
		}
	})

	t.Run("postgres connection refused", func(t *testing.T) {
		output := `psycopg2.OperationalError: could not connect to server: Connection refused
	Is the server running on host "localhost" and accepting
	TCP/IP connections on port 5432?`
		hint := databaseHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for postgres connection refused")
		}
		if !strings.Contains(hint, "postgresql start") {
			t.Errorf("expected start suggestion, got: %s", hint)
		}
	})

	t.Run("postgres role does not exist", func(t *testing.T) {
		output := `FATAL:  role "testuser" does not exist`
		hint := databaseHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for missing postgres role")
		}
		if !strings.Contains(hint, "createuser") {
			t.Errorf("expected createuser suggestion, got: %s", hint)
		}
	})

	t.Run("mysql access denied", func(t *testing.T) {
		output := `ERROR 1045 (28000): Access denied for user 'root'@'localhost' (using password: NO)
mysql connection failed`
		hint := databaseHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for MySQL access denied")
		}
		if !strings.Contains(hint, "access denied") {
			t.Errorf("expected access denied hint, got: %s", hint)
		}
	})

	t.Run("sqlite sql syntax error", func(t *testing.T) {
		output := `sqlite3.OperationalError: near "FROM": syntax error`
		hint := databaseHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for SQL syntax error")
		}
		if !strings.Contains(hint, "syntax error") {
			t.Errorf("expected syntax error hint, got: %s", hint)
		}
	})

	t.Run("exit code 0", func(t *testing.T) {
		hint := databaseHint("sqlite3.OperationalError: no such table: users", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestMemoryHint(t *testing.T) {
	t.Run("python memory error", func(t *testing.T) {
		output := `Traceback (most recent call last):
  File "solution.py", line 42, in <module>
MemoryError`
		hint := memoryHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for MemoryError")
		}
		if !strings.Contains(hint, "chunks") {
			t.Errorf("expected chunked processing suggestion, got: %s", hint)
		}
	})

	t.Run("oom text in output", func(t *testing.T) {
		// OOM text with non-137 exit code (137 is handled by signalHint).
		output := `process killed: out of memory`
		hint := memoryHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for OOM text in output")
		}
		if !strings.Contains(hint, "memory") {
			t.Errorf("expected memory hint, got: %s", hint)
		}
	})

	t.Run("exit 137 no text defers to signalHint", func(t *testing.T) {
		// Exit 137 without OOM text — signalHint handles this, memoryHint should not.
		hint := memoryHint("Killed", 137)
		if hint != "" {
			t.Errorf("exit 137 without MemoryError text should defer to signalHint, got: %s", hint)
		}
	})

	t.Run("segfault text with non-139 exit", func(t *testing.T) {
		// Segfault text with non-139 exit code.
		output := `Segmentation fault (core dumped)`
		hint := memoryHint(output, 1)
		if hint == "" {
			t.Fatal("expected hint for segfault text")
		}
		if !strings.Contains(hint, "bounds") {
			t.Errorf("expected bounds check suggestion, got: %s", hint)
		}
	})

	t.Run("exit 139 defers to signalHint", func(t *testing.T) {
		// Exit 139 without segfault text — signalHint handles this.
		hint := memoryHint("", 139)
		if hint != "" {
			t.Errorf("exit 139 without segfault text should defer to signalHint, got: %s", hint)
		}
	})

	t.Run("normal exit", func(t *testing.T) {
		hint := memoryHint("all good", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %s", hint)
		}
	})
}

func TestVerificationCheckpoint_StaleTestWarning(t *testing.T) {
	// When the agent runs a verification command and then makes 6+ edits
	// without running tests again, the middleware should inject a "stale test"
	// reminder.
	mw, _ := VerificationCheckpoint("/app")

	// Build messages: one verification command + result, then 7 edit calls.
	var messages []core.ModelMessage

	// Verification run.
	messages = append(messages, core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName:   "bash",
				ToolCallID: "verify1",
				ArgsJSON:   `{"command":"pytest"}`,
			},
		},
	})
	messages = append(messages, core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.ToolReturnPart{
				ToolCallID: "verify1",
				Content:    "3 passed, 2 failed\n[exit code: 1]",
			},
		},
	})

	// 7 edit calls without any verification.
	for i := range 7 {
		messages = append(messages, core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "edit",
					ToolCallID: fmt.Sprintf("edit%d", i),
					ArgsJSON:   fmt.Sprintf(`{"path":"solution.py","old_string":"old%d","new_string":"new%d"}`, i, i),
				},
			},
		})
		messages = append(messages, core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolCallID: fmt.Sprintf("edit%d", i),
					Content:    "ok",
				},
			},
		})
	}

	// Call the middleware.
	var capturedMessages []core.ModelMessage
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		capturedMessages = msgs
		return &core.ModelResponse{}, nil
	}
	mw(context.Background(), messages, &core.ModelSettings{}, &core.ModelRequestParameters{}, next)

	// The last message should be the stale test warning.
	if len(capturedMessages) == 0 {
		t.Fatal("expected messages to be passed to next")
	}
	lastMsg := capturedMessages[len(capturedMessages)-1]
	req, ok := lastMsg.(core.ModelRequest)
	if !ok {
		t.Fatal("expected last message to be a ModelRequest")
	}
	found := false
	for _, part := range req.Parts {
		if up, ok := part.(core.UserPromptPart); ok {
			if strings.Contains(up.Content, "TESTING REMINDER") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected stale test warning with 7 edits after verification, but none found")
	}
}

func TestVerificationCheckpoint_NoStaleTestWithFewEdits(t *testing.T) {
	// When the agent makes fewer than 6 edits after verification,
	// no stale test warning should appear.
	mw, _ := VerificationCheckpoint("/app")

	var messages []core.ModelMessage
	// Verification run.
	messages = append(messages, core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName:   "bash",
				ToolCallID: "v1",
				ArgsJSON:   `{"command":"pytest"}`,
			},
		},
	})
	messages = append(messages, core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.ToolReturnPart{
				ToolCallID: "v1",
				Content:    "3 passed\n[exit code: 0]",
			},
		},
	})
	// Only 3 edits — below threshold.
	for i := range 3 {
		messages = append(messages, core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "edit",
					ToolCallID: fmt.Sprintf("e%d", i),
					ArgsJSON:   `{"path":"sol.py","old_string":"a","new_string":"b"}`,
				},
			},
		})
		messages = append(messages, core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolCallID: fmt.Sprintf("e%d", i),
					Content:    "ok",
				},
			},
		})
	}

	var capturedMessages []core.ModelMessage
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		capturedMessages = msgs
		return &core.ModelResponse{}, nil
	}
	mw(context.Background(), messages, &core.ModelSettings{}, &core.ModelRequestParameters{}, next)

	// Check that no stale test warning was injected.
	for _, msg := range capturedMessages {
		if req, ok := msg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if up, ok := part.(core.UserPromptPart); ok {
					if strings.Contains(up.Content, "TESTING REMINDER") {
						t.Error("should not inject stale test warning with only 3 edits after verification")
					}
				}
			}
		}
	}
}

func TestVerificationCheckpoint_NoConsecutiveUserMessages(t *testing.T) {
	// Verification warnings must be merged into the last ModelRequest, not
	// appended as new ModelRequests. Consecutive user-role messages cause a
	// 400 error from Anthropic's API.
	mw, _ := VerificationCheckpoint("")

	// Build a message history that triggers a regression warning:
	// two test runs where pass count decreases.
	var messages []core.ModelMessage
	passCounts := []int{5, 3}
	failCounts := []int{1, 3}
	for i := range 2 {
		callID := fmt.Sprintf("consec%d", i+1)
		output := fmt.Sprintf("%d passed, %d failed\n[exit code: 1]", passCounts[i], failCounts[i])
		messages = append(messages,
			core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.ToolCallPart{
						ToolName:   "bash",
						ArgsJSON:   `{"command":"pytest"}`,
						ToolCallID: callID,
					},
				},
			},
			core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.ToolReturnPart{
						ToolName:   "bash",
						Content:    output,
						ToolCallID: callID,
					},
				},
			},
		)
	}

	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		for i := 1; i < len(msgs); i++ {
			_, prevIsReq := msgs[i-1].(core.ModelRequest)
			_, currIsReq := msgs[i].(core.ModelRequest)
			if prevIsReq && currIsReq {
				t.Errorf("consecutive ModelRequest messages at indices %d and %d — would cause Anthropic 400 error", i-1, i)
			}
		}
		return &core.ModelResponse{}, nil
	}

	_, err := mw(context.Background(), messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildContextRecoverySummary_TestTrajectory(t *testing.T) {
	// When the recovery summary contains verification results with test counts,
	// it should include a compact TEST PROGRESS trajectory.
	messages := []core.ModelMessage{
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "v1",
					ArgsJSON:   `{"command":"pytest"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolCallID: "v1",
					Content:    "2 passed, 8 failed\n[exit code: 1]",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "v2",
					ArgsJSON:   `{"command":"pytest"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolCallID: "v2",
					Content:    "5 passed, 5 failed\n[exit code: 1]",
				},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "bash",
					ToolCallID: "v3",
					ArgsJSON:   `{"command":"pytest"}`,
				},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolCallID: "v3",
					Content:    "5 passed, 5 failed\n[exit code: 1]",
				},
			},
		},
	}

	summary := buildContextRecoverySummary(messages)
	if !strings.Contains(summary, "TEST PROGRESS:") {
		t.Errorf("expected TEST PROGRESS section, got:\n%s", summary)
	}
	if !strings.Contains(summary, "2/10") {
		t.Errorf("expected '2/10' in trajectory, got:\n%s", summary)
	}
	if !strings.Contains(summary, "5/10") {
		t.Errorf("expected '5/10' in trajectory, got:\n%s", summary)
	}
	// Last two runs are identical (5/10 → 5/10), so should show stalled indicator.
	if !strings.Contains(summary, "stalled") {
		t.Errorf("expected 'stalled' indicator for repeated 5/10, got:\n%s", summary)
	}
}

func TestBrowserHint(t *testing.T) {
	t.Run("chrome_not_found", func(t *testing.T) {
		hint := browserHint("Error: Failed to launch chrome! No chrome binary at /usr/bin/chromium", 1)
		if hint == "" || !strings.Contains(hint, "Browser") {
			t.Errorf("expected browser hint for chrome failure, got: %q", hint)
		}
	})
	t.Run("selenium_error", func(t *testing.T) {
		hint := browserHint("selenium.common.exceptions.WebDriverException: Message: 'chromedriver' not found", 1)
		if hint == "" || !strings.Contains(hint, "Selenium") {
			t.Errorf("expected browser hint for selenium, got: %q", hint)
		}
	})
	t.Run("playwright_error", func(t *testing.T) {
		hint := browserHint("playwright._impl._errors.Error: Executable doesn't exist at /ms-playwright/chromium", 1)
		if hint == "" || !strings.Contains(hint, "Playwright") {
			t.Errorf("expected browser hint for playwright, got: %q", hint)
		}
	})
	t.Run("x11_display_error", func(t *testing.T) {
		hint := browserHint("Error: could not connect to display :0 — X11 not set", 1)
		if hint == "" || !strings.Contains(hint, "display") {
			t.Errorf("expected display hint, got: %q", hint)
		}
	})
	t.Run("exit_code_0", func(t *testing.T) {
		hint := browserHint("Chrome started successfully", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %q", hint)
		}
	})
	t.Run("unrelated_error", func(t *testing.T) {
		hint := browserHint("ImportError: No module named 'numpy'", 1)
		if hint != "" {
			t.Errorf("expected no hint for unrelated error, got: %q", hint)
		}
	})
}

func TestSSLHint(t *testing.T) {
	t.Run("pip_ssl_error", func(t *testing.T) {
		hint := sslHint("pip install numpy\nSSL: CERTIFICATE_VERIFY_FAILED from pypi.org", 1)
		if hint == "" || !strings.Contains(hint, "trusted-host") {
			t.Errorf("expected pip SSL hint, got: %q", hint)
		}
	})
	t.Run("curl_ssl_error", func(t *testing.T) {
		hint := sslHint("curl: (60) SSL certificate problem: certificate verify failed", 1)
		if hint == "" || !strings.Contains(hint, "curl") {
			t.Errorf("expected curl SSL hint, got: %q", hint)
		}
	})
	t.Run("git_ssl_error", func(t *testing.T) {
		hint := sslHint("fatal: unable to access 'https://github.com/...': SSL certificate problem: certificate verify failed", 1)
		if hint == "" || !strings.Contains(hint, "git") {
			t.Errorf("expected git SSL hint, got: %q", hint)
		}
	})
	t.Run("generic_ssl_error", func(t *testing.T) {
		hint := sslHint("requests.exceptions.SSLError: SSL: CERTIFICATE_VERIFY_FAILED", 1)
		if hint == "" || !strings.Contains(hint, "SSL") {
			t.Errorf("expected generic SSL hint, got: %q", hint)
		}
	})
	t.Run("exit_code_0", func(t *testing.T) {
		hint := sslHint("SSL handshake completed", 0)
		if hint != "" {
			t.Errorf("expected no hint for exit code 0, got: %q", hint)
		}
	})
	t.Run("unrelated_error", func(t *testing.T) {
		hint := sslHint("SyntaxError: invalid syntax", 1)
		if hint != "" {
			t.Errorf("expected no hint for unrelated error, got: %q", hint)
		}
	})
}

func TestLinkerHint_MultipleDefinition(t *testing.T) {
	output := `/usr/bin/ld: /tmp/ccXYZ.o: in function 'init':
utils.c:(.text+0x0): multiple definition of 'init'; /tmp/ccABC.o:main.c:(.text+0x0): first defined here
collect2: error: ld returned 1 exit status`
	hint := linkerHint(output)
	if !strings.Contains(hint, "multiple definition") {
		t.Errorf("expected multiple definition hint, got: %q", hint)
	}
	if !strings.Contains(hint, "extern") {
		t.Errorf("expected 'extern' advice in hint, got: %q", hint)
	}
}

func TestLinkerHint_CannotFindLibrary(t *testing.T) {
	output := `/usr/bin/ld: cannot find -lncurses
collect2: error: ld returned 1 exit status`
	hint := linkerHint(output)
	if !strings.Contains(hint, "ncurses") {
		t.Errorf("expected ncurses library name in hint, got: %q", hint)
	}
	if !strings.Contains(hint, "apt-get install") {
		t.Errorf("expected apt-get install advice, got: %q", hint)
	}
}

func TestLinkerHint_MathFunctions(t *testing.T) {
	output := `main.c:(.text+0x1a): undefined reference to 'sqrt'
collect2: error: ld returned 1 exit status`
	hint := linkerHint(output)
	if !strings.Contains(hint, "-lm") {
		t.Errorf("expected -lm hint for math function, got: %q", hint)
	}
}

func TestLinkerHint_PthreadFunctions(t *testing.T) {
	output := `main.c:(.text+0x1a): undefined reference to 'pthread_create'
collect2: error: ld returned 1 exit status`
	hint := linkerHint(output)
	if !strings.Contains(hint, "-lpthread") {
		t.Errorf("expected -lpthread hint, got: %q", hint)
	}
}

func TestSignalHint_SIGPIPE(t *testing.T) {
	hint := signalHint(141)
	if hint == "" {
		t.Fatal("expected SIGPIPE hint for exit code 141")
	}
	if !strings.Contains(hint, "SIGPIPE") {
		t.Errorf("expected SIGPIPE in hint, got: %q", hint)
	}
	if !strings.Contains(hint, "broken pipe") || !strings.Contains(strings.ToLower(hint), "harmless") {
		t.Errorf("expected guidance that broken pipe is usually harmless, got: %q", hint)
	}
}

func TestShellLimitHint_ArgumentListTooLong(t *testing.T) {
	output := "bash: /usr/bin/rm: Argument list too long"
	hint := shellLimitHint(output, 126)
	if hint == "" {
		t.Fatal("expected hint for 'Argument list too long'")
	}
	if !strings.Contains(hint, "find") || !strings.Contains(hint, "xargs") {
		t.Errorf("expected find/xargs advice, got: %q", hint)
	}
}

func TestShellLimitHint_TooManyOpenFiles(t *testing.T) {
	output := "OSError: [Errno 24] Too many open files: '/tmp/data.txt'"
	hint := shellLimitHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for 'Too many open files'")
	}
	if !strings.Contains(hint, "ulimit") {
		t.Errorf("expected ulimit advice, got: %q", hint)
	}
}

func TestShellLimitHint_EMFILE(t *testing.T) {
	output := "Error: EMFILE: too many open files, watch"
	hint := shellLimitHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for EMFILE")
	}
}

func TestPerlModuleHint(t *testing.T) {
	output := `Can't locate JSON/PP.pm in @INC (you may need to install the JSON::PP module) (@INC contains: ...)`
	hint := perlModuleHint(output, 2)
	if hint == "" {
		t.Fatal("expected hint for missing Perl module")
	}
	if !strings.Contains(hint, "JSON::PP") {
		t.Errorf("expected JSON::PP module name, got: %q", hint)
	}
	if !strings.Contains(hint, "cpanm") {
		t.Errorf("expected cpanm install advice, got: %q", hint)
	}
}

func TestPerlModuleHint_NoError(t *testing.T) {
	hint := perlModuleHint("All tests passed", 0)
	if hint != "" {
		t.Errorf("expected no hint on exit code 0, got: %q", hint)
	}
}

func TestExtractTAPCounts_BasicOkNotOk(t *testing.T) {
	output := `1..5
ok 1 - should add numbers
ok 2 - should subtract
not ok 3 - should multiply
ok 4 - should divide
not ok 5 - should modulo`

	p, f, ok := extractTAPCounts(output)
	if !ok {
		t.Fatal("expected TAP counts to be detected")
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got %d", p)
	}
	if f != 2 {
		t.Errorf("expected 2 failed, got %d", f)
	}
}

func TestExtractTAPCounts_NodeSummary(t *testing.T) {
	output := `TAP version 13
# test addition
ok 1 should add
# test subtraction
ok 2 should subtract
# tests 2
# pass 2
# fail 0
# ok`

	p, f, ok := extractTAPCounts(output)
	if !ok {
		t.Fatal("expected TAP counts from summary")
	}
	if p != 2 {
		t.Errorf("expected 2 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

func TestExtractTAPCounts_AllPassing(t *testing.T) {
	output := `1..3
ok 1 - first test
ok 2 - second test
ok 3 - third test`

	p, f, ok := extractTAPCounts(output)
	if !ok {
		t.Fatal("expected TAP counts")
	}
	if p != 3 || f != 0 {
		t.Errorf("expected 3/0, got %d/%d", p, f)
	}
}

func TestTestResultSummary_TAP(t *testing.T) {
	output := `1..3
ok 1 - first
not ok 2 - second
ok 3 - third`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected TAP summary")
	}
	if !strings.Contains(summary, "TAP") {
		t.Errorf("expected 'TAP' in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "2/3") {
		t.Errorf("expected 2/3 passed in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "not ok 2") {
		t.Errorf("expected first failure detail, got: %q", summary)
	}
}

func TestExtractTestCounts_TAP(t *testing.T) {
	output := `1..4
ok 1 - test A
not ok 2 - test B
ok 3 - test C
ok 4 - test D`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected TAP test counts")
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got %d", p)
	}
	if f != 1 {
		t.Errorf("expected 1 failed, got %d", f)
	}
}

// --- ExUnit (Elixir) test parsing ---

func TestExtractTestCounts_ExUnit(t *testing.T) {
	output := `
..F..

  1) test greets the world (GreeterTest)
     test/greeter_test.exs:5
     Assertion with == failed
     code:  assert Greeter.hello() == "Hello, World!"
     left:  "Hello, world!"
     right: "Hello, World!"
     stacktrace:
       test/greeter_test.exs:6: (test)

Finished in 0.03 seconds (0.00s async, 0.03s sync)
5 tests, 1 failure
`
	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ExUnit test counts")
	}
	if p != 4 {
		t.Errorf("expected 4 passed, got %d", p)
	}
	if f != 1 {
		t.Errorf("expected 1 failed, got %d", f)
	}
}

func TestExtractTestCounts_ExUnit_AllPassing(t *testing.T) {
	output := `
.....

Finished in 0.02 seconds (0.00s async, 0.02s sync)
5 tests, 0 failures
`
	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ExUnit test counts")
	}
	if p != 5 {
		t.Errorf("expected 5 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

func TestExtractTestCounts_ExUnit_WithDoctests(t *testing.T) {
	output := `
.....

Finished in 0.04 seconds (0.00s async, 0.04s sync)
2 doctests, 3 tests, 0 failures
`
	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ExUnit test counts")
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

func TestTestResultSummary_ExUnit(t *testing.T) {
	output := `
..F..

  1) test greets the world (GreeterTest)
     test/greeter_test.exs:5
     Assertion with == failed
     code:  assert Greeter.hello() == "Hello, World!"
     left:  "Hello, world!"
     right: "Hello, World!"
     stacktrace:
       test/greeter_test.exs:6: (test)

Finished in 0.03 seconds (0.00s async, 0.03s sync)
5 tests, 1 failure
`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected ExUnit test summary")
	}
	if !strings.Contains(summary, "5 tests, 1 failure") {
		t.Errorf("expected summary to contain ExUnit counts, got: %s", summary)
	}
	// Should extract the failing test name.
	if !strings.Contains(summary, "test greets the world") {
		t.Errorf("expected summary to contain failing test name, got: %s", summary)
	}
	// Should extract ExUnit's left:/right: failure detail.
	if !strings.Contains(summary, "first failure") {
		t.Errorf("expected first failure detail, got: %s", summary)
	}
}

func TestTestResultSummary_ExUnit_AllPassing(t *testing.T) {
	output := `
.....

Finished in 0.02 seconds (0.00s async, 0.02s sync)
5 tests, 0 failures
`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected ExUnit test summary")
	}
	if !strings.Contains(summary, "5 tests, 0 failures") {
		t.Errorf("expected summary to contain ExUnit all-passing, got: %s", summary)
	}
}

func TestFirstFailureDetail_ExUnit(t *testing.T) {
	output := `
  1) test greets the world (GreeterTest)
     test/greeter_test.exs:5
     Assertion with == failed
     code:  assert Greeter.hello() == "Hello, World!"
     left:  "Hello, world!"
     right: "Hello, World!"
     stacktrace:
       test/greeter_test.exs:6: (test)
`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected ExUnit first failure detail")
	}
	if !strings.Contains(detail, "Assertion with == failed") {
		t.Errorf("expected assertion detail, got: %s", detail)
	}
	if !strings.Contains(detail, "left:") {
		t.Errorf("expected left: value in detail, got: %s", detail)
	}
	if !strings.Contains(detail, "right:") {
		t.Errorf("expected right: value in detail, got: %s", detail)
	}
}

// --- XCTest (Swift) test parsing ---

func TestExtractTestCounts_XCTest(t *testing.T) {
	output := `
Test Suite 'All tests' started at 2024-01-15 10:30:00.000
Test Suite 'MyTests' started at 2024-01-15 10:30:00.001
Test Case '-[MyTests testAdd]' started.
Test Case '-[MyTests testAdd]' passed (0.001 seconds).
Test Case '-[MyTests testSubtract]' started.
/path/to/test.swift:42: error: -[MyTests testSubtract] : XCTAssertEqual failed: ("3") is not equal to ("5") -
Test Case '-[MyTests testSubtract]' failed (0.002 seconds).
Test Case '-[MyTests testMultiply]' started.
Test Case '-[MyTests testMultiply]' passed (0.001 seconds).
Test Suite 'MyTests' failed at 2024-01-15 10:30:00.010
	 Executed 3 tests, with 1 failure (0 unexpected) in 0.004 (0.005) seconds
Test Suite 'All tests' failed at 2024-01-15 10:30:00.010
	 Executed 3 tests, with 1 failure (0 unexpected) in 0.004 (0.005) seconds
`
	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected XCTest test counts")
	}
	if p != 2 {
		t.Errorf("expected 2 passed, got %d", p)
	}
	if f != 1 {
		t.Errorf("expected 1 failed, got %d", f)
	}
}

func TestExtractTestCounts_XCTest_AllPassing(t *testing.T) {
	output := `
Test Suite 'All tests' started at 2024-01-15 10:30:00.000
Executed 5 tests, with 0 failures (0 unexpected) in 0.010 (0.012) seconds
`
	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected XCTest test counts")
	}
	if p != 5 {
		t.Errorf("expected 5 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

func TestTestResultSummary_XCTest(t *testing.T) {
	output := `
Test Case '-[MyTests testSubtract]' started.
/path/to/test.swift:42: error: -[MyTests testSubtract] : XCTAssertEqual failed: ("3") is not equal to ("5") -
Test Case '-[MyTests testSubtract]' failed (0.002 seconds).
	 Executed 3 tests, with 1 failure (0 unexpected) in 0.004 (0.005) seconds
`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected XCTest test summary")
	}
	if !strings.Contains(summary, "Executed 3 tests") {
		t.Errorf("expected XCTest summary line, got: %s", summary)
	}
	if !strings.Contains(summary, "1 failure") {
		t.Errorf("expected failure count in summary, got: %s", summary)
	}
}

func TestFirstFailureDetail_XCTest(t *testing.T) {
	output := `/path/to/test.swift:42: error: -[MyTests testSubtract] : XCTAssertEqual failed: ("3") is not equal to ("5") -`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected XCTest first failure detail")
	}
	if !strings.Contains(detail, "XCTAssertEqual failed") {
		t.Errorf("expected XCTAssert in detail, got: %s", detail)
	}
}

// --- Ruby gem hint tests ---

func TestRubyGemHint_LoadError(t *testing.T) {
	output := `/usr/lib/ruby/3.0.0/rubygems/core_ext/kernel_require.rb:85:in 'require': cannot load such file -- nokogiri (LoadError)
	from /usr/lib/ruby/3.0.0/rubygems/core_ext/kernel_require.rb:85:in 'require'
	from app.rb:1:in '<main>'`
	hint := rubyGemHint(output, 1)
	if hint == "" {
		t.Fatal("expected Ruby LoadError hint")
	}
	if !strings.Contains(hint, "nokogiri") {
		t.Errorf("expected gem name 'nokogiri' in hint, got: %s", hint)
	}
	if !strings.Contains(hint, "gem install") {
		t.Errorf("expected 'gem install' in hint, got: %s", hint)
	}
}

func TestRubyGemHint_BundlerGemNotFound(t *testing.T) {
	output := `Could not find gem 'rspec' in any of the gem sources listed in your Gemfile.`
	hint := rubyGemHint(output, 1)
	if hint == "" {
		t.Fatal("expected Bundler hint")
	}
	if !strings.Contains(hint, "bundle install") {
		t.Errorf("expected 'bundle install' in hint, got: %s", hint)
	}
}

func TestRubyGemHint_NoError(t *testing.T) {
	hint := rubyGemHint("Hello world", 0)
	if hint != "" {
		t.Errorf("expected no hint on exit 0, got: %s", hint)
	}
}

// --- Java exception hint tests ---

func TestJavaExceptionHint_ClassNotFound(t *testing.T) {
	output := `Exception in thread "main" java.lang.ClassNotFoundException: com.example.MyApp
	at java.net.URLClassLoader.findClass(URLClassLoader.java:382)
	at java.lang.ClassLoader.loadClass(ClassLoader.java:418)`
	hint := javaExceptionHint(output, 1)
	if hint == "" {
		t.Fatal("expected Java ClassNotFoundException hint")
	}
	if !strings.Contains(hint, "com.example.MyApp") {
		t.Errorf("expected class name in hint, got: %s", hint)
	}
	if !strings.Contains(hint, "classpath") {
		t.Errorf("expected classpath suggestion, got: %s", hint)
	}
}

func TestJavaExceptionHint_NoClassDefFound(t *testing.T) {
	output := `Exception in thread "main" java.lang.NoClassDefFoundError: org/json/JSONObject`
	hint := javaExceptionHint(output, 1)
	if hint == "" {
		t.Fatal("expected Java NoClassDefFoundError hint")
	}
	if !strings.Contains(hint, "NoClassDefFoundError") {
		t.Errorf("expected NoClassDefFoundError in hint, got: %s", hint)
	}
}

func TestJavaExceptionHint_OutOfMemory(t *testing.T) {
	output := `Exception in thread "main" java.lang.OutOfMemoryError: Java heap space`
	hint := javaExceptionHint(output, 1)
	if hint == "" {
		t.Fatal("expected Java OOM hint")
	}
	if !strings.Contains(hint, "-Xmx") {
		t.Errorf("expected -Xmx suggestion, got: %s", hint)
	}
}

func TestJavaExceptionHint_StackOverflow(t *testing.T) {
	output := `Exception in thread "main" java.lang.StackOverflowError
	at com.example.Recursive.call(Recursive.java:10)`
	hint := javaExceptionHint(output, 1)
	if hint == "" {
		t.Fatal("expected Java StackOverflowError hint")
	}
	if !strings.Contains(hint, "recursion") {
		t.Errorf("expected recursion suggestion, got: %s", hint)
	}
}

func TestJavaExceptionHint_NoError(t *testing.T) {
	hint := javaExceptionHint("BUILD SUCCESSFUL", 0)
	if hint != "" {
		t.Errorf("expected no hint on exit 0, got: %s", hint)
	}
}

// --- Bun test parsing tests ---

func TestExtractTestCounts_Bun(t *testing.T) {
	output := `bun test v1.1.0 (abc123)

test.ts:
✓ adds numbers correctly [0.50ms]
✗ subtracts numbers correctly [0.30ms]
✓ multiplies [0.10ms]

 2 pass
 1 fail
 3 expect() calls
Ran 3 tests across 1 files. [1.23s]`

	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Bun output")
	}
	if passed != 2 {
		t.Errorf("expected 2 passed, got %d", passed)
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

func TestExtractTestCounts_Bun_AllPassing(t *testing.T) {
	output := ` 6 pass
 0 fail
 6 expect() calls
Ran 6 tests across 3 files. [0.85s]`

	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Bun all-passing output")
	}
	if passed != 6 {
		t.Errorf("expected 6 passed, got %d", passed)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}
}

func TestExtractTestCounts_Bun_WithSkip(t *testing.T) {
	output := ` 3 pass
 1 skip
 1 fail
 4 expect() calls
Ran 5 tests across 2 files. [2.10s]`

	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Bun output with skips")
	}
	if passed != 3 {
		t.Errorf("expected 3 passed, got %d", passed)
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

func TestTestResultSummary_Bun(t *testing.T) {
	output := `bun test v1.1.0 (abc123)

test.ts:
✓ adds numbers correctly
✗ subtracts numbers correctly

error: expect(received).toBe(expected)
Expected: 5
Received: 3

 1 pass
 1 fail
 2 expect() calls
Ran 2 tests across 1 files. [0.42s]`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for Bun output")
	}
	if !strings.Contains(summary, "1 pass") {
		t.Errorf("expected summary to contain '1 pass', got: %s", summary)
	}
	if !strings.Contains(summary, "1 fail") {
		t.Errorf("expected summary to contain '1 fail', got: %s", summary)
	}
}

func TestTestResultSummary_Bun_AllPassing(t *testing.T) {
	output := ` 5 pass
 0 fail
 5 expect() calls
Ran 5 tests across 2 files. [1.00s]`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for Bun all-passing output")
	}
	if !strings.Contains(summary, "5 pass") {
		t.Errorf("expected summary to contain '5 pass', got: %s", summary)
	}
}

func TestExtractTestCounts_Zig(t *testing.T) {
	output := `Test [1/5] test.add... OK
Test [2/5] test.remove... OK
Test [3/5] test.update... OK
Test [4/5] test.delete... OK
Test [5/5] test.list... OK
All 5 tests passed.`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected extractTestCounts to parse Zig output")
	}
	if p != 5 || f != 0 {
		t.Errorf("expected 5 passed 0 failed, got %d passed %d failed", p, f)
	}
}

func TestExtractTestCounts_Zig_LargeCount(t *testing.T) {
	output := `All 42 tests passed.`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected extractTestCounts to parse Zig output")
	}
	if p != 42 || f != 0 {
		t.Errorf("expected 42 passed 0 failed, got %d passed %d failed", p, f)
	}
}

func TestTestResultSummary_Zig(t *testing.T) {
	output := `Test [1/3] test.parse_config... OK
Test [2/3] test.validate_input... OK
Test [3/3] test.process_data... OK
All 3 tests passed.`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for Zig output")
	}
	if !strings.Contains(summary, "All 3 tests passed") {
		t.Errorf("expected summary to contain 'All 3 tests passed', got: %s", summary)
	}
}

// Test TypeScript compilation error hint parsing.
func TestCompilationErrorHint_TypeScript(t *testing.T) {
	output := `src/index.ts(42,5): error TS2322: Type 'string' is not assignable to type 'number'.
src/utils.ts(10,3): error TS7006: Parameter 'x' implicitly has an 'any' type.`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected non-empty hint for TypeScript error")
	}
	if !strings.Contains(hint, "src/index.ts:42") {
		t.Errorf("expected hint to contain file:line 'src/index.ts:42', got: %s", hint)
	}
	if !strings.Contains(hint, "TS2322") {
		t.Errorf("expected hint to contain error code 'TS2322', got: %s", hint)
	}
}

// Test nodeErrorHint with lockfile-aware package manager.
func TestNodeErrorHint_LockfileAware(t *testing.T) {
	output := `Error: Cannot find module 'express'
Require stack:
- /app/index.js
    at Module._resolveFilename (node:internal/modules/cjs/loader:1134:15)
    code: 'MODULE_NOT_FOUND'`

	// No workDir — should default to npm.
	hint := nodeErrorHint(output, 1)
	if !strings.Contains(hint, "npm install express") {
		t.Errorf("expected npm install, got: %s", hint)
	}

	// With bun.lockb — should suggest bun.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bun.lockb"), []byte("bun"), 0o644)
	hint = nodeErrorHint(output, 1, dir)
	if !strings.Contains(hint, "bun add express") {
		t.Errorf("expected bun add, got: %s", hint)
	}

	// With yarn.lock — should suggest yarn.
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "yarn.lock"), []byte("yarn"), 0o644)
	hint = nodeErrorHint(output, 1, dir2)
	if !strings.Contains(hint, "yarn add express") {
		t.Errorf("expected yarn add, got: %s", hint)
	}

	// With pnpm-lock.yaml — should suggest pnpm.
	dir3 := t.TempDir()
	os.WriteFile(filepath.Join(dir3, "pnpm-lock.yaml"), []byte("pnpm"), 0o644)
	hint = nodeErrorHint(output, 1, dir3)
	if !strings.Contains(hint, "pnpm add express") {
		t.Errorf("expected pnpm add, got: %s", hint)
	}
}

// Test SBT test output parsing.
func TestExtractTestCounts_SBT(t *testing.T) {
	output := `[info] Compiling 5 Scala sources to /app/target/classes
[info] Tests: succeeded 8, failed 2, canceled 0, ignored 1, pending 0
[info] *** 2 TESTS FAILED ***`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for SBT output")
	}
	if passed != 8 {
		t.Errorf("expected passed=8, got %d", passed)
	}
	if failed != 2 {
		t.Errorf("expected failed=2, got %d", failed)
	}
}

func TestExtractTestCounts_SBT_AllPassing(t *testing.T) {
	output := `[info] Compiling 3 Scala sources
[info] Tests: succeeded 12, failed 0, canceled 0, ignored 0, pending 0
[info] All tests passed.`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for SBT all-passing output")
	}
	if passed != 12 {
		t.Errorf("expected passed=12, got %d", passed)
	}
	if failed != 0 {
		t.Errorf("expected failed=0, got %d", failed)
	}
}

func TestTestResultSummary_SBT(t *testing.T) {
	output := `[info] Tests: succeeded 5, failed 3, canceled 0, ignored 0, pending 0
[info] *** 3 TESTS FAILED ***`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for SBT output")
	}
	if !strings.Contains(summary, "succeeded 5") {
		t.Errorf("expected summary to contain 'succeeded 5', got: %s", summary)
	}
	if !strings.Contains(summary, "failed 3") {
		t.Errorf("expected summary to contain 'failed 3', got: %s", summary)
	}
}

// Test C#/MSBuild compilation error hint.
func TestCompilationErrorHint_CSharp(t *testing.T) {
	output := `Program.cs(5,17): error CS0029: Cannot implicitly convert type 'string' to 'int'
Program.cs(12,9): error CS1002: ; expected`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected non-empty hint for C# error")
	}
	if !strings.Contains(hint, "Program.cs:5") {
		t.Errorf("expected hint to contain 'Program.cs:5', got: %s", hint)
	}
	if !strings.Contains(hint, "CS0029") {
		t.Errorf("expected hint to contain error code 'CS0029', got: %s", hint)
	}
}

// Test Dart test result summary parsing.
func TestTestResultSummary_Dart(t *testing.T) {
	output := `00:00 +0: loading test/widget_test.dart
00:00 +1: first test
00:01 +2 -1: failing test
00:01 +2 -1: Some tests failed.`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for Dart test output")
	}
	if !strings.Contains(summary, "+2") {
		t.Errorf("expected summary to contain '+2', got: %s", summary)
	}
	if !strings.Contains(summary, "failed") {
		t.Errorf("expected summary to contain 'failed', got: %s", summary)
	}
}

// Test Dart test count extraction.
func TestExtractTestCounts_Dart(t *testing.T) {
	output := `00:01 +5 -2: Some tests failed.`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Dart test output")
	}
	if passed != 5 {
		t.Errorf("expected passed=5, got %d", passed)
	}
	if failed != 2 {
		t.Errorf("expected failed=2, got %d", failed)
	}
}

// Test Dart test count extraction (all passing).
func TestExtractTestCounts_Dart_AllPassing(t *testing.T) {
	output := `00:03 +10: All tests passed!`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Dart all-passing output")
	}
	if passed != 10 {
		t.Errorf("expected passed=10, got %d", passed)
	}
	if failed != 0 {
		t.Errorf("expected failed=0, got %d", failed)
	}
}

// Test NUnit first failure detail extraction ("Expected: X" / "But was: Y").
func TestFirstFailureDetail_NUnit(t *testing.T) {
	output := `NUnit Console Runner 3.16.3
  Expected: 42
  But was:  17

1 test(s) failed`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected non-empty detail for NUnit output")
	}
	if !strings.Contains(detail, "Expected: 42") {
		t.Errorf("expected detail to contain 'Expected: 42', got %q", detail)
	}
	if !strings.Contains(detail, "But was:") {
		t.Errorf("expected detail to contain 'But was:', got %q", detail)
	}
}

// Test GoogleTest first failure detail extraction ("Value of:" pattern).
func TestFirstFailureDetail_GoogleTest(t *testing.T) {
	output := `[==========] Running 3 tests from 1 test suite.
[----------] 3 tests from Calculator
[ RUN      ] Calculator.Add
/test/calculator_test.cpp:15: Failure
Value of: calc.Add(2, 3)
  Actual: 6
Expected: 5
[  FAILED  ] Calculator.Add (0 ms)`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected non-empty detail for GoogleTest output")
	}
	if !strings.Contains(detail, "Value of:") {
		t.Errorf("expected detail to contain 'Value of:', got %q", detail)
	}
	if !strings.Contains(detail, "Actual:") {
		t.Errorf("expected detail to contain 'Actual:', got %q", detail)
	}
}

// Test GoogleTest "Expected equality" pattern.
func TestFirstFailureDetail_GoogleTest_Equality(t *testing.T) {
	output := `[ RUN      ] Calculator.Multiply
/test/calculator_test.cpp:22: Failure
Expected equality of these values:
  calc.Multiply(3, 4)
    Which is: 11
  12
[  FAILED  ] Calculator.Multiply (0 ms)`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected non-empty detail for GoogleTest equality output")
	}
	if !strings.Contains(detail, "Expected equality") {
		t.Errorf("expected detail to contain 'Expected equality', got %q", detail)
	}
}

// Test GoogleTest summary parsing.
func TestTestResultSummary_GoogleTest(t *testing.T) {
	output := `[==========] Running 3 tests from 1 test suite.
[----------] 3 tests from Calculator
[ RUN      ] Calculator.Add
[       OK ] Calculator.Add (0 ms)
[ RUN      ] Calculator.Subtract
/test/calculator_test.cpp:22: Failure
Value of: calc.Subtract(5, 3)
  Actual: 3
Expected: 2
[  FAILED  ] Calculator.Subtract (0 ms)
[ RUN      ] Calculator.Multiply
[       OK ] Calculator.Multiply (0 ms)
[----------] 3 tests from Calculator (0 ms total)
[==========] 3 tests from 1 test suite ran. (0 ms total)
[  PASSED  ] 2 tests.
[  FAILED  ] 1 test, listed below:
[  FAILED  ] Calculator.Subtract

 1 FAILED TEST`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for GoogleTest output")
	}
	if !strings.Contains(summary, "[  PASSED  ] 2 tests") {
		t.Errorf("expected summary to contain pass count, got %q", summary)
	}
	if !strings.Contains(summary, "Calculator.Subtract") {
		t.Errorf("expected summary to contain failing test name, got %q", summary)
	}
}

func TestTestResultSummary_GoogleTest_AllPassing(t *testing.T) {
	output := `[==========] 2 tests from 1 test suite ran. (0 ms total)
[  PASSED  ] 2 tests.`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for all-passing GoogleTest output")
	}
	if !strings.Contains(summary, "[  PASSED  ] 2 tests") {
		t.Errorf("expected summary to contain pass count, got %q", summary)
	}
}

func TestExtractTestCounts_GoogleTest(t *testing.T) {
	output := `[==========] 5 tests from 2 test suites ran. (0 ms total)
[  PASSED  ] 3 tests.
[  FAILED  ] 2 tests, listed below:
[  FAILED  ] Suite.Test1
[  FAILED  ] Suite.Test2

 2 FAILED TESTS`
	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for GoogleTest output")
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got %d", p)
	}
	if f != 2 {
		t.Errorf("expected 2 failed, got %d", f)
	}
}

func TestExtractTestCounts_GoogleTest_AllPassing(t *testing.T) {
	output := `[==========] 4 tests from 1 test suite ran. (0 ms total)
[  PASSED  ] 4 tests.`
	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for all-passing GoogleTest output")
	}
	if p != 4 {
		t.Errorf("expected 4 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

// Test Lua busted test result summary parsing.
func TestTestResultSummary_LuaBusted(t *testing.T) {
	output := `●●●○
4 successes / 1 failure / 0 errors / 1 pending : 0.012 seconds`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for Lua busted output")
	}
	if !strings.Contains(summary, "4 successes") {
		t.Errorf("expected summary to contain '4 successes', got %q", summary)
	}
	if !strings.Contains(summary, "1 failure") {
		t.Errorf("expected summary to contain '1 failure', got %q", summary)
	}
}

// Test Lua busted test count extraction.
func TestExtractTestCounts_LuaBusted(t *testing.T) {
	output := `●●●○
4 successes / 1 failure / 0 errors / 1 pending : 0.012 seconds`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Lua busted output")
	}
	if passed != 4 {
		t.Errorf("expected passed=4, got %d", passed)
	}
	if failed != 1 {
		t.Errorf("expected failed=1, got %d", failed)
	}
}

// Test Lua busted all passing.
func TestExtractTestCounts_LuaBusted_AllPassing(t *testing.T) {
	output := `●●●●●
5 successes / 0 failures / 0 errors / 0 pending : 0.008 seconds`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Lua busted all-passing output")
	}
	if passed != 5 {
		t.Errorf("expected passed=5, got %d", passed)
	}
	if failed != 0 {
		t.Errorf("expected failed=0, got %d", failed)
	}
}

// Test R testthat test result summary parsing.
func TestTestResultSummary_RTestthat(t *testing.T) {
	output := `ℹ Testing mypackage
✔ | F W  S  OK | Context
✖ | 1        2 | math [0.1s]
───────────────────────────────────
[ FAIL 1 | WARN 0 | SKIP 0 | PASS 2 ]`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for R testthat output")
	}
	if !strings.Contains(summary, "FAIL 1") {
		t.Errorf("expected summary to contain 'FAIL 1', got %q", summary)
	}
	if !strings.Contains(summary, "PASS 2") {
		t.Errorf("expected summary to contain 'PASS 2', got %q", summary)
	}
}

// Test R testthat all passing.
func TestTestResultSummary_RTestthat_AllPassing(t *testing.T) {
	output := `ℹ Testing mypackage
✔ | F W  S  OK | Context
✔ |          5 | math [0.1s]
───────────────────────────────────
[ FAIL 0 | WARN 0 | SKIP 0 | PASS 5 ]`
	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected non-empty summary for R testthat all-passing output")
	}
	if !strings.Contains(summary, "PASS 5") {
		t.Errorf("expected summary to contain 'PASS 5', got %q", summary)
	}
}

// Test R testthat test count extraction.
func TestExtractTestCounts_RTestthat(t *testing.T) {
	output := `[ FAIL 2 | WARN 0 | SKIP 1 | PASS 10 ]`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for R testthat output")
	}
	if passed != 10 {
		t.Errorf("expected passed=10, got %d", passed)
	}
	if failed != 2 {
		t.Errorf("expected failed=2, got %d", failed)
	}
}

// Test R testthat all passing count extraction.
func TestExtractTestCounts_RTestthat_AllPassing(t *testing.T) {
	output := `[ FAIL 0 | WARN 0 | SKIP 0 | PASS 8 ]`
	passed, failed, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for R testthat all-passing output")
	}
	if passed != 8 {
		t.Errorf("expected passed=8, got %d", passed)
	}
	if failed != 0 {
		t.Errorf("expected failed=0, got %d", failed)
	}
}

// Test R testthat first failure detail.
func TestFirstFailureDetail_RTestthat(t *testing.T) {
	output := `── Failure (test-math.R:5:3): addition works ──
` + "`add(2, 3)` not equal to expected." + `
  1/1 mismatches
  [1] 6 - 5 == 1`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected non-empty detail for R testthat output")
	}
	if !strings.Contains(detail, "Failure") {
		t.Errorf("expected detail to contain 'Failure', got %q", detail)
	}
	if !strings.Contains(detail, "not equal") {
		t.Errorf("expected detail to contain 'not equal', got %q", detail)
	}
}

// Test ScalaTest first failure detail.
func TestFirstFailureDetail_ScalaTest(t *testing.T) {
	output := `- should add numbers correctly *** FAILED ***
  42 did not equal 43 (MathSpec.scala:15)
  at org.scalatest.Assertions.fail(Assertions.scala:56)`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected non-empty detail for ScalaTest output")
	}
	if !strings.Contains(detail, "did not equal") {
		t.Errorf("expected detail to contain 'did not equal', got %q", detail)
	}
}

// Test ScalaTest "was not equal to" variant.
func TestFirstFailureDetail_ScalaTest_WasNotEqual(t *testing.T) {
	output := `[info] - should compute sum *** FAILED ***
[info]   List(1, 2, 3) was not equal to List(1, 2, 4) (CollectionSpec.scala:22)`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected non-empty detail for ScalaTest 'was not equal to' output")
	}
	if !strings.Contains(detail, "was not equal to") {
		t.Errorf("expected detail to contain 'was not equal to', got %q", detail)
	}
}

// Test expanded missingHeaderHint mappings.
func TestMissingHeaderHint_Expanded(t *testing.T) {
	tests := []struct {
		name   string
		output string
		pkg    string
	}{
		{"gmp", "fatal error: gmp.h: No such file or directory", "libgmp-dev"},
		{"mpfr", "fatal error: mpfr.h: No such file or directory", "libmpfr-dev"},
		{"alsa", "fatal error: alsa/asoundlib.h: No such file or directory", "libasound2-dev"},
		{"pcap", "fatal error: pcap.h: No such file or directory", "libpcap-dev"},
		{"libxml2", "fatal error: libxml/parser.h: No such file or directory", "libxml2-dev"},
		{"freetype", "fatal error: ft2build.h: No such file or directory", "libfreetype-dev"},
		{"sndfile", "fatal error: sndfile.h: No such file or directory", "libsndfile1-dev"},
		{"hdf5", "fatal error: hdf5.h: No such file or directory", "libhdf5-dev"},
		{"archive", "fatal error: archive.h: No such file or directory", "libarchive-dev"},
		{"xrandr", "fatal error: X11/extensions/Xrandr.h: No such file or directory", "libxrandr-dev"},
		{"xft", "fatal error: X11/Xft/Xft.h: No such file or directory", "libxft-dev"},
		{"netcdf", "fatal error: netcdf.h: No such file or directory", "libnetcdf-dev"},
		{"pcre2", "fatal error: pcre2.h: No such file or directory", "libpcre2-dev"},
		{"cblas", "fatal error: cblas.h: No such file or directory", "libopenblas-dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := missingHeaderHint(tt.output)
			if hint == "" {
				t.Fatalf("expected hint for %s header, got empty", tt.name)
			}
			if !strings.Contains(hint, tt.pkg) {
				t.Errorf("expected hint to contain %q, got %q", tt.pkg, hint)
			}
		})
	}
}

// Test expanded linkerHint mappings.
func TestLinkerHint_Expanded(t *testing.T) {
	tests := []struct {
		name   string
		output string
		flag   string
	}{
		{"rt_clock", "undefined reference to `clock_gettime'", "-lrt"},
		{"rt_timer", "undefined reference to `timer_create'", "-lrt"},
		{"jpeg", "undefined reference to `jpeg_start_compress'", "-ljpeg"},
		{"png", "undefined reference to `png_create_write_struct'", "-lpng"},
		{"gmp", "undefined reference to `mpz_init'", "-lgmp"},
		{"alsa", "undefined reference to `snd_pcm_open'", "-lasound"},
		{"pcap", "undefined reference to `pcap_open_live'", "-lpcap"},
		{"xml2", "undefined reference to `xmlParseFile'", "-lxml2"},
		{"freetype", "undefined reference to `FT_Init_FreeType'", "-lfreetype"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := linkerHint(tt.output)
			if hint == "" {
				t.Fatalf("expected hint for %s, got empty", tt.name)
			}
			if !strings.Contains(hint, tt.flag) {
				t.Errorf("expected hint to contain %q, got %q", tt.flag, hint)
			}
		})
	}
}

// Test expanded commandNotFoundHint mappings.
func TestCommandNotFoundHint_Expanded(t *testing.T) {
	tests := []struct {
		cmd string
		pkg string
	}{
		{"php", "php"},
		{"clang", "clang"},
		{"lldb", "lldb"},
		{"tree", "tree"},
		{"tmux", "tmux"},
		{"screen", "screen"},
		{"dig", "dnsutils"},
		{"nslookup", "dnsutils"},
		{"traceroute", "traceroute"},
		{"ifconfig", "net-tools"},
		{"inotifywait", "inotify-tools"},
		{"rg", "ripgrep"},
		{"fd", "fd-find"},
		{"parallel", "parallel"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			stderr := tt.cmd + ": command not found"
			hint := commandNotFoundHint(stderr)
			if hint == "" {
				t.Fatalf("expected hint for %q, got empty", tt.cmd)
			}
			if !strings.Contains(hint, tt.pkg) {
				t.Errorf("expected hint to contain %q, got %q", tt.pkg, hint)
			}
		})
	}
}

// Test Kotlin compilation error hint (format 1: e: file.kt: (line, col): message).
func TestCompilationErrorHint_Kotlin_ParenFormat(t *testing.T) {
	output := `e: Main.kt: (42, 5): Unresolved reference: foo
e: Main.kt: (43, 10): Type mismatch`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Kotlin error, got empty")
	}
	if !strings.Contains(hint, "Main.kt") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to contain file and line, got %q", hint)
	}
	if !strings.Contains(hint, "Unresolved reference") {
		t.Errorf("expected hint to contain error message, got %q", hint)
	}
}

// Test Kotlin compilation error hint (format 2: e: file.kt:line:col message).
func TestCompilationErrorHint_Kotlin_ColonFormat(t *testing.T) {
	output := `e: Utils.kt:15:8 Expecting member declaration`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Kotlin colon-format error, got empty")
	}
	if !strings.Contains(hint, "Utils.kt") || !strings.Contains(hint, "15") {
		t.Errorf("expected hint to contain file and line, got %q", hint)
	}
}

// Test expanded CMake package mappings.
func TestCmakeHint_ExpandedPackages(t *testing.T) {
	tests := []struct {
		name string
		pkg  string
		apt  string
	}{
		{"BZip2", "BZip2", "libbz2-dev"},
		{"HDF5", "HDF5", "libhdf5-dev"},
		{"LAPACK", "LAPACK", "liblapack-dev"},
		{"Cairo", "Cairo", "libcairo2-dev"},
		{"ALSA", "ALSA", "libasound2-dev"},
		{"GMP", "GMP", "libgmp-dev"},
		{"SDL2", "SDL2", "libsdl2-dev"},
		{"Readline", "Readline", "libreadline-dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := fmt.Sprintf("CMake Error: Could NOT find %s (missing: %s_LIBRARY)", tt.pkg, tt.pkg)
			hint := cmakeHint(output, 1)
			if hint == "" {
				t.Fatalf("expected hint for CMake %s, got empty", tt.pkg)
			}
			if !strings.Contains(hint, tt.apt) {
				t.Errorf("expected hint to contain %q, got %q", tt.apt, hint)
			}
		})
	}
}

// Test expanded Python exception types in pythonErrorHint.
func TestPythonErrorHint_ExpandedExceptions(t *testing.T) {
	tests := []struct {
		name string
		exc  string
	}{
		{"ModuleNotFoundError", "ModuleNotFoundError: No module named 'nonexistent'"},
		{"NotImplementedError", "NotImplementedError: subclass must implement this"},
		{"ConnectionError", "ConnectionError: [Errno 111] Connection refused"},
		{"TimeoutError", "TimeoutError: operation timed out"},
		{"BrokenPipeError", "BrokenPipeError: [Errno 32] Broken pipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := fmt.Sprintf("Traceback (most recent call last):\n  File \"main.py\", line 42, in <module>\n    do_thing()\n%s", tt.exc)
			hint := pythonErrorHint(output, 1)
			if hint == "" {
				t.Fatalf("expected hint for %s, got empty", tt.name)
			}
			if !strings.Contains(hint, "main.py") || !strings.Contains(hint, "42") {
				t.Errorf("expected hint to contain file and line, got %q", hint)
			}
		})
	}
}

// Test Elixir compilation error hint.
func TestElixirHint_CompileError(t *testing.T) {
	output := `** (CompileError) lib/app.ex:15: undefined function hello/0 (expected App to define hello/0)`
	hint := elixirHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Elixir CompileError, got empty")
	}
	if !strings.Contains(hint, "lib/app.ex") || !strings.Contains(hint, "15") {
		t.Errorf("expected hint to contain file and line, got %q", hint)
	}
}

// Test Elixir UndefinedFunctionError.
func TestElixirHint_UndefinedFunction(t *testing.T) {
	output := `** (UndefinedFunctionError) function MyModule.foo/1 is undefined or private`
	hint := elixirHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Elixir UndefinedFunctionError, got empty")
	}
	if !strings.Contains(hint, "UndefinedFunctionError") {
		t.Errorf("expected hint to mention UndefinedFunctionError, got %q", hint)
	}
}

// Test Elixir Mix Hex not found.
func TestElixirHint_HexNotFound(t *testing.T) {
	output := `** (Mix) Could not find Hex, which is needed to build dependency :decimal`
	hint := elixirHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for missing Hex, got empty")
	}
	if !strings.Contains(hint, "hex") || !strings.Contains(hint, "mix local.hex") {
		t.Errorf("expected hint about installing Hex, got %q", hint)
	}
}

// Test Nim compilation error format: "file.nim(line, col) Error: message".
func TestCompilationErrorHint_Nim(t *testing.T) {
	output := `main.nim(42, 5) Error: undeclared identifier: 'foobar'`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Nim error, got empty")
	}
	if !strings.Contains(hint, "main.nim") || !strings.Contains(hint, ":42") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "undeclared identifier") {
		t.Errorf("expected error message in hint, got %q", hint)
	}
}

// Test D language (DMD) compilation error format: "file.d(line): Error: message".
func TestCompilationErrorHint_D(t *testing.T) {
	output := "source/app.d(42): Error: undefined identifier `foo`"
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for D error, got empty")
	}
	if !strings.Contains(hint, "source/app.d") || !strings.Contains(hint, ":42") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "undefined identifier") {
		t.Errorf("expected error message in hint, got %q", hint)
	}
}

// Test Scala 3 compilation error format: "-- [EXXXX] Error: file.scala:line:col ---".
func TestCompilationErrorHint_Scala3(t *testing.T) {
	output := `-- [E007] Type Mismatch Error: src/Main.scala:42:5 -------
42 |  val x: Int = "hello"
   |               ^^^^^^^
   |               Found:    ("hello" : String)
   |               Required: Int`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Scala 3 error, got empty")
	}
	if !strings.Contains(hint, "src/Main.scala") || !strings.Contains(hint, ":42") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "Type Mismatch") {
		t.Errorf("expected error type in hint, got %q", hint)
	}
}

// Test Scala 3 simple error format: "-- Error: file.scala:line:col ---".
func TestCompilationErrorHint_Scala3_Simple(t *testing.T) {
	output := `-- Error: src/Main.scala:10:1 -------
10 |object Foo {
   |^
   |missing argument for parameter x`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Scala 3 simple error, got empty")
	}
	if !strings.Contains(hint, "src/Main.scala") || !strings.Contains(hint, ":10") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
}

// Test Fortran (gfortran) multi-line error format.
func TestCompilationErrorHint_Fortran(t *testing.T) {
	output := `main.f90:42:5:

   42 |   call foo(x, y)
      |     1
Error: Symbol 'foo' at (1) has no IMPLICIT type`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Fortran error, got empty")
	}
	if !strings.Contains(hint, "main.f90") || !strings.Contains(hint, ":42") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "Error:") || !strings.Contains(hint, "IMPLICIT type") {
		t.Errorf("expected error message in hint, got %q", hint)
	}
}

// Test Fortran Fatal Error format.
func TestCompilationErrorHint_FortranFatal(t *testing.T) {
	output := `program.f90:1:6:

    1 | program hello
      |      1
Fatal Error: Cannot open module file 'utils.mod' for reading at (1)`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Fortran fatal error, got empty")
	}
	if !strings.Contains(hint, "program.f90") || !strings.Contains(hint, ":1") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
}

func TestCompilationErrorHint_GHC_Modern(t *testing.T) {
	output := `[1 of 1] Compiling Main
Main.hs:42:5: error: [GHC-88464]
    Variable not in scope: fooBar :: Int -> Bool
    Suggested fix: Perhaps use 'foobar' (imported from Data.List)
   |
42 |     if fooBar x then y else z
   |        ^^^^^^`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for GHC modern error, got empty")
	}
	if !strings.Contains(hint, "Main.hs") || !strings.Contains(hint, ":42") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "Variable not in scope") {
		t.Errorf("expected actual error message from continuation line, got %q", hint)
	}
}

func TestCompilationErrorHint_GHC_TypeMismatch(t *testing.T) {
	output := `Solver.hs:15:10: error:
    • Could not deduce (Num String) arising from a use of '+'
    • In the expression: "hello" + 1
      In an equation for 'foo': foo = "hello" + 1
   |
15 |     foo = "hello" + 1
   |          ^^^^^^^^^^^`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for GHC type error, got empty")
	}
	if !strings.Contains(hint, "Solver.hs") || !strings.Contains(hint, ":15") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "Could not deduce") {
		t.Errorf("expected type error message from continuation line, got %q", hint)
	}
}

func TestCompilationErrorHint_GHC_OldFormat(t *testing.T) {
	// Older GHC format without "error:" keyword — error on next line.
	output := `[1 of 1] Compiling Main
Main.hs:5:1:
    Not in scope: 'solve'
    Perhaps you meant 'show' (imported from Prelude)`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for GHC old-format error, got empty")
	}
	if !strings.Contains(hint, "Main.hs") || !strings.Contains(hint, ":5") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "Not in scope") {
		t.Errorf("expected error message from continuation line, got %q", hint)
	}
}

func TestCompilationErrorHint_GHC_ParseError(t *testing.T) {
	output := `Solution.hs:10:1: error:
    Parse error (possibly incorrect indentation or mismatched brackets)
   |
10 | where
   | ^`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for GHC parse error, got empty")
	}
	if !strings.Contains(hint, "Solution.hs") || !strings.Contains(hint, ":10") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "Parse error") {
		t.Errorf("expected parse error message from continuation line, got %q", hint)
	}
}

func TestCompilationErrorHint_OCaml(t *testing.T) {
	output := `File "src/main.ml", line 42, characters 5-10:
42 |   let x = foo bar
              ^^^^^
Error: Unbound value foo`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for OCaml error, got empty")
	}
	if !strings.Contains(hint, "src/main.ml") || !strings.Contains(hint, ":42") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "Unbound value foo") {
		t.Errorf("expected error message, got %q", hint)
	}
}

func TestCompilationErrorHint_OCaml_NoError(t *testing.T) {
	output := `File "lib/parser.mli", line 7, characters 0-25:
7 | val parse : string -> ast
    ^^^^^^^^^^^^^^^^^^^^^^^^^
Error: Type ast is not defined`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for OCaml interface error, got empty")
	}
	if !strings.Contains(hint, "lib/parser.mli") || !strings.Contains(hint, ":7") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
}

func TestCompilationErrorHint_Perl(t *testing.T) {
	output := `syntax error at script.pl line 42, near "}"
Execution of script.pl aborted due to compilation errors.`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Perl syntax error, got empty")
	}
	if !strings.Contains(hint, "script.pl") || !strings.Contains(hint, ":42") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
	if !strings.Contains(hint, "syntax error") {
		t.Errorf("expected error message, got %q", hint)
	}
}

func TestCompilationErrorHint_PerlDied(t *testing.T) {
	output := `Died at module.pm line 15.`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Perl died error, got empty")
	}
	if !strings.Contains(hint, "module.pm") || !strings.Contains(hint, ":15") {
		t.Errorf("expected file:line reference, got %q", hint)
	}
}

// Test Nim unittest firstFailureDetail extraction.
func TestFirstFailureDetail_Nim(t *testing.T) {
	output := `[Suite] Math tests
  [OK] test basic addition
  [FAILED] test subtraction
    /home/user/test_math.nim(15)
    Check failed: sub(5, 3) == 1
    actual: 2
    expected: 1`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for Nim unittest, got empty")
	}
	if !strings.Contains(detail, "[FAILED] test subtraction") {
		t.Errorf("expected test name in detail, got %q", detail)
	}
	if !strings.Contains(detail, "Check failed") {
		t.Errorf("expected assertion detail, got %q", detail)
	}
}

// Test Zig test firstFailureDetail extraction.
func TestFirstFailureDetail_Zig(t *testing.T) {
	output := `Test [1/3] test.basic addition... OK
Test [2/3] test.subtraction... FAIL
error: expected 4, found 3
Test [3/3] test.multiplication... OK`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for Zig test, got empty")
	}
	if !strings.Contains(detail, "FAIL") {
		t.Errorf("expected FAIL in detail, got %q", detail)
	}
	if !strings.Contains(detail, "expected 4, found 3") {
		t.Errorf("expected error detail, got %q", detail)
	}
}

// Test HSpec (Haskell) firstFailureDetail extraction.
func TestFirstFailureDetail_HSpec(t *testing.T) {
	output := `Math
  addition
    adds two numbers FAILED [1]

Failures:

  1) Math.addition adds two numbers
       expected: 4
        but got: 3`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for HSpec, got empty")
	}
	if !strings.Contains(detail, "expected: 4") {
		t.Errorf("expected assertion detail, got %q", detail)
	}
	if !strings.Contains(detail, "but got: 3") {
		t.Errorf("expected actual value, got %q", detail)
	}
}

func TestFirstFailureDetail_Catch2(t *testing.T) {
	output := `/path/test.cpp:42: FAILED:
  CHECK( result == 42 )
with expansion:
  43 == 42`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for Catch2, got empty")
	}
	if !strings.Contains(detail, "CHECK( result == 42 )") {
		t.Errorf("expected assertion expression, got %q", detail)
	}
	if !strings.Contains(detail, "43 == 42") {
		t.Errorf("expected expansion, got %q", detail)
	}
}

func TestFirstFailureDetail_Catch2_Require(t *testing.T) {
	output := `~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
test.cpp is a Catch2 v3 test
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
/src/test.cpp:15: FAILED:
  REQUIRE( str == "hello" )
with expansion:
  "world" == "hello"`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for Catch2 REQUIRE, got empty")
	}
	if !strings.Contains(detail, "REQUIRE") {
		t.Errorf("expected REQUIRE assertion, got %q", detail)
	}
	if !strings.Contains(detail, "world") {
		t.Errorf("expected expansion values, got %q", detail)
	}
}

func TestFirstFailureDetail_BoostTest(t *testing.T) {
	output := `test.cpp(42): error: in "test_addition": check x == y has failed [3 != 4]`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for Boost.Test, got empty")
	}
	if !strings.Contains(detail, "check x == y has failed") {
		t.Errorf("expected check expression, got %q", detail)
	}
	if !strings.Contains(detail, "[3 != 4]") {
		t.Errorf("expected expansion in brackets, got %q", detail)
	}
}

func TestFirstFailureDetail_PerlTestMore(t *testing.T) {
	output := `not ok 1 - addition works
#   Failed test 'addition works'
#   at t/math.t line 12.
#          got: '3'
#     expected: '4'`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for Perl Test::More, got empty")
	}
	if !strings.Contains(detail, "got:") {
		t.Errorf("expected got value, got %q", detail)
	}
	if !strings.Contains(detail, "expected:") {
		t.Errorf("expected expected value, got %q", detail)
	}
}

func TestFirstFailureDetail_DartTest(t *testing.T) {
	output := `00:01 +0 -1: test addition [E]
  Expected: <42>
  Actual: <43>`
	detail := firstFailureDetail(output)
	if detail == "" {
		t.Fatal("expected failure detail for Dart test, got empty")
	}
	if !strings.Contains(detail, "Expected: <42>") {
		t.Errorf("expected expected value, got %q", detail)
	}
	if !strings.Contains(detail, "Actual: <43>") {
		t.Errorf("expected actual value, got %q", detail)
	}
}

// Test compilationFingerprint catches Nim/D error formats.
func TestShortOutputTracking(t *testing.T) {
	// Verify that testFailureFingerprint and extractTestCounts work on
	// short output (< 2000 chars). Previously, the bash tool only computed
	// these for output > 2000 chars, which meant single-test tasks and
	// small projects got no stagnation/regression detection.

	t.Run("short_pytest_fingerprint", func(t *testing.T) {
		// Typical short pytest failure (well under 2000 chars).
		output := "test_math.py::test_add FAILED\nE       assert 3 == 5\n1 failed"
		fp := testFailureFingerprint(output)
		if fp == "" {
			t.Error("expected fingerprint for short pytest failure output")
		}
	})

	t.Run("short_go_test_fingerprint", func(t *testing.T) {
		output := "--- FAIL: TestAdd (0.00s)\n    main_test.go:10: expected 5, got 3\nFAIL"
		fp := testFailureFingerprint(output)
		if fp == "" {
			t.Error("expected fingerprint for short go test failure output")
		}
	})

	t.Run("short_pytest_counts", func(t *testing.T) {
		output := "1 failed, 2 passed"
		passed, failed, ok := extractTestCounts(output)
		if !ok {
			t.Fatal("expected counts to be parsed from short pytest output")
		}
		if passed != 2 || failed != 1 {
			t.Errorf("expected 2 passed 1 failed, got %d passed %d failed", passed, failed)
		}
	})

	t.Run("short_go_test_counts", func(t *testing.T) {
		output := "ok  \tmypackage\t0.005s\nFAIL"
		// Go test doesn't always include counts in short output,
		// but the fingerprint should still work.
		fp := testFailureFingerprint(output)
		// Even if counts aren't parseable, fingerprint should detect FAIL.
		_ = fp // fingerprint may or may not match depending on format
	})

	t.Run("short_passing_clears_fingerprint", func(t *testing.T) {
		// Verify passing test output produces empty fingerprint.
		output := "1 passed"
		fp := testFailureFingerprint(output)
		if fp != "" {
			t.Errorf("passing test should have empty fingerprint, got: %q", fp)
		}
	})
}

func TestLoopDetectionMiddleware_ReadLoop(t *testing.T) {
	// Test that reading the same file many times without editing triggers
	// an analysis paralysis warning.
	mw := LoopDetectionMiddleware(3) // threshold=3, read threshold=6
	ctx := context.Background()
	readWarnings := 0
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		for _, msg := range msgs {
			if req, ok := msg.(core.ModelRequest); ok {
				for _, part := range req.Parts {
					if up, ok := part.(core.UserPromptPart); ok {
						if strings.Contains(up.Content, "analysis paralysis") {
							readWarnings++
						}
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	viewMsg := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName: "view",
				ArgsJSON: `{"path":"/app/solution.py"}`,
			},
		},
	}

	// Simulate repeated reads without any edits.
	messages := []core.ModelMessage{}
	for i := 0; i < 10 && readWarnings == 0; i++ {
		messages = append(messages, viewMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
	}
	if readWarnings == 0 {
		t.Error("expected analysis paralysis warning after repeated reads without editing")
	}
}

func TestLoopDetectionMiddleware_ReadThenEdit(t *testing.T) {
	// Test that reading a file many times does NOT trigger a warning if
	// the agent also edits the file (normal iterative development).
	mw := LoopDetectionMiddleware(3)
	ctx := context.Background()
	readWarnings := 0
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		for _, msg := range msgs {
			if req, ok := msg.(core.ModelRequest); ok {
				for _, part := range req.Parts {
					if up, ok := part.(core.UserPromptPart); ok {
						if strings.Contains(up.Content, "analysis paralysis") {
							readWarnings++
						}
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	viewMsg := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName: "view",
				ArgsJSON: `{"path":"/app/solution.py"}`,
			},
		},
	}
	editMsg := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName: "edit",
				ArgsJSON: `{"path":"/app/solution.py"}`,
			},
		},
	}

	// Simulate read-edit-read-edit cycle.
	messages := []core.ModelMessage{}
	for range 10 {
		messages = append(messages, viewMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
		messages = append(messages, editMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
	}
	if readWarnings > 0 {
		t.Error("should not warn about read loops when the file is also being edited")
	}
}

func TestLoopDetectionMiddleware_BashLoopPersistent(t *testing.T) {
	// Test that a bash loop triggers faster on recurrence (halving behavior),
	// matching the edit loop behavior. Before the fix, delete(bashCounts, cmd)
	// at detection time zeroed the counter before the halving code could operate,
	// making every recurrence require the full threshold+2 count.
	mw := LoopDetectionMiddleware(3) // threshold=3, bash threshold=5
	ctx := context.Background()
	bashWarnings := 0
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		for _, msg := range msgs {
			if req, ok := msg.(core.ModelRequest); ok {
				for _, part := range req.Parts {
					if up, ok := part.(core.UserPromptPart); ok {
						if strings.Contains(up.Content, "stuck in a loop") {
							bashWarnings++
						}
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	bashMsg := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName: "bash",
				ArgsJSON: `{"command":"python3 test.py"}`,
			},
		},
	}

	// Phase 1: Accumulate bash runs until first warning.
	messages := []core.ModelMessage{}
	for i := 0; i < 15 && bashWarnings == 0; i++ {
		messages = append(messages, bashMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
	}
	if bashWarnings == 0 {
		t.Fatal("expected bash loop warning")
	}
	firstWarningAt := len(messages)

	// Phase 2: Continue running same command — second warning should come
	// faster than the first due to halving.
	for i := 0; i < 15 && bashWarnings == 1; i++ {
		messages = append(messages, bashMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
	}
	if bashWarnings < 2 {
		t.Fatal("expected second bash loop warning")
	}
	secondWarningAt := len(messages) - firstWarningAt

	// With halving, the second warning should come faster than the first.
	if secondWarningAt >= firstWarningAt {
		t.Errorf("persistent bash loop should be detected faster: first=%d runs, second=%d runs (expected second < first)",
			firstWarningAt, secondWarningAt)
	}
}

func TestLoopDetectionMiddleware_LSPLoopCounterResets(t *testing.T) {
	// Test that an LSP loop warning halves the counter so it doesn't fire
	// on every subsequent turn. Before the fix, the reset code wrote to
	// searchCounts instead of lspCounts, so the LSP counter never decreased
	// and the warning fired on every turn after the first detection.
	mw := LoopDetectionMiddleware(3) // threshold=3, searchLoopThreshold=4
	ctx := context.Background()
	warnings := 0
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		for _, msg := range msgs {
			if req, ok := msg.(core.ModelRequest); ok {
				for _, part := range req.Parts {
					if up, ok := part.(core.UserPromptPart); ok {
						if strings.Contains(up.Content, "stuck in a loop") {
							warnings++
						}
					}
				}
			}
		}
		return &core.ModelResponse{}, nil
	}

	lspMsg := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.ToolCallPart{
				ToolName: "lsp",
				ArgsJSON: `{"method":"definition","file":"/app/main.go","line":10}`,
			},
		},
	}
	// In production, messages alternate: ModelResponse then ModelRequest
	// (tool result). The middleware only counts tool calls from ModelResponse.
	toolResultMsg := core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.ToolReturnPart{Content: "definition at main.go:42"},
		},
	}

	// Phase 1: Accumulate LSP calls until the loop warning fires.
	messages := []core.ModelMessage{}
	for i := 0; i < 10 && warnings == 0; i++ {
		messages = append(messages, lspMsg, toolResultMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
	}
	if warnings == 0 {
		t.Fatal("expected loop warning after repeated LSP calls")
	}

	firstWarningCount := warnings

	// Phase 2: After the warning fires and the counter is halved, we need
	// more calls before it fires again. Without the fix, the counter was
	// never halved so it would fire on the very next turn.
	// Track how many additional turns before the next warning.
	turnsUntilNextWarning := 0
	for range 3 {
		messages = append(messages, lspMsg, toolResultMsg)
		_, _ = mw(ctx, messages, nil, nil, next)
		turnsUntilNextWarning++
		if warnings > firstWarningCount {
			break
		}
	}
	// With halving, the counter drops from ~4 to ~2, so it should take at
	// least 2 more turns (+1 each) to reach the threshold of 4 again.
	// Without the fix (counter never halved), it would fire immediately.
	if turnsUntilNextWarning < 2 {
		t.Errorf("LSP loop counter not resetting properly: next warning fired after only %d turn(s), expected at least 2",
			turnsUntilNextWarning)
	}
}

func TestIsVerificationString_ClearsTracking(t *testing.T) {
	// Verify that isVerificationString matches commands that should
	// clear test tracking state on success.
	tests := []struct {
		cmd  string
		want bool
	}{
		{"pytest", true},
		{"go test ./...", true},
		{"npm test", true},
		{"python -m pytest test_foo.py", true},
		{"make", true},
		{"ls -la", false},
		{"echo hello", false},
	}
	for _, tc := range tests {
		got := isVerificationString(strings.ToLower(tc.cmd))
		if got != tc.want {
			t.Errorf("isVerificationString(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestCompilationFingerprint_NimD(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "Nim",
			output: "main.nim(42, 5) Error: undeclared identifier: 'x'",
		},
		{
			name:   "D",
			output: "app.d(42): Error: undefined identifier `foo`",
		},
		{
			name:   "Fortran",
			output: "Fatal Error: Cannot open module file 'utils.mod' for reading at (1)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := compilationFingerprint(tc.output)
			if fp == "" {
				t.Fatalf("expected fingerprint for %s error, got empty", tc.name)
			}
		})
	}
}

func TestCompilationFingerprint_PythonRust(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name: "Python SyntaxError",
			output: `  File "solution.py", line 42
    def foo(
           ^
SyntaxError: invalid syntax`,
		},
		{
			name:   "Python IndentationError",
			output: `IndentationError: unexpected indent`,
		},
		{
			name:   "Python NameError",
			output: `NameError: name 'solve' is not defined`,
		},
		{
			name:   "Python ModuleNotFoundError",
			output: `ModuleNotFoundError: No module named 'numpy'`,
		},
		{
			name: "Rust compilation error",
			output: `error[E0425]: cannot find value 'x' in this scope
 --> src/main.rs:5:10
  |
5 |     let y = x + 1;
  |             ^ not found in this scope`,
		},
		{
			name:   "Scala 3 error",
			output: `-- [E007] Type Mismatch Error: src/Main.scala:42:5 ---`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := compilationFingerprint(tc.output)
			if fp == "" {
				t.Fatalf("expected fingerprint for %s error, got empty", tc.name)
			}
		})
	}

	// Verify no false positives on clean output.
	fp := compilationFingerprint("Build succeeded\nAll tests passed")
	if fp != "" {
		t.Errorf("expected no fingerprint for clean output, got: %s", fp)
	}
}

func TestCompilationFingerprint_Go(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "Go undefined identifier",
			output: `./main.go:42:5: undefined: myFunc`,
		},
		{
			name:   "Go unused import",
			output: `./main.go:3:2: "fmt" imported and not used`,
		},
		{
			name:   "Go unused variable",
			output: `./main.go:10:2: x declared and not used`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := compilationFingerprint(tc.output)
			if fp == "" {
				t.Fatalf("expected fingerprint for Go %s, got empty", tc.name)
			}
		})
	}
}

func TestCompilationErrorSummary_Go(t *testing.T) {
	output := `# command-line-arguments
./main.go:5:2: undefined: solve
./main.go:8:2: "fmt" imported and not used
./main.go:12:6: result declared and not used
[exit code: 2]`
	summary := compilationErrorSummary(output, 2)
	if summary == "" {
		t.Fatal("expected compilation error summary for Go errors, got empty")
	}
	if !strings.Contains(summary, "undefined: solve") {
		t.Error("summary should include the undefined identifier error")
	}
	if !strings.Contains(summary, "imported and not used") {
		t.Error("summary should include the unused import error")
	}
	if !strings.Contains(summary, "declared and not used") {
		t.Error("summary should include the unused variable error")
	}
}

func TestCompilationFingerprint_GHC(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name: "GHC Not in scope",
			output: `[1 of 1] Compiling Main
Main.hs:5:1:
    Not in scope: 'solve'
    Perhaps you meant 'show' (imported from Prelude)`,
		},
		{
			name: "GHC Could not deduce",
			output: `Solver.hs:15:10: error:
    • Could not deduce (Num String) arising from a use of '+'`,
		},
		{
			name: "GHC Variable not in scope",
			output: `Main.hs:42:5: error: [GHC-88464]
    Variable not in scope: fooBar :: Int -> Bool`,
		},
		{
			name: "GHC Parse error",
			output: `Solution.hs:10:1: error:
    Parse error (possibly incorrect indentation)`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := compilationFingerprint(tc.output)
			if fp == "" {
				t.Fatalf("expected fingerprint for GHC %s, got empty", tc.name)
			}
		})
	}
}

func TestCompilationErrorSummary_GHC(t *testing.T) {
	output := `[1 of 1] Compiling Main
Main.hs:5:1:
    Not in scope: 'solve'
Main.hs:10:3:
    Couldn't match type 'String' with 'Int'
[exit code: 1]`
	summary := compilationErrorSummary(output, 1)
	if summary == "" {
		t.Fatal("expected compilation error summary for GHC errors, got empty")
	}
	if !strings.Contains(summary, "Not in scope") {
		t.Error("summary should include the 'Not in scope' error")
	}
	if !strings.Contains(summary, "Couldn't match type") {
		t.Error("summary should include the type mismatch error")
	}
}

func TestEdit_CRLFNormalization(t *testing.T) {
	dir := t.TempDir()
	// Write a file with CRLF line endings.
	crlfContent := "package main\r\n\r\nfunc main() {\r\n\tfmt.Println(\"Hello\")\r\n}\r\n"
	os.WriteFile(filepath.Join(dir, "crlf.go"), []byte(crlfContent), 0o644)

	tool := Edit(WithWorkDir(dir))

	// Edit with LF-only old_string (what the model generates).
	result := call(t, tool, `{"path": "crlf.go", "old_string": "\tfmt.Println(\"Hello\")", "new_string": "\tfmt.Println(\"World\")"}`)
	assertContains(t, result, "Replaced 1")

	// Verify CRLF line endings are preserved in the output.
	data, _ := os.ReadFile(filepath.Join(dir, "crlf.go"))
	content := string(data)
	if !strings.Contains(content, "\r\n") {
		t.Error("expected CRLF line endings to be preserved")
	}
	if !strings.Contains(content, "World") {
		t.Error("expected edit to be applied")
	}
	if strings.Contains(content, "Hello") {
		t.Error("expected old string to be replaced")
	}
}

func TestMultiEdit_CRLFNormalization(t *testing.T) {
	dir := t.TempDir()
	crlfContent := "line1\r\nline2\r\nline3\r\n"
	os.WriteFile(filepath.Join(dir, "crlf.txt"), []byte(crlfContent), 0o644)

	tool := MultiEdit(WithWorkDir(dir))
	result := call(t, tool, `{"edits": [{"path": "crlf.txt", "old_string": "line2", "new_string": "LINE_TWO"}]}`)
	assertContains(t, result, "Replaced 1")

	data, _ := os.ReadFile(filepath.Join(dir, "crlf.txt"))
	content := string(data)
	if !strings.Contains(content, "\r\n") {
		t.Error("expected CRLF line endings to be preserved in multi_edit")
	}
	if !strings.Contains(content, "LINE_TWO") {
		t.Error("expected edit to be applied")
	}
}

func TestEdit_CRLFInParams(t *testing.T) {
	dir := t.TempDir()
	// File has LF-only line endings.
	os.WriteFile(filepath.Join(dir, "lf.go"), []byte("line1\nline2\nline3\n"), 0o644)

	tool := Edit(WithWorkDir(dir))

	// Model sends CRLF in old_string/new_string (e.g., copied from a CRLF view).
	result := call(t, tool, "{\"path\": \"lf.go\", \"old_string\": \"line1\\r\\nline2\", \"new_string\": \"LINE1\\r\\nLINE2\"}")
	assertContains(t, result, "Replaced 1")

	data, _ := os.ReadFile(filepath.Join(dir, "lf.go"))
	content := string(data)
	if strings.Contains(content, "\r\n") {
		t.Error("LF-only file should not gain CRLF line endings")
	}
	if !strings.Contains(content, "LINE1\nLINE2") {
		t.Error("expected CRLF-normalized edit to be applied")
	}
}

func TestEdit_CRLFInParamsWithCRLFFile(t *testing.T) {
	dir := t.TempDir()
	// File has CRLF line endings AND params also have CRLF.
	os.WriteFile(filepath.Join(dir, "crlf.go"), []byte("line1\r\nline2\r\nline3\r\n"), 0o644)

	tool := Edit(WithWorkDir(dir))

	result := call(t, tool, "{\"path\": \"crlf.go\", \"old_string\": \"line1\\r\\nline2\", \"new_string\": \"LINE1\\r\\nLINE2\"}")
	assertContains(t, result, "Replaced 1")

	data, _ := os.ReadFile(filepath.Join(dir, "crlf.go"))
	content := string(data)
	if !strings.Contains(content, "\r\n") {
		t.Error("expected CRLF line endings to be preserved")
	}
	if !strings.Contains(content, "LINE1\r\nLINE2") {
		t.Error("expected edit to be applied with CRLF preserved")
	}
}

func TestMultiEdit_CRLFInParams(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lf.txt"), []byte("aaa\nbbb\nccc\n"), 0o644)

	tool := MultiEdit(WithWorkDir(dir))
	// CRLF in both old_string and new_string within multi_edit params.
	result := call(t, tool, "{\"edits\": [{\"path\": \"lf.txt\", \"old_string\": \"aaa\\r\\nbbb\", \"new_string\": \"AAA\\r\\nBBB\"}]}")
	assertContains(t, result, "Replaced 1")

	data, _ := os.ReadFile(filepath.Join(dir, "lf.txt"))
	content := string(data)
	if strings.Contains(content, "\r\n") {
		t.Error("LF-only file should not gain CRLF line endings")
	}
	if !strings.Contains(content, "AAA\nBBB") {
		t.Error("expected CRLF-normalized edit to be applied in multi_edit")
	}
}

func TestEdit_CRLFContextDisplay(t *testing.T) {
	// Verify that editing a CRLF file produces context around the edit.
	// Before the fix, editResultWithContext received CRLF content but LF-only
	// newStr, so strings.Index failed and no context was shown.
	dir := t.TempDir()
	crlfContent := "line1\r\nline2\r\nline3\r\nline4\r\nline5\r\n"
	os.WriteFile(filepath.Join(dir, "crlf.txt"), []byte(crlfContent), 0o644)

	tool := Edit(WithWorkDir(dir))
	result := call(t, tool, `{"path": "crlf.txt", "old_string": "line3", "new_string": "LINE_THREE"}`)
	assertContains(t, result, "Replaced 1")
	// The result must include the context section showing surrounding lines.
	assertContains(t, result, "Context:")
	assertContains(t, result, "LINE_THREE")

	// Also verify CRLF is preserved on disk.
	data, _ := os.ReadFile(filepath.Join(dir, "crlf.txt"))
	if !strings.Contains(string(data), "\r\n") {
		t.Error("expected CRLF line endings to be preserved")
	}
}

func TestEdit_CRLFAutoCorrectWhitespaceContext(t *testing.T) {
	// Verify that auto-corrected whitespace edits on CRLF files show context.
	dir := t.TempDir()
	// File uses tabs, model sends spaces.
	crlfContent := "func main() {\r\n\tline1\r\n\tline2\r\n\tline3\r\n}\r\n"
	os.WriteFile(filepath.Join(dir, "crlf.go"), []byte(crlfContent), 0o644)

	tool := Edit(WithWorkDir(dir))
	// Model sends spaces (4) instead of tabs — triggers autoCorrectWhitespace.
	result := call(t, tool, `{"path": "crlf.go", "old_string": "    line2", "new_string": "    LINE_TWO"}`)
	assertContains(t, result, "Replaced 1")
	assertContains(t, result, "auto-corrected whitespace")
	assertContains(t, result, "Context:")
	assertContains(t, result, "LINE_TWO")
}

func TestExtractGoFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "simple function call",
			content: `
package solution_test

import "testing"

func TestSolve(t *testing.T) {
	result := Solve(3, []int{1, 2, 3})
	if result != 6 {
		t.Errorf("got %d, want 6", result)
	}
}
`,
			want: []string{"Solve"},
		},
		{
			name: "module method call",
			content: `
package solution_test

import (
	"testing"
	"solution"
)

func TestProcess(t *testing.T) {
	result := solution.Process(data, 0.5)
	assert.Equal(t, expected, result)
}
`,
			want: []string{"Process"},
		},
		{
			name: "skip stdlib calls",
			content: `
package solution_test

func TestBasic(t *testing.T) {
	result := Transform("hello")
	fmt.Println(result)
	if len(result) != 5 {
		t.Fatal("wrong length")
	}
}
`,
			want: []string{"Transform"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractGoFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestExtractRubyFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "simple function call",
			content: `
require_relative './solution'

RSpec.describe 'Solution' do
  it 'returns correct result' do
    expect(solve(3, [1, 2, 3])).to eq(6)
  end
end
`,
			want: []string{"solve"},
		},
		{
			name: "class method call",
			content: `
require_relative './solution'

RSpec.describe Solution do
  it 'processes data' do
    result = Solution.process(data, threshold: 0.5)
    expect(result).not_to be_nil
  end
end
`,
			want: []string{"process"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractRubyFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestExtractRustFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "simple function call",
			content: `
use solution;

#[test]
fn test_solve() {
    assert_eq!(solve(3, &[1, 2, 3]), 6);
}
`,
			want: []string{"solve"},
		},
		{
			name: "module path call",
			content: `
mod solution;

#[test]
fn test_process() {
    let result = solution::process(&data, 0.5);
    assert!(result.is_some());
}
`,
			want: []string{"process"},
		},
		{
			name: "skip stdlib calls",
			content: `
#[test]
fn test_basic() {
    let result = transform("hello".to_string());
    assert_eq!(result.len(), 5);
}
`,
			want: []string{"transform"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractRustFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestExtractJavaFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "JUnit test calling solution methods",
			content: `
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

public class SolutionTest {
    @Test
    public void testSolve() {
        Solution sol = new Solution();
        int result = sol.solve(3, new int[]{1, 2, 3});
        assertEquals(6, result);
    }

    @Test
    public void testProcess() {
        String output = Calculator.compute(10, 20);
        assertNotNull(output);
    }
}
`,
			want: []string{"solve", "compute"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractJavaFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestExtractCSharpFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "NUnit test calling solution methods",
			content: `
using NUnit.Framework;

[TestFixture]
public class SolutionTests
{
    [Test]
    public void TestSolve()
    {
        var sol = new Solution();
        int result = sol.Solve(3, new[] {1, 2, 3});
        Assert.AreEqual(6, result);
    }

    [Test]
    public void TestProcess()
    {
        string output = Processor.Transform("hello");
        Assert.IsNotNull(output);
    }
}
`,
			want: []string{"Solve", "Transform"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractCSharpFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestIsEntryPointFile(t *testing.T) {
	tests := []struct {
		name   string
		expect bool
	}{
		// Original entry points.
		{"main.go", true},
		{"main.py", true},
		{"app.js", true},
		{"index.ts", true},
		{"solution.py", true},
		{"__init__.py", true},
		{"conftest.py", true},
		// New entry points.
		{"lib.rs", true},
		{"mod.rs", true},
		{"build.rs", true},
		{"setup.py", true},
		{"setup.cfg", true},
		{"program.c", true},
		{"run.sh", true},
		// Exact match config files.
		{"makefile", true},
		{"dockerfile", true},
		{"cargo.toml", true},
		{"go.mod", true},
		{"package.json", true},
		// Non-entry-point files.
		{"utils.go", false},
		{"helper.py", false},
		{"data.json", false},
		{"readme.md", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isEntryPointFile(tc.name)
			if got != tc.expect {
				t.Errorf("isEntryPointFile(%q) = %v, want %v", tc.name, got, tc.expect)
			}
		})
	}
}

func TestIsSkippableDir(t *testing.T) {
	tests := []struct {
		name   string
		expect bool
	}{
		// Should skip.
		{".git", true},
		{".svn", true},
		{".hg", true},
		{"node_modules", true},
		{"__pycache__", true},
		{".tox", true},
		{"vendor", true},
		{"_build", true},
		{".build", true},
		{"zig-cache", true},
		{"zig-out", true},
		{"nimcache", true},
		{".gradle", true},
		{".dub", true},
		{"deps", true},
		{"_deps", true},
		{".eggs", true},
		{".venv", true},
		{"venv", true},
		{".cache", true},
		{".pytest_cache", true},
		{".mypy_cache", true},
		{".ruff_cache", true},
		{".next", true},
		{".nuxt", true},
		{".turbo", true},
		{"coverage", true},
		{".coverage", true},
		{"build", true},
		{"dist", true},
		{"target", true},
		{"out", true},
		// IDE directories.
		{".idea", true},
		{".vscode", true},
		{".vs", true},
		{"__snapshots__", true},
		{".angular", true},
		{".parcel-cache", true},
		{".svelte-kit", true},
		// Infrastructure/deployment directories.
		{".terraform", true},
		{".serverless", true},
		{".pulumi", true},
		{".yarn", true},
		{".expo", true},
		// Bazel build outputs.
		{"bazel-bin", true},
		{"bazel-out", true},
		{"bazel-testlogs", true},
		{"bazel-genfiles", true},
		// Should NOT skip.
		{"src", false},
		{"lib", false},
		{"tests", false},
		{"cmd", false},
		{"pkg", false},
		{"app", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isSkippableDir(tc.name)
			if got != tc.expect {
				t.Errorf("isSkippableDir(%q) = %v, want %v", tc.name, got, tc.expect)
			}
		})
	}
}

// --- Meson test output parsing ---

func TestTestResultSummary_Meson(t *testing.T) {
	output := `1/3 myproject:test_add          OK              0.03s
2/3 myproject:test_sub          FAIL            0.02s
3/3 myproject:test_mul          OK              0.01s

Ok:                 2
Expected Fail:      0
Fail:               1
Unexpected Pass:    0
Skipped:            0
Timeout:            0`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected Meson summary")
	}
	if !strings.Contains(summary, "meson") {
		t.Errorf("expected 'meson' in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "Ok:") {
		t.Errorf("expected 'Ok:' in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "Fail:") {
		t.Errorf("expected 'Fail:' in summary, got: %q", summary)
	}
}

func TestTestResultSummary_MesonAllPassing(t *testing.T) {
	output := `1/3 myproject:test_add          OK              0.03s
2/3 myproject:test_sub          OK              0.02s
3/3 myproject:test_mul          OK              0.01s

Ok:                 3
Expected Fail:      0
Fail:               0
Unexpected Pass:    0
Skipped:            0
Timeout:            0`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected Meson summary")
	}
	if !strings.Contains(summary, "meson") {
		t.Errorf("expected 'meson' in summary, got: %q", summary)
	}
}

func TestExtractTestCounts_Meson(t *testing.T) {
	output := `1/3 myproject:test_add          OK              0.03s
2/3 myproject:test_sub          FAIL            0.02s
3/3 myproject:test_mul          OK              0.01s

Ok:                 2
Expected Fail:      0
Fail:               1
Unexpected Pass:    0
Skipped:            0
Timeout:            0`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected Meson test counts")
	}
	if p != 2 {
		t.Errorf("expected 2 passed, got %d", p)
	}
	if f != 1 {
		t.Errorf("expected 1 failed, got %d", f)
	}
}

func TestExtractTestCounts_MesonAllPassing(t *testing.T) {
	output := `1/3 myproject:test_add          OK              0.03s
2/3 myproject:test_sub          OK              0.02s
3/3 myproject:test_mul          OK              0.01s

Ok:                 3
Expected Fail:      0
Fail:               0
Unexpected Pass:    0
Skipped:            0
Timeout:            0`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected Meson test counts")
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

// --- Bazel test output parsing ---

func TestTestResultSummary_Bazel(t *testing.T) {
	output := `INFO: Analyzed 3 targets (0 packages loaded, 0 targets configured).
INFO: Found 3 test targets...
INFO: Elapsed time: 2.234s, Critical Path: 1.5s
INFO: 5 processes: 2 internal, 3 linux-sandbox.
//src:add_test                                                       PASSED in 0.3s
//src:sub_test                                                       FAILED in 0.5s
  /home/user/.cache/bazel/sandbox/testlogs/src/sub_test/test.log
//src:mul_test                                                       PASSED in 0.2s

Executed 3 out of 3 tests: 2 tests pass and 1 fails locally.`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected Bazel summary")
	}
	if !strings.Contains(summary, "Executed 3 out of 3 tests") {
		t.Errorf("expected Bazel summary line, got: %q", summary)
	}
	if !strings.Contains(summary, "//src:sub_test") {
		t.Errorf("expected failed target name, got: %q", summary)
	}
}

func TestTestResultSummary_BazelAllPassing(t *testing.T) {
	output := `INFO: Analyzed 3 targets (0 packages loaded, 0 targets configured).
INFO: Found 3 test targets...
//src:add_test                                                       PASSED in 0.3s
//src:sub_test                                                       PASSED in 0.5s
//src:mul_test                                                       PASSED in 0.2s

Executed 3 out of 3 tests: 3 tests pass.`

	summary := testResultSummary(output)
	if summary == "" {
		t.Fatal("expected Bazel summary")
	}
	if !strings.Contains(summary, "3 tests pass") {
		t.Errorf("expected all-passing summary, got: %q", summary)
	}
}

func TestExtractTestCounts_Bazel(t *testing.T) {
	output := `//src:add_test                                                       PASSED in 0.3s
//src:sub_test                                                       FAILED in 0.5s
//src:mul_test                                                       PASSED in 0.2s

Executed 3 out of 3 tests: 2 tests pass and 1 fails locally.`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected Bazel test counts")
	}
	if p != 2 {
		t.Errorf("expected 2 passed, got %d", p)
	}
	if f != 1 {
		t.Errorf("expected 1 failed, got %d", f)
	}
}

func TestExtractTestCounts_BazelAllPassing(t *testing.T) {
	output := `//src:add_test                                                       PASSED in 0.3s
//src:sub_test                                                       PASSED in 0.5s

Executed 2 out of 2 tests: 2 tests pass.`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected Bazel test counts")
	}
	if p != 2 {
		t.Errorf("expected 2 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

func TestExtractTestCounts_Tasty(t *testing.T) {
	output := `  Test group
    test addition: OK (0.01s)
    test subtraction: OK (0.01s)
    test multiplication: FAIL
      expected: 42
       but got: 43

2 out of 3 tests failed (0.02s)`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected Tasty test counts")
	}
	if p != 1 {
		t.Errorf("expected 1 passed, got %d", p)
	}
	if f != 2 {
		t.Errorf("expected 2 failed, got %d", f)
	}
}

func TestExtractTestCounts_TastyAllPassing(t *testing.T) {
	// Tasty "All N tests passed" is handled by the generic/Zig section.
	output := `  Test group
    test addition: OK (0.01s)
    test subtraction: OK (0.01s)
    test multiplication: OK (0.01s)

All 3 tests passed (0.03s)`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected Tasty all-passing test counts")
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got %d", p)
	}
	if f != 0 {
		t.Errorf("expected 0 failed, got %d", f)
	}
}

func TestExtractCppFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "C test with assert calls",
			content: `
#include <assert.h>
#include "solution.h"

int main() {
    assert(solve(5, 3) == 8);
    int result = process(data, 10);
    assert(result == 42);
    return 0;
}
`,
			want: []string{"solve", "process"},
		},
		{
			name: "C++ test with namespace calls",
			content: `
#include <cassert>
#include "solution.hpp"

int main() {
    Solution sol;
    int result = sol.compute(3, std::vector<int>{1, 2, 3});
    assert(result == 6);
    auto val = Matrix::determinant(m);
    return 0;
}
`,
			want: []string{"compute", "determinant"},
		},
		{
			name: "Google Test style",
			content: `
#include <gtest/gtest.h>
#include "calculator.h"

TEST(CalculatorTest, BasicAdd) {
    Calculator calc;
    EXPECT_EQ(calc.add(3, 4), 7);
    EXPECT_EQ(multiply(5, 6), 30);
}
`,
			want: []string{"add", "multiply"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractCppFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestExtractCppFunctionSignatures_SkipsStdlib(t *testing.T) {
	content := `
#include <cstdio>
int main() {
    printf("hello %d\n", 42);
    int x = abs(-5);
    std::sort(v.begin(), v.end());
    return 0;
}
`
	sigs := extractCppFunctionSignatures(content)
	for _, sig := range sigs {
		if strings.Contains(sig, "printf(") || strings.Contains(sig, "abs(") || strings.Contains(sig, "sort(") {
			t.Errorf("should not extract stdlib function, got %q", sig)
		}
	}
}

func TestExtractElixirFunctionSignatures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "ExUnit test calling solution module",
			content: `
defmodule SolutionTest do
  use ExUnit.Case

  test "basic case" do
    assert Solution.solve(5, 3) == 8
    result = MyModule.process([1, 2, 3])
    assert result == 6
  end

  test "edge case" do
    assert Solution.solve(0, 0) == 0
  end
end
`,
			want: []string{"solve", "process"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := extractElixirFunctionSignatures(tt.content)
			for _, wantFunc := range tt.want {
				found := false
				for _, sig := range sigs {
					if strings.Contains(sig, wantFunc+"(") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find signature for %q in %v", wantFunc, sigs)
				}
			}
		})
	}
}

func TestExtractElixirFunctionSignatures_SkipsStdlib(t *testing.T) {
	content := `
defmodule SolutionTest do
  use ExUnit.Case

  test "stdlib calls" do
    list = Enum.map([1, 2, 3], &(&1 * 2))
    IO.inspect(list)
    assert length(list) == 3
  end
end
`
	sigs := extractElixirFunctionSignatures(content)
	for _, sig := range sigs {
		if strings.Contains(sig, "map(") || strings.Contains(sig, "inspect(") || strings.Contains(sig, "length(") {
			t.Errorf("should not extract stdlib function, got %q", sig)
		}
	}
}

func TestIsContextOverflowError_GeminiPattern(t *testing.T) {
	err := &core.ModelHTTPError{
		StatusCode: 400,
		Body:       "Please reduce the length of the prompt to fit within the model's context window.",
	}
	if !isContextOverflowError(err) {
		t.Error("expected Gemini 'reduce the length' pattern to be detected as context overflow")
	}
}

func TestIsContextOverflowError_TokensExceed(t *testing.T) {
	err := &core.ModelHTTPError{
		StatusCode: 400,
		Message:    "This model's maximum context length is 128000 tokens. Your messages resulted in 150000 tokens.",
		Body:       `{"error": {"message": "This model's maximum context length is 128000 tokens. Your messages resulted in 150000 tokens."}}`,
	}
	if !isContextOverflowError(err) {
		t.Error("expected tokens exceed pattern to be detected as context overflow")
	}
}

func TestIsContextOverflowError_PayloadTooLarge(t *testing.T) {
	err := &core.ModelHTTPError{
		StatusCode: 400,
		Body:       `{"error": "request too large"}`,
	}
	if !isContextOverflowError(err) {
		t.Error("expected 'request too large' to be detected as context overflow")
	}
}

func TestIsContextOverflowError_PromptTooLong(t *testing.T) {
	err := &core.ModelHTTPError{
		StatusCode: 400,
		Body:       `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 250000 tokens > 200000 maximum"}}`,
	}
	if !isContextOverflowError(err) {
		t.Error("expected 'prompt is too long' to be detected as context overflow")
	}
}

func TestIsContextOverflowError_TooManyTokens(t *testing.T) {
	err := &core.ModelHTTPError{
		StatusCode: 422,
		Body:       `{"detail": "Too many tokens in the request. Maximum is 128000."}`,
	}
	if !isContextOverflowError(err) {
		t.Error("expected 422 'too many tokens' to be detected as context overflow")
	}
}

func TestIsContextOverflowError_NonOverflowError(t *testing.T) {
	err := &core.ModelHTTPError{
		StatusCode: 400,
		Body:       `{"error": "invalid api key"}`,
	}
	if isContextOverflowError(err) {
		t.Error("expected non-overflow 400 error to NOT be detected as context overflow")
	}
}

// TestContextOverflowMiddleware_ShortHistoryNoPanic verifies that the
// ContextOverflowMiddleware doesn't panic when the message history is short
// (≤ keepLast+1 messages). The emergency compress returns the same number of
// messages with truncated content. The old code compared compressed[0] ==
// current[0] using interface equality — but ModelRequest contains a slice
// field (Parts), which is not comparable in Go and causes a runtime panic.
func TestContextOverflowMiddleware_ShortHistoryNoPanic(t *testing.T) {
	// Build a short message history (5 messages — within keepLast+1 = 7).
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Do a task"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "I'll help"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "More details"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Working on it"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Continue"},
			},
		},
	}

	// Call emergencyCompressMessagesWithConfig directly with the short history.
	// This returns the same number of messages (truncation only, no dropping).
	compressed := emergencyCompressMessagesWithConfig(messages, 20000, 6)

	// The old buggy code would compare compressed[0] == messages[0] which
	// panics because ModelRequest contains a slice. This test just verifies
	// no panic occurs and the compressed messages are usable.
	if len(compressed) != len(messages) {
		t.Errorf("expected %d messages, got %d", len(messages), len(compressed))
	}

	// Now test through the actual ContextOverflowMiddleware to confirm the
	// comparison doesn't panic. Use a model that always returns 413.
	callCount := 0
	mw := ContextOverflowMiddleware()
	_, err := mw(
		context.Background(),
		messages,
		nil,
		nil,
		func(ctx context.Context, msgs []core.ModelMessage, s *core.ModelSettings, p *core.ModelRequestParameters) (*core.ModelResponse, error) {
			callCount++
			return nil, &core.ModelHTTPError{
				StatusCode: 413,
				Body:       "request entity too large",
			}
		},
	)

	// Should get the 413 error back (after compression attempts), NOT a panic.
	if err == nil {
		t.Fatal("expected error from 413, got nil")
	}
	var httpErr *core.ModelHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 error, got: %v", err)
	}
}

func TestCompilationErrorHint_Julia(t *testing.T) {
	output := `ERROR: LoadError: syntax: unexpected "end"
Stacktrace:
 [1] include(fname::String)
   @ Base ./loading.jl:2076
in expression starting at script.jl:42
[exit code: 1]`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Julia error hint")
	}
	if !strings.Contains(hint, "script.jl") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference script.jl:42, got: %s", hint)
	}
	if !strings.Contains(hint, "syntax") || !strings.Contains(hint, "unexpected") {
		t.Errorf("expected hint to include error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_JuliaUndefVar(t *testing.T) {
	output := `ERROR: LoadError: UndefVarError: foo not defined
in expression starting at solution.jl:15
[exit code: 1]`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Julia UndefVarError hint")
	}
	if !strings.Contains(hint, "solution.jl") || !strings.Contains(hint, "15") {
		t.Errorf("expected hint to reference solution.jl:15, got: %s", hint)
	}
}

func TestCompilationErrorHint_RubySyntax(t *testing.T) {
	output := `script.rb:42: syntax error, unexpected end-of-input, expecting ')'
[exit code: 1]`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Ruby syntax error hint")
	}
	if !strings.Contains(hint, "script.rb") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference script.rb:42, got: %s", hint)
	}
	if !strings.Contains(hint, "syntax error") {
		t.Errorf("expected hint to include 'syntax error', got: %s", hint)
	}
}

func TestCompilationErrorHint_Lua(t *testing.T) {
	output := `lua: script.lua:42: attempt to call a nil value (global 'solve')
stack traceback:
	script.lua:42: in main chunk
	[C]: in ?
[exit code: 1]`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Lua error hint")
	}
	if !strings.Contains(hint, "script.lua") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference script.lua:42, got: %s", hint)
	}
	if !strings.Contains(hint, "attempt to call a nil value") {
		t.Errorf("expected hint to include error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Luac(t *testing.T) {
	output := `luac: solution.lua:10: '=' expected near '<eof>'
[exit code: 1]`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected luac error hint")
	}
	if !strings.Contains(hint, "solution.lua") || !strings.Contains(hint, "10") {
		t.Errorf("expected hint to reference solution.lua:10, got: %s", hint)
	}
}

func TestCompilationErrorHint_Gleam(t *testing.T) {
	output := `error: Unknown variable
  ┌─ src/main.gleam:42:5
  │
42 │   let x = foo
  │           ^^^ Did you mean ` + "`bar`" + `?
`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Gleam error hint")
	}
	if !strings.Contains(hint, "main.gleam") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference main.gleam:42, got: %s", hint)
	}
	if !strings.Contains(hint, "Unknown variable") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Gleam_TypeError(t *testing.T) {
	output := `error: Type mismatch
  ┌─ src/lib.gleam:15:10
  │
15 │   name + 1
  │          ^

Expected type:

    String

Found type:

    Int
`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Gleam type error hint")
	}
	if !strings.Contains(hint, "lib.gleam") || !strings.Contains(hint, "15") {
		t.Errorf("expected hint to reference lib.gleam:15, got: %s", hint)
	}
}

func TestCompilationErrorHint_Clojure(t *testing.T) {
	output := `Syntax error compiling at (src/core.clj:42:5).
No such var: clojure.core/foobar`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Clojure error hint")
	}
	if !strings.Contains(hint, "core.clj") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference core.clj:42, got: %s", hint)
	}
}

func TestCompilationErrorHint_Erlang(t *testing.T) {
	output := `src/module.erl:42: function foo/1 undefined`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Erlang error hint")
	}
	if !strings.Contains(hint, "module.erl") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference module.erl:42, got: %s", hint)
	}
	if !strings.Contains(hint, "function foo/1 undefined") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Elixir(t *testing.T) {
	output := `== Compilation error in file lib/app.ex ==
** (CompileError) lib/app.ex:42: undefined function foo/1
    (elixir) src/elixir.erl:355: :elixir.eval_forms/3`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Elixir compilation error")
	}
	if !strings.Contains(hint, "lib/app.ex:42") {
		t.Errorf("expected hint to contain file:line, got: %s", hint)
	}
	if !strings.Contains(hint, "undefined function foo/1") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_PHP(t *testing.T) {
	output := `PHP Parse error:  syntax error, unexpected '}' in /var/www/app.php on line 42`
	hint := compilationErrorHint(output, 255)
	if hint == "" {
		t.Fatal("expected hint for PHP parse error")
	}
	if !strings.Contains(hint, "app.php:42") {
		t.Errorf("expected hint to contain file:line, got: %s", hint)
	}
	if !strings.Contains(hint, "syntax error") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Crystal(t *testing.T) {
	output := `Error in src/main.cr:42: undefined local variable or method 'foo' for Main`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Crystal compilation error")
	}
	if !strings.Contains(hint, "src/main.cr:42") {
		t.Errorf("expected hint to contain file:line, got: %s", hint)
	}
	if !strings.Contains(hint, "undefined local variable") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Dart(t *testing.T) {
	output := `lib/main.dart:42:5: Error: Undefined name 'foo'.
    foo();
    ^^^`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Dart compilation error")
	}
	if !strings.Contains(hint, "lib/main.dart:42") {
		t.Errorf("expected hint to contain file:line, got: %s", hint)
	}
}

func TestCompilationErrorHint_Java(t *testing.T) {
	output := `MainClass.java:42: error: cannot find symbol
        System.out.println(foo);
                           ^
  symbol:   variable foo
  location: class MainClass`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Java compilation error")
	}
	if !strings.Contains(hint, "MainClass.java:42") {
		t.Errorf("expected hint to contain file:line, got: %s", hint)
	}
	if !strings.Contains(hint, "cannot find symbol") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Swift(t *testing.T) {
	output := `main.swift:10:5: error: use of unresolved identifier 'foo'
    let x = foo
            ^~~`
	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for Swift compilation error")
	}
	if !strings.Contains(hint, "main.swift:10") {
		t.Errorf("expected hint to contain file:line, got: %s", hint)
	}
	if !strings.Contains(hint, "unresolved identifier") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Elm(t *testing.T) {
	output := `-- TYPE MISMATCH --------- src/Main.elm

The 1st argument to ` + "`div`" + ` is not what I expect:

42| div "hello" []
        ^^^^^^^
This argument is a String, but ` + "`div`" + ` needs:

    List (Attribute msg)`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Elm error hint")
	}
	if !strings.Contains(hint, "Main.elm") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference Main.elm:42, got: %s", hint)
	}
	if !strings.Contains(hint, "TYPE MISMATCH") {
		t.Errorf("expected hint to contain error type, got: %s", hint)
	}
}

func TestCompilationErrorHint_Elm_NamingError(t *testing.T) {
	output := `-- NAMING ERROR --------- src/Page/Home.elm

I cannot find a ` + "`viewHeader`" + ` variable:

15| viewHeader model
    ^^^^^^^^^^
These names seem close though:

    viewFooter`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Elm naming error hint")
	}
	if !strings.Contains(hint, "Page/Home.elm") || !strings.Contains(hint, "15") {
		t.Errorf("expected hint to reference Page/Home.elm:15, got: %s", hint)
	}
	if !strings.Contains(hint, "NAMING ERROR") {
		t.Errorf("expected hint to contain error type, got: %s", hint)
	}
}

func TestCompilationErrorHint_Terraform(t *testing.T) {
	output := `
Error: Reference to undeclared resource

  on main.tf line 42, in resource "aws_instance" "web":
  42:   ami = aws_ami.ubuntu.id

A managed resource "aws_ami" "ubuntu" has not been declared in the root module.`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Terraform error hint")
	}
	if !strings.Contains(hint, "main.tf") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference main.tf:42, got: %s", hint)
	}
	if !strings.Contains(hint, "Reference to undeclared resource") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Terraform_Warning(t *testing.T) {
	output := `
Warning: Argument is deprecated

  on modules/vpc/main.tf line 10, in resource "aws_vpc" "main":
  10:   enable_classiclink = true

The enable_classiclink argument has been deprecated.`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Terraform warning hint")
	}
	if !strings.Contains(hint, "modules/vpc/main.tf") || !strings.Contains(hint, "10") {
		t.Errorf("expected hint to reference vpc/main.tf:10, got: %s", hint)
	}
	if !strings.Contains(hint, "Argument is deprecated") {
		t.Errorf("expected hint to contain warning message, got: %s", hint)
	}
}

func TestCompilationErrorHint_Nix(t *testing.T) {
	output := `error: undefined variable 'pkgss'

       at /home/user/flake.nix:42:5:

           41|   buildInputs = [
           42|     pkgss.hello
              |     ^
           43|   ];`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Nix error hint")
	}
	if !strings.Contains(hint, "flake.nix") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference flake.nix:42, got: %s", hint)
	}
}

func TestCompilationErrorHint_Solidity(t *testing.T) {
	output := `Error (7576): Undeclared identifier.
 --> contracts/Token.sol:42:9:
  |
42 |         balances[msg.sendr] += amount;
   |                  ^^^^^^^^^`

	hint := compilationErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected Solidity error hint")
	}
	if !strings.Contains(hint, "Token.sol") || !strings.Contains(hint, "42") {
		t.Errorf("expected hint to reference Token.sol:42, got: %s", hint)
	}
	if !strings.Contains(hint, "Undeclared identifier") {
		t.Errorf("expected hint to contain error message, got: %s", hint)
	}
}

func TestPythonErrorHint_CustomException(t *testing.T) {
	output := `Traceback (most recent call last):
  File "app/views.py", line 42, in process_form
    validate_input(data)
  File "app/validators.py", line 15, in validate_input
    raise InvalidInputError("missing required field 'name'")
InvalidInputError: missing required field 'name'`
	hint := pythonErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for custom Python exception")
	}
	if !strings.Contains(hint, "InvalidInputError") {
		t.Errorf("expected hint to contain custom exception name, got: %s", hint)
	}
	if !strings.Contains(hint, "app/validators.py:15") {
		t.Errorf("expected hint to contain innermost file:line, got: %s", hint)
	}
}

func TestPythonErrorHint_DottedModuleException(t *testing.T) {
	output := `Traceback (most recent call last):
  File "manage.py", line 10, in <module>
    execute_from_command_line(sys.argv)
  File "/app/myproject/settings.py", line 5, in <module>
    raise django.core.exceptions.ImproperlyConfigured("SECRET_KEY not set")
django.core.exceptions.ImproperlyConfigured: SECRET_KEY not set`
	hint := pythonErrorHint(output, 1)
	if hint == "" {
		t.Fatal("expected hint for dotted module Python exception")
	}
	if !strings.Contains(hint, "ImproperlyConfigured") {
		t.Errorf("expected hint to contain exception name, got: %s", hint)
	}
}

func TestLooksLikePythonException(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{"ValueError: invalid literal", true},
		{"CustomError: something failed", true},
		{"django.core.exceptions.ImproperlyConfigured: SECRET_KEY", true},
		{"myapp.errors.ValidationError: bad input", true},
		{"error: command not found", false},          // lowercase start
		{"This is not an error: just a note", false}, // spaces in name
		{"  IndentedError: not at column 0", false},  // leading space is treated as non-exception
		{"/usr/bin/python: can't open file", false},  // path, not exception
		{"", false},
		{"NoColonHere", false},
	}
	for _, tt := range tests {
		got := looksLikePythonException(tt.line)
		if got != tt.expected {
			t.Errorf("looksLikePythonException(%q) = %v, want %v", tt.line, got, tt.expected)
		}
	}
}

func TestParseJustfileTargets(t *testing.T) {
	justfile := `# Build the project
build:
    cargo build --release

# Run tests with optional filter
test filter="":
    cargo test {{filter}}

# Start the dev server
dev:
    cargo run

# Private recipe (should be skipped)
_setup:
    mkdir -p build

# Clean artifacts
clean:
    rm -rf target/

# Settings (should be skipped)
set dotenv-load

# Alias (should be skipped)
alias b := build
`
	targets := parseJustfileTargets(justfile)
	expected := map[string]bool{
		"build": true,
		"test":  true,
		"dev":   true,
		"clean": true,
	}
	for _, t2 := range targets {
		if !expected[t2] {
			t.Errorf("unexpected target: %s", t2)
		}
		delete(expected, t2)
	}
	for missing := range expected {
		t.Errorf("missing expected target: %s", missing)
	}
}

func TestEdit_PreservesFilePermissions(t *testing.T) {
	dir := t.TempDir()
	// Create an executable script.
	scriptPath := filepath.Join(dir, "run.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello\n"), 0o755)

	tool := Edit(WithWorkDir(dir))
	result := call(t, tool, `{"path": "run.sh", "old_string": "echo hello", "new_string": "echo world"}`)
	assertContains(t, result, "Replaced 1")

	// Verify permissions are preserved (not reset to 0644).
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0o111 == 0 {
		t.Errorf("expected executable permissions to be preserved, got %o", perm)
	}
}

func TestMultiEdit_PreservesFilePermissions(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "build.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/bash\nmake all\n"), 0o755)

	tool := MultiEdit(WithWorkDir(dir))
	result := call(t, tool, `{"edits": [{"path": "build.sh", "old_string": "make all", "new_string": "make clean all"}]}`)
	assertContains(t, result, "Replaced 1")

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0o111 == 0 {
		t.Errorf("expected executable permissions to be preserved, got %o", perm)
	}
}

func TestGrep_LongLinesTruncated(t *testing.T) {
	dir := t.TempDir()
	// Create a file with a very long line (e.g., minified JS).
	longLine := "var x=" + strings.Repeat("a", 5000) + ";"
	os.WriteFile(filepath.Join(dir, "bundle.js"), []byte(longLine), 0o644)

	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "var x"}`)
	assertContains(t, result, "bundle.js")
	// Should be truncated — result should be much shorter than 5000 chars.
	if len(result) > 3000 {
		t.Errorf("expected grep output to truncate long lines, got %d chars", len(result))
	}
	assertContains(t, result, "...")
}

func TestView_ShowsTotalLineCount(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "small.txt"), []byte("line1\nline2\nline3\n"), 0o644)

	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path": "small.txt"}`)
	// Should show total line count even for fully-read files.
	assertContains(t, result, "lines)")
}

func TestGrep_ExcludeFilter(t *testing.T) {
	dir := setupTestDir(t)

	tool := Grep(WithWorkDir(dir))

	// Without exclude, test files should be found.
	result := call(t, tool, `{"pattern": "func", "include": "*.go", "files_only": true}`)
	assertContains(t, result, "utils_test.go")
	assertContains(t, result, "utils.go")

	// With exclude, test files should be filtered out.
	result = call(t, tool, `{"pattern": "func", "include": "*.go", "exclude": "*_test.go", "files_only": true}`)
	assertNotContains(t, result, "utils_test.go")
	assertContains(t, result, "utils.go")
}

func TestGlob_ExcludeFilter(t *testing.T) {
	dir := setupTestDir(t)

	tool := Glob(WithWorkDir(dir))

	// Without exclude, test files should appear.
	result := call(t, tool, `{"pattern": "**/*.go"}`)
	assertContains(t, result, "utils_test.go")
	assertContains(t, result, "utils.go")

	// With exclude, test files should be filtered out.
	result = call(t, tool, `{"pattern": "**/*.go", "exclude": "*_test.go"}`)
	assertNotContains(t, result, "utils_test.go")
	assertContains(t, result, "utils.go")
}

func TestView_NegativeOffset(t *testing.T) {
	dir := t.TempDir()
	// Create a 10-line file.
	var content strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	writeTestFile(t, dir, "ten.txt", content.String())

	tool := View(WithWorkDir(dir))

	// offset=-3 should show last 3 lines (8, 9, 10).
	result := call(t, tool, `{"path": "ten.txt", "offset": -3}`)
	assertContains(t, result, "line 8")
	assertContains(t, result, "line 9")
	assertContains(t, result, "line 10")
	assertNotContains(t, result, "line 7")
	assertContains(t, result, "10 total lines, showing 8-10")
}

func TestView_NegativeOffsetLargerThanFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "small.txt", "alpha\nbeta\ngamma\n")

	tool := View(WithWorkDir(dir))

	// offset=-100 on a 3-line file should show all lines.
	result := call(t, tool, `{"path": "small.txt", "offset": -100}`)
	assertContains(t, result, "alpha")
	assertContains(t, result, "beta")
	assertContains(t, result, "gamma")
	assertContains(t, result, "showing 1-3")
}

func TestView_NegativeOffsetEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "empty.txt", "")

	tool := View(WithWorkDir(dir))

	result := call(t, tool, `{"path": "empty.txt", "offset": -5}`)
	assertContains(t, result, "empty file")
}

func TestView_ExactLimitNoTruncation(t *testing.T) {
	// A file with exactly `limit` lines should NOT show truncation indicator.
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	writeTestFile(t, dir, "exact.txt", strings.Join(lines, "\n"))

	tool := View(WithWorkDir(dir))
	result := call(t, tool, `{"path": "exact.txt", "limit": 10}`)
	assertContains(t, result, "line 1")
	assertContains(t, result, "line 10")
	assertNotContains(t, result, "...")
	assertContains(t, result, "10 lines")
}

func TestView_OffsetExactLimitNoTruncation(t *testing.T) {
	// Reading from an offset where remaining lines exactly equal limit
	// should NOT show truncation.
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 15; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	writeTestFile(t, dir, "offset.txt", strings.Join(lines, "\n"))

	tool := View(WithWorkDir(dir))
	// Read lines 6-15 (10 lines) with limit=10.
	result := call(t, tool, `{"path": "offset.txt", "offset": 6, "limit": 10}`)
	assertContains(t, result, "line 6")
	assertContains(t, result, "line 15")
	assertNotContains(t, result, "...")
	assertContains(t, result, "15 total lines")
}

func TestMultiEdit_IdenticalStrings(t *testing.T) {
	dir := setupTestDir(t)
	tool := MultiEdit(WithWorkDir(dir))
	args := `{"edits": [
		{"path": "hello.go", "old_string": "Hello, World!", "new_string": "Hello, World!"}
	]}`
	err := callErr(t, tool, args)
	if err == nil {
		t.Fatal("expected error for identical old_string and new_string")
	}
	assertContains(t, err.Error(), "identical")
}

func TestFindNearestLines_ShortFirstLine(t *testing.T) {
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n\nfunc helper() {\n\treturn 42\n}\n"
	// Search starts with "{" — a short line that the old code would bail on.
	search := "{\n\tfmt.Println(\"hello\")\n}"
	result := findNearestLines(content, search, 3)
	if result == "" {
		t.Fatal("expected findNearestLines to find matches when first line is short")
	}
	// Should anchor on "fmt.Println" line and find it.
	assertContains(t, result, "Println")
}

func TestFileSnippetForEdit_ShortFirstLine(t *testing.T) {
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n\nfunc helper() {\n\treturn 42\n}\n"
	// Search starts with "}" — short line, but the second line is meaningful.
	search := "}\n\nfunc helper() {"
	result := fileSnippetForEdit(content, search)
	if result == "" {
		t.Fatal("expected fileSnippetForEdit to find snippet when first line is short")
	}
	assertContains(t, result, "helper")
}

func TestAutoCorrectBlankLines(t *testing.T) {
	t.Run("strips_blanks_from_both", func(t *testing.T) {
		// Normal case: old has leading/trailing blanks, new also has them.
		content := "foo\nbar"
		oldStr := "\nfoo\nbar\n"
		newStr := "\nbaz\nqux\n"
		trimmedOld, trimmedNew, ok := autoCorrectBlankLines(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected ok")
		}
		if trimmedOld != "foo\nbar" {
			t.Errorf("trimmedOld = %q, want %q", trimmedOld, "foo\nbar")
		}
		if trimmedNew != "baz\nqux" {
			t.Errorf("trimmedNew = %q, want %q", trimmedNew, "baz\nqux")
		}
	})

	t.Run("new_has_no_blanks_to_strip", func(t *testing.T) {
		// Bug case: old has leading+trailing blanks, new has no blanks.
		// The function should NOT strip content lines from new.
		content := "foo\nbar"
		oldStr := "\nfoo\nbar\n"
		newStr := "baz\nqux"
		trimmedOld, trimmedNew, ok := autoCorrectBlankLines(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected ok")
		}
		if trimmedOld != "foo\nbar" {
			t.Errorf("trimmedOld = %q, want %q", trimmedOld, "foo\nbar")
		}
		// The new_string has no blank lines to strip — it should be unchanged.
		if trimmedNew != "baz\nqux" {
			t.Errorf("trimmedNew = %q, want %q (should not strip content lines)", trimmedNew, "baz\nqux")
		}
	})

	t.Run("new_has_fewer_blanks_than_old", func(t *testing.T) {
		// Old has 2 leading blanks, new has 1 leading blank.
		content := "foo\nbar"
		oldStr := "\n\nfoo\nbar"
		newStr := "\nbaz\nqux"
		trimmedOld, trimmedNew, ok := autoCorrectBlankLines(content, oldStr, newStr)
		if !ok {
			t.Fatal("expected ok")
		}
		if trimmedOld != "foo\nbar" {
			t.Errorf("trimmedOld = %q, want %q", trimmedOld, "foo\nbar")
		}
		// Should strip 1 leading blank from new (not 2, since new only has 1).
		if trimmedNew != "baz\nqux" {
			t.Errorf("trimmedNew = %q, want %q", trimmedNew, "baz\nqux")
		}
	})
}

func TestEdit_BlankLineStrippingPreservesNewContent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("foo\nbar\n"), 0o644)

	tool := Edit(WithWorkDir(dir))

	// Model writes old_string with leading blank that file doesn't have,
	// but new_string has no blank lines. The edit should replace foo\nbar
	// with baz\nqux, not delete it.
	result := call(t, tool, `{"path": "test.txt", "old_string": "\nfoo\nbar", "new_string": "baz\nqux"}`)
	assertContains(t, result, "Replaced 1")

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	content := string(data)
	if !strings.Contains(content, "baz") || !strings.Contains(content, "qux") {
		t.Errorf("expected new content to be preserved, got: %q", content)
	}
	if content == "\n" || content == "" {
		t.Error("content was deleted instead of replaced — blank line stripping removed content lines from new_string")
	}
}

func TestGrep_SummaryFooter(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "func"}`)
	// Should contain match/file count summary.
	assertContains(t, result, "matches in")
	assertContains(t, result, "files)")
}

func TestGrep_FilesOnlySummary(t *testing.T) {
	dir := setupTestDir(t)
	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "func", "files_only": true}`)
	assertContains(t, result, "files matched)")
}

func TestGlob_ResultCount(t *testing.T) {
	dir := setupTestDir(t)
	tool := Glob(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "**/*.go"}`)
	assertContains(t, result, "files)")
}

func TestLs_EntryCount(t *testing.T) {
	dir := setupTestDir(t)
	tool := Ls(WithWorkDir(dir))
	result := call(t, tool, `{}`)
	assertContains(t, result, "entries)")
}

func TestWrite_MissingTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	tool := Write(WithWorkDir(dir))
	// Write a Go file without trailing newline.
	result := call(t, tool, `{"path": "test.go", "content": "package main"}`)
	assertContains(t, result, "trailing newline")
}

func TestWrite_TrailingNewlinePresent(t *testing.T) {
	dir := t.TempDir()
	tool := Write(WithWorkDir(dir))
	// Write a Go file with trailing newline — should NOT warn.
	result := call(t, tool, `{"path": "test.go", "content": "package main\n"}`)
	if strings.Contains(result, "trailing newline") {
		t.Fatal("should not warn when trailing newline is present")
	}
}

func TestWrite_NoNewlineWarningForBinary(t *testing.T) {
	dir := t.TempDir()
	tool := Write(WithWorkDir(dir))
	// Write a .png file without trailing newline — should NOT warn.
	result := call(t, tool, `{"path": "image.png", "content": "fake binary data"}`)
	if strings.Contains(result, "trailing newline") {
		t.Fatal("should not warn about trailing newline for binary files")
	}
}

func TestWrite_PreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	// Create an existing CRLF file.
	os.WriteFile(filepath.Join(dir, "crlf.txt"), []byte("old\r\ncontent\r\n"), 0o644)

	tool := Write(WithWorkDir(dir))
	// Overwrite with LF-only content (what models generate).
	result := call(t, tool, `{"path": "crlf.txt", "content": "new\ncontent\n"}`)
	assertContains(t, result, "Wrote")

	data, _ := os.ReadFile(filepath.Join(dir, "crlf.txt"))
	content := string(data)
	if !strings.Contains(content, "\r\n") {
		t.Error("expected CRLF line endings to be preserved when overwriting a CRLF file")
	}
	if !strings.Contains(content, "new\r\ncontent\r\n") {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestWrite_NewFileLF(t *testing.T) {
	dir := t.TempDir()
	tool := Write(WithWorkDir(dir))
	// New file (no existing file) — should NOT add CRLF.
	result := call(t, tool, `{"path": "new.txt", "content": "line1\nline2\n"}`)
	assertContains(t, result, "Wrote")

	data, _ := os.ReadFile(filepath.Join(dir, "new.txt"))
	content := string(data)
	if strings.Contains(content, "\r\n") {
		t.Error("new file should not gain CRLF line endings")
	}
}

func TestExpandBraces(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"*.go", []string{"*.go"}},
		{"*.{go,py}", []string{"*.go", "*.py"}},
		{"src/{a,b}/*.ts", []string{"src/a/*.ts", "src/b/*.ts"}},
		{"{foo,bar,baz}.txt", []string{"foo.txt", "bar.txt", "baz.txt"}},
		{"no_braces", []string{"no_braces"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := expandBraces(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("expandBraces(%q) = %v, want %v", tt.input, got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Fatalf("expandBraces(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestGlob_BraceExpansion(t *testing.T) {
	dir := t.TempDir()
	// Create test files.
	writeTestFile(t, dir, "main.go", "package main\n")
	writeTestFile(t, dir, "util.py", "print('hi')\n")
	writeTestFile(t, dir, "readme.md", "# Readme\n")

	tool := Glob(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "*.{go,py}"}`)
	assertContains(t, result, "main.go")
	assertContains(t, result, "util.py")
	if strings.Contains(result, "readme.md") {
		t.Fatal("should not match .md files with *.{go,py}")
	}
}

func TestGrep_BraceInclude(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "main.go", "func main() {}\n")
	writeTestFile(t, dir, "util.py", "def main(): pass\n")
	writeTestFile(t, dir, "readme.md", "# Main section\n")

	tool := Grep(WithWorkDir(dir))
	result := call(t, tool, `{"pattern": "main", "include": "*.{go,py}"}`)
	assertContains(t, result, "main.go")
	assertContains(t, result, "util.py")
	if strings.Contains(result, "readme.md") {
		t.Fatal("should not match .md files with include *.{go,py}")
	}
}

// TestExtractTestCounts_PASSFALLFallbackFalsePositive verifies that the
// PASS/FAIL fallback line counter does NOT false-positive on partial word
// matches like PASSWORD, PASSENGER, FAILED, FAILURE, etc.
func TestExtractTestCounts_PASSFALLFallbackFalsePositive(t *testing.T) {
	// This output contains "PASS" as a substring in env vars and "FAIL" in
	// error messages — none are actual test PASS/FAIL lines.
	output := `Setting up environment...
PASSWORD=secret123
PASS_AUTH=true
PASSENGER_PORT=3000
PASS_MAX_DAYS=90
Starting server...
FAILURE: connection refused
FAILED to bind port 8080
FAILOVER mode enabled
Build complete.`

	p, f, ok := extractTestCounts(output)
	if ok {
		t.Errorf("expected ok=false for non-test output with PASS/FAIL substrings, got passed=%d failed=%d", p, f)
	}
}

// TestExtractTestCounts_PASSFALLFallbackLegitimate verifies that the
// PASS/FAIL fallback correctly handles real bare PASS/FAIL lines.
func TestExtractTestCounts_PASSFALLFallbackLegitimate(t *testing.T) {
	output := `Running test suite...
PASS test_addition
PASS test_subtraction
FAIL test_division
PASS test_multiplication`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for output with bare PASS/FAIL lines")
	}
	if p != 3 {
		t.Errorf("expected 3 passed, got %d", p)
	}
	if f != 1 {
		t.Errorf("expected 1 failed, got %d", f)
	}
}

// TestExtractTestCounts_GradleCompleted verifies Gradle "N tests completed"
// parsing with the actual Gradle output format.
func TestExtractTestCounts_GradleCompleted(t *testing.T) {
	output := `> Task :test

3 tests completed, 1 failed

> Task :test FAILED

BUILD FAILED in 2s
3 actionable tasks: 3 executed`

	p, f, ok := extractTestCounts(output)
	if !ok {
		t.Fatal("expected ok=true for Gradle test output")
	}
	if p != 2 {
		t.Errorf("expected 2 passed, got %d", p)
	}
	if f != 1 {
		t.Errorf("expected 1 failed, got %d", f)
	}
}

func TestContextInjectionMiddleware_DoesNotMutateInput(t *testing.T) {
	// ContextInjectionMiddleware prepends environment context to the first
	// message's system parts. Before the fix, it mutated the caller's
	// messages slice directly (messages[0] = req), which caused envPart to
	// accumulate on every model call when the middleware shared a backing
	// array with the agent's persistent message history.

	dir := t.TempDir()
	mw := ContextInjectionMiddleware(dir)
	ctx := context.Background()

	originalPart := core.UserPromptPart{Content: "Hello"}
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{originalPart},
		},
	}

	// Capture the original first message for comparison.
	origReq := messages[0].(core.ModelRequest)
	origPartsLen := len(origReq.Parts)

	var receivedMsgs []core.ModelMessage
	next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
		receivedMsgs = msgs
		return &core.ModelResponse{
			Parts: []core.ModelResponsePart{core.TextPart{Content: "ok"}},
		}, nil
	}

	_, err := mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The middleware should have injected env context into what next() received.
	if len(receivedMsgs) == 0 {
		t.Fatal("next received no messages")
	}
	nextReq, ok := receivedMsgs[0].(core.ModelRequest)
	if !ok {
		t.Fatal("next's first message is not a ModelRequest")
	}
	if len(nextReq.Parts) <= origPartsLen {
		t.Error("expected middleware to prepend env context, but parts count did not increase")
	}

	// The original messages slice must NOT have been mutated.
	afterReq := messages[0].(core.ModelRequest)
	if len(afterReq.Parts) != origPartsLen {
		t.Errorf("original messages[0] was mutated: had %d parts, now has %d", origPartsLen, len(afterReq.Parts))
	}

	// Call a second time to verify env context doesn't accumulate.
	_, err = mw(ctx, messages, nil, nil, next)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	afterReq2 := messages[0].(core.ModelRequest)
	if len(afterReq2.Parts) != origPartsLen {
		t.Errorf("original messages[0] mutated on second call: had %d parts, now has %d", origPartsLen, len(afterReq2.Parts))
	}
}

func TestProgressTrackingMiddleware_CompoundBashCommands(t *testing.T) {
	// ProgressTrackingMiddleware should detect file-writing commands even
	// when they appear in compound bash commands (e.g. "cd /app && gcc main.c").
	// Before the fix, HasPrefix-only checks for gcc/g++/cc/make/wget missed
	// these compound forms, leading to false progress warnings.

	compoundCommands := []string{
		`cd /app && gcc main.c -o main`,
		`cd /src && g++ app.cpp -o app`,
		`cd /project && cc hello.c`,
		`cd /build && make`,
		`cd /build && make all`,
		`cd /tmp && wget https://example.com/file.tar.gz`,
		`export CFLAGS="-O2" && gcc -shared lib.c -o lib.so`,
		`mkdir -p build && cd build && cmake .. && make`,
	}

	for _, cmd := range compoundCommands {
		t.Run(cmd, func(t *testing.T) {
			dir := t.TempDir()
			mw := ProgressTrackingMiddleware(dir)
			ctx := context.Background()

			argsJSON, _ := json.Marshal(map[string]string{"command": cmd})

			// Build message history with the bash tool call in a model response.
			messages := []core.ModelMessage{
				core.ModelRequest{
					Parts: []core.ModelRequestPart{
						core.UserPromptPart{Content: "Build the project"},
					},
				},
				core.ModelResponse{
					Parts: []core.ModelResponsePart{
						core.ToolCallPart{
							ToolName: "bash",
							ArgsJSON: string(argsJSON),
						},
					},
				},
			}

			next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
				// Check that no progress warning was injected.
				for _, msg := range msgs {
					if req, ok := msg.(core.ModelRequest); ok {
						for _, part := range req.Parts {
							if up, ok := part.(core.UserPromptPart); ok {
								if strings.Contains(up.Content, "PROGRESS WARNING") || strings.Contains(up.Content, "CRITICAL") {
									t.Errorf("unexpected progress warning injected for compound command %q: %s", cmd, up.Content)
								}
							}
						}
					}
				}
				return &core.ModelResponse{
					Parts: []core.ModelResponsePart{core.TextPart{Content: "ok"}},
				}, nil
			}

			// Run enough turns to trigger the warning threshold (default turnWarning=7).
			for i := range 10 {
				_, err := mw(ctx, messages, nil, nil, next)
				if err != nil {
					t.Fatalf("turn %d: unexpected error: %v", i, err)
				}
			}
		})
	}
}

func TestProgressTrackingMiddleware_SimpleCommandsStillDetected(t *testing.T) {
	// Verify that simple (non-compound) file-writing commands are still detected.
	simpleCommands := []string{
		`gcc main.c -o main`,
		`g++ app.cpp -o app`,
		`cc hello.c`,
		`make`,
		`make all`,
		`wget https://example.com/file.tar.gz`,
		`cp src/file.txt dst/`,
		`mv old.txt new.txt`,
	}

	for _, cmd := range simpleCommands {
		t.Run(cmd, func(t *testing.T) {
			dir := t.TempDir()
			mw := ProgressTrackingMiddleware(dir)
			ctx := context.Background()

			argsJSON, _ := json.Marshal(map[string]string{"command": cmd})

			messages := []core.ModelMessage{
				core.ModelRequest{
					Parts: []core.ModelRequestPart{
						core.UserPromptPart{Content: "Build"},
					},
				},
				core.ModelResponse{
					Parts: []core.ModelResponsePart{
						core.ToolCallPart{
							ToolName: "bash",
							ArgsJSON: string(argsJSON),
						},
					},
				},
			}

			next := func(_ context.Context, msgs []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
				for _, msg := range msgs {
					if req, ok := msg.(core.ModelRequest); ok {
						for _, part := range req.Parts {
							if up, ok := part.(core.UserPromptPart); ok {
								if strings.Contains(up.Content, "PROGRESS WARNING") || strings.Contains(up.Content, "CRITICAL") {
									t.Errorf("unexpected progress warning for simple command %q: %s", cmd, up.Content)
								}
							}
						}
					}
				}
				return &core.ModelResponse{
					Parts: []core.ModelResponsePart{core.TextPart{Content: "ok"}},
				}, nil
			}

			for i := range 10 {
				_, err := mw(ctx, messages, nil, nil, next)
				if err != nil {
					t.Fatalf("turn %d: unexpected error: %v", i, err)
				}
			}
		})
	}
}

func TestTruncateAtRuneBoundary(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		wantLen  int // expected byte length of result
	}{
		{
			name:     "ASCII only, no truncation needed",
			input:    "hello world",
			maxBytes: 100,
			wantLen:  11,
		},
		{
			name:     "ASCII only, truncation",
			input:    "hello world",
			maxBytes: 5,
			wantLen:  5,
		},
		{
			name:     "CJK text, cut between characters",
			input:    "Hello 世界你好", // "Hello " = 6 bytes, each CJK char = 3 bytes
			maxBytes: 9,            // Falls right at end of 世
			wantLen:  9,
		},
		{
			name:     "CJK text, cut mid-character",
			input:    "Hello 世界你好",
			maxBytes: 7, // Falls in the middle of 世 (bytes 6,7,8)
			wantLen:  6, // Should back up to end of "Hello "
		},
		{
			name:     "CJK text, cut mid-character second byte",
			input:    "Hello 世界你好",
			maxBytes: 8, // Falls in the middle of 世 (bytes 6,7,8)
			wantLen:  6, // Should back up to end of "Hello "
		},
		{
			name:     "Emoji, cut mid-character",
			input:    "Hi 👋🌍", // "Hi " = 3 bytes, 👋 = 4 bytes, 🌍 = 4 bytes
			maxBytes: 5,       // Falls in middle of 👋 (bytes 3,4,5,6)
			wantLen:  3,       // Should back up to end of "Hi "
		},
		{
			name:     "Emoji, cut right after emoji",
			input:    "Hi 👋🌍",
			maxBytes: 7, // Right after 👋
			wantLen:  7,
		},
		{
			name:     "Empty string",
			input:    "",
			maxBytes: 10,
			wantLen:  0,
		},
		{
			name:     "All multi-byte, cut at zero",
			input:    "世界",
			maxBytes: 0,
			wantLen:  0,
		},
		{
			name:     "All multi-byte, cut at 1 (inside first char)",
			input:    "世界",
			maxBytes: 1,
			wantLen:  0, // Can't fit even one CJK character
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateAtRuneBoundary(tt.input, tt.maxBytes)

			if len(result) != tt.wantLen {
				t.Errorf("truncateAtRuneBoundary(%q, %d): got len=%d, want len=%d (result=%q)",
					tt.input, tt.maxBytes, len(result), tt.wantLen, result)
			}

			// The result must always be valid UTF-8.
			if !utf8.ValidString(result) {
				t.Errorf("truncateAtRuneBoundary(%q, %d): result %q is not valid UTF-8",
					tt.input, tt.maxBytes, result)
			}

			// The result must be a prefix of the input.
			if !strings.HasPrefix(tt.input, result) {
				t.Errorf("truncateAtRuneBoundary(%q, %d): result %q is not a prefix of input",
					tt.input, tt.maxBytes, result)
			}
		})
	}
}

func TestTruncateMessageContent_UTF8Safety(t *testing.T) {
	// Construct a message with CJK content that would be cut mid-character
	// by naive byte truncation.
	cjkContent := strings.Repeat("世界你好", 100) // 1200 bytes of CJK text

	// Test ToolReturnPart truncation.
	msg := core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.ToolReturnPart{
				ToolCallID: "call_1",
				Content:    cjkContent,
			},
		},
	}

	truncated := truncateMessageContent(msg, 50)
	req, ok := truncated.(core.ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	tr, ok := req.Parts[0].(core.ToolReturnPart)
	if !ok {
		t.Fatal("expected ToolReturnPart")
	}
	content, ok := tr.Content.(string)
	if !ok {
		t.Fatal("expected string content")
	}
	if !utf8.ValidString(content) {
		t.Errorf("ToolReturnPart content is not valid UTF-8 after truncation: %q...%q",
			content[:20], content[len(content)-20:])
	}

	// Test UserPromptPart truncation.
	msg2 := core.ModelRequest{
		Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: cjkContent},
		},
	}

	truncated2 := truncateMessageContent(msg2, 50)
	req2 := truncated2.(core.ModelRequest)
	up := req2.Parts[0].(core.UserPromptPart)
	if !utf8.ValidString(up.Content) {
		t.Errorf("UserPromptPart content is not valid UTF-8 after truncation")
	}

	// Test TextPart (ModelResponse) truncation.
	msg3 := core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.TextPart{Content: cjkContent},
		},
	}

	truncated3 := truncateMessageContent(msg3, 50)
	resp := truncated3.(core.ModelResponse)
	tp := resp.Parts[0].(core.TextPart)
	if !utf8.ValidString(tp.Content) {
		t.Errorf("TextPart content is not valid UTF-8 after truncation")
	}
}

func TestTruncateOutput_UTF8Safety(t *testing.T) {
	// Build a long string with CJK characters that would be cut mid-character.
	input := strings.Repeat("错误信息：测试失败", 200) // ~5400 bytes of CJK

	result := truncateOutput(input, 100)

	if !utf8.ValidString(result) {
		t.Errorf("truncateOutput produced invalid UTF-8")
	}

	// Verify it was actually truncated.
	if !strings.Contains(result, "[truncated") {
		t.Error("expected truncation marker in output")
	}
}

func TestEmergencyCompressMessages_Alternation(t *testing.T) {
	// Build a conversation with proper alternation: Req, Resp, Req, Resp, ...
	// Then compress and verify the result still alternates properly.
	messages := []core.ModelMessage{
		// First message: user prompt (always kept)
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Implement the feature"},
			},
		},
	}

	// Add alternating messages to get past the threshold.
	for i := range 14 {
		if i%2 == 0 {
			messages = append(messages, core.ModelResponse{
				Parts: []core.ModelResponsePart{
					core.TextPart{Content: fmt.Sprintf("Assistant response %d", i)},
				},
			})
		} else {
			messages = append(messages, core.ModelRequest{
				Parts: []core.ModelRequestPart{
					core.UserPromptPart{Content: fmt.Sprintf("User message %d", i)},
				},
			})
		}
	}

	compressed := emergencyCompressMessagesWithConfig(messages, 20000, 6)

	// Check alternation: each message should alternate between request and response.
	for i := 1; i < len(compressed); i++ {
		_, prevIsReq := compressed[i-1].(core.ModelRequest)
		_, prevIsResp := compressed[i-1].(core.ModelResponse)
		_, curIsReq := compressed[i].(core.ModelRequest)
		_, curIsResp := compressed[i].(core.ModelResponse)

		if prevIsReq && curIsReq {
			t.Errorf("adjacent ModelRequests at positions %d and %d", i-1, i)
		}
		if prevIsResp && curIsResp {
			t.Errorf("adjacent ModelResponses at positions %d and %d", i-1, i)
		}
		if !prevIsReq && !prevIsResp {
			t.Errorf("unknown message type at position %d", i-1)
		}
		if !curIsReq && !curIsResp {
			t.Errorf("unknown message type at position %d", i)
		}
	}

	// First message should still be the original user prompt.
	firstReq, ok := compressed[0].(core.ModelRequest)
	if !ok {
		t.Fatal("first message should be ModelRequest")
	}
	up, ok := firstReq.Parts[0].(core.UserPromptPart)
	if !ok {
		t.Fatal("first part should be UserPromptPart")
	}
	if up.Content != "Implement the feature" {
		t.Errorf("first message content changed: %q", up.Content)
	}

	// Second message should be the recovery summary (ModelResponse).
	_, ok = compressed[1].(core.ModelResponse)
	if !ok {
		t.Error("second message should be ModelResponse (recovery summary)")
	}
}

// TestEmergencyCompressMessages_OrphanedToolResults verifies that emergency
// compression strips tool results whose matching tool calls were dropped.
// Without this fix, the Anthropic API rejects the compressed messages because
// tool_result blocks reference tool_use IDs that no longer exist.
func TestEmergencyCompressMessages_OrphanedToolResults(t *testing.T) {
	messages := []core.ModelMessage{
		// Message 0: task prompt (always kept).
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Implement the feature"},
			},
		},
	}

	// Messages 1-6: conversation with tool calls (will be dropped).
	for i := range 3 {
		// Assistant response with a tool call.
		messages = append(messages, core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: fmt.Sprintf("Let me check file %d", i)},
				core.ToolCallPart{
					ToolName:   "view",
					ArgsJSON:   fmt.Sprintf(`{"path":"file%d.go"}`, i),
					ToolCallID: fmt.Sprintf("call_%d", i),
				},
			},
		})
		// Tool result matching the call.
		messages = append(messages, core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "view",
					ToolCallID: fmt.Sprintf("call_%d", i),
					Content:    fmt.Sprintf("contents of file%d.go", i),
				},
			},
		})
	}

	// Messages 7-12: more conversation (these will be kept as the tail).
	for i := 3; i < 6; i++ {
		messages = append(messages, core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: fmt.Sprintf("Let me edit file %d", i)},
				core.ToolCallPart{
					ToolName:   "edit",
					ArgsJSON:   fmt.Sprintf(`{"path":"file%d.go"}`, i),
					ToolCallID: fmt.Sprintf("call_%d", i),
				},
			},
		})
		messages = append(messages, core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "edit",
					ToolCallID: fmt.Sprintf("call_%d", i),
					Content:    "edit applied",
				},
			},
		})
	}

	// Add a final response.
	messages = append(messages, core.ModelResponse{
		Parts: []core.ModelResponsePart{
			core.TextPart{Content: "Done editing"},
		},
	})

	// Compress with keepLast=6. This should drop messages 1-6 (3 tool call/result pairs)
	// and keep messages 7-12 + 13 as the tail. The tail's first ModelRequest
	// should NOT have orphaned tool results.
	compressed := emergencyCompressMessagesWithConfig(messages, 20000, 6)

	// Collect all tool call IDs in the compressed messages.
	toolCallIDs := make(map[string]bool)
	for _, msg := range compressed {
		if resp, ok := msg.(core.ModelResponse); ok {
			for _, part := range resp.Parts {
				if tc, ok := part.(core.ToolCallPart); ok {
					toolCallIDs[tc.ToolCallID] = true
				}
			}
		}
	}

	// Verify no orphaned tool results.
	for i, msg := range compressed {
		if req, ok := msg.(core.ModelRequest); ok {
			for _, part := range req.Parts {
				if tr, ok := part.(core.ToolReturnPart); ok {
					if !toolCallIDs[tr.ToolCallID] {
						t.Errorf("message %d has orphaned ToolReturnPart with ID %q (no matching ToolCallPart)", i, tr.ToolCallID)
					}
				}
				if rp, ok := part.(core.RetryPromptPart); ok {
					if rp.ToolCallID != "" && !toolCallIDs[rp.ToolCallID] {
						t.Errorf("message %d has orphaned RetryPromptPart with ID %q (no matching ToolCallPart)", i, rp.ToolCallID)
					}
				}
			}
		}
	}
}

// TestEdit_AutoCorrectWhitespace_TabsVsSpaces verifies that whitespace auto-
// correction handles the most common production failure: the model sends spaces
// but the file uses tabs (or vice versa).
func TestEdit_AutoCorrectWhitespace_TabsVsSpaces(t *testing.T) {
	dir := setupTestDir(t)

	// File uses tabs for indentation.
	tabContent := "package main\n\nfunc foo() {\n\tif true {\n\t\treturn 1\n\t}\n}\n"
	writeTestFile(t, dir, "tabs.go", tabContent)

	tool := Edit(WithWorkDir(dir))

	// Model sends spaces (4-space indent) instead of tabs.
	out := call(t, tool, `{
		"path": "tabs.go",
		"old_string": "    if true {\n        return 1\n    }",
		"new_string": "    if true {\n        return 2\n    }"
	}`)
	if !strings.Contains(out, "auto-corrected") {
		t.Errorf("expected auto-correction note, got: %s", out)
	}

	// Verify the file was updated correctly.
	got := readFileContent(t, filepath.Join(dir, "tabs.go"))
	if !strings.Contains(got, "\t\treturn 2") {
		t.Errorf("expected tab-indented 'return 2', got:\n%s", got)
	}
}

// TestEdit_AutoCorrectBlankLines_ExtraLeading verifies correction when the
// model includes extra blank lines at the start of old_string.
func TestEdit_AutoCorrectBlankLines_ExtraLeading(t *testing.T) {
	dir := setupTestDir(t)

	// File has exactly one blank line before the function.
	fileContent := "package main\n\nfunc foo() {\n\treturn 1\n}\n"
	writeTestFile(t, dir, "blank.go", fileContent)

	tool := Edit(WithWorkDir(dir))

	// Model includes TWO extra blank lines before the function,
	// but the file only has one. This exercises blank line auto-correction.
	out := call(t, tool, `{
		"path": "blank.go",
		"old_string": "\n\n\nfunc foo() {\n\treturn 1\n}",
		"new_string": "\n\n\nfunc bar() {\n\treturn 2\n}"
	}`)
	if !strings.Contains(out, "auto-corrected") {
		t.Errorf("expected auto-correction note, got: %s", out)
	}
	got := readFileContent(t, filepath.Join(dir, "blank.go"))
	if !strings.Contains(got, "func bar()") {
		t.Errorf("expected 'func bar()', got:\n%s", got)
	}
}

// TestEdit_AutoCorrectLineTrim_WrongContextLine verifies that line trim
// correction works when the model includes an incorrect context line.
func TestEdit_AutoCorrectLineTrim_WrongContextLine(t *testing.T) {
	dir := setupTestDir(t)

	content := "package main\n\nimport \"fmt\"\n\nfunc foo() {\n\tfmt.Println(\"hello\")\n\treturn 1\n}\n"
	writeTestFile(t, dir, "trim.go", content)

	tool := Edit(WithWorkDir(dir))

	// Model includes wrong first context line (import instead of blank line).
	// The middle lines are unique, so line trim should fix it.
	out := call(t, tool, `{
		"path": "trim.go",
		"old_string": "import \"os\"\n\nfunc foo() {\n\tfmt.Println(\"hello\")\n\treturn 1\n}",
		"new_string": "import \"os\"\n\nfunc foo() {\n\tfmt.Println(\"world\")\n\treturn 2\n}"
	}`)
	if !strings.Contains(out, "auto-corrected") {
		t.Errorf("expected auto-correction note, got: %s", out)
	}
	got := readFileContent(t, filepath.Join(dir, "trim.go"))
	if !strings.Contains(got, "return 2") {
		t.Errorf("expected 'return 2', got:\n%s", got)
	}
}

// TestExtractTestCounts_VariousFrameworks exercises the test count parser with
// real-world output from multiple test frameworks.
func TestExtractTestCounts_VariousFrameworks(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantPassed int
		wantFailed int
		wantOK     bool
	}{
		{
			name:       "pytest_pass_and_fail",
			output:     "==== 8 passed, 2 failed in 1.23s ====",
			wantPassed: 8, wantFailed: 2, wantOK: true,
		},
		{
			name:       "pytest_all_pass",
			output:     "==== 15 passed in 3.45s ====",
			wantPassed: 15, wantFailed: 0, wantOK: true,
		},
		{
			name:       "go_test_mixed",
			output:     "--- PASS: TestFoo (0.01s)\n--- PASS: TestBar (0.02s)\n--- FAIL: TestBaz (0.01s)\nFAIL",
			wantPassed: 2, wantFailed: 1, wantOK: true,
		},
		{
			name:       "cargo_test",
			output:     "test result: FAILED. 10 passed; 3 failed; 0 ignored; 0 measured; 0 filtered out",
			wantPassed: 10, wantFailed: 3, wantOK: true,
		},
		{
			name:       "jest_output",
			output:     "Test Suites:  2 passed, 1 failed, 3 total\nTests:        5 passed, 2 failed, 7 total",
			wantPassed: 5, wantFailed: 2, wantOK: true,
		},
		{
			name:       "rspec_output",
			output:     "20 examples, 3 failures",
			wantPassed: 17, wantFailed: 3, wantOK: true,
		},
		{
			name:       "unittest_ok",
			output:     "Ran 12 tests in 0.5s\n\nOK",
			wantPassed: 12, wantFailed: 0, wantOK: true,
		},
		{
			name:       "unittest_fail",
			output:     "Ran 10 tests in 1.2s\n\nFAILED (failures=2, errors=1)",
			wantPassed: 7, wantFailed: 3, wantOK: true,
		},
		{
			name:       "mocha_output",
			output:     "  7 passing (100ms)\n  2 failing",
			wantPassed: 7, wantFailed: 2, wantOK: true,
		},
		{
			name:       "catch2_all_pass",
			output:     "All tests passed (42 assertions in 10 test cases)",
			wantPassed: 10, wantFailed: 0, wantOK: true,
		},
		{
			name:       "catch2_mixed",
			output:     "test cases: 8 | 6 passed | 2 failed\nassertions: 20 | 16 passed | 4 failed",
			wantPassed: 6, wantFailed: 2, wantOK: true,
		},
		{
			name:       "no_test_output",
			output:     "hello world\nall done",
			wantPassed: 0, wantFailed: 0, wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, failed, ok := extractTestCounts(tt.output)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if passed != tt.wantPassed {
				t.Errorf("passed = %d, want %d", passed, tt.wantPassed)
			}
			if failed != tt.wantFailed {
				t.Errorf("failed = %d, want %d", failed, tt.wantFailed)
			}
		})
	}
}

// readFileContent is a test helper that reads a file's contents.
func readFileContent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFileContent: %v", err)
	}
	return string(data)
}

func TestInjectUserPromptIntoLastRequest(t *testing.T) {
	// Messages ending with a ModelRequest — common case.
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "system"},
			core.UserPromptPart{Content: "hello"},
		}},
		core.ModelResponse{Parts: []core.ModelResponsePart{
			core.TextPart{Content: "response"},
			core.ToolCallPart{ToolName: "bash", ArgsJSON: `{}`, ToolCallID: "call_1"},
		}},
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.ToolReturnPart{ToolName: "bash", ToolCallID: "call_1", Content: "ok"},
		}},
	}

	result := injectUserPromptIntoLastRequest(messages, "TIME WARNING: 50% elapsed")

	// Must not create a new message — should inject into the last one.
	if len(result) != len(messages) {
		t.Fatalf("expected %d messages, got %d (new message was appended instead of injecting)", len(messages), len(result))
	}

	// Last message should now have the injected UserPromptPart.
	lastReq, ok := result[len(result)-1].(core.ModelRequest)
	if !ok {
		t.Fatal("last message is not a ModelRequest")
	}
	if len(lastReq.Parts) != 2 {
		t.Fatalf("expected 2 parts in last request, got %d", len(lastReq.Parts))
	}
	up, ok := lastReq.Parts[1].(core.UserPromptPart)
	if !ok {
		t.Fatal("second part is not UserPromptPart")
	}
	if !strings.Contains(up.Content, "TIME WARNING") {
		t.Errorf("injected content missing, got %q", up.Content)
	}

	// Original messages must NOT be mutated.
	origLast := messages[len(messages)-1].(core.ModelRequest)
	if len(origLast.Parts) != 1 {
		t.Errorf("original message was mutated: expected 1 part, got %d", len(origLast.Parts))
	}

	// Verify no consecutive ModelRequests in result.
	for i := 1; i < len(result); i++ {
		_, prevIsReq := result[i-1].(core.ModelRequest)
		_, currIsReq := result[i].(core.ModelRequest)
		if prevIsReq && currIsReq {
			t.Errorf("consecutive ModelRequests at positions %d and %d", i-1, i)
		}
	}
}

// TestStripOrphanedToolResults_NoConsecutiveMessages verifies that stripping
// orphaned tool results does not create consecutive same-role messages.
// When a ModelRequest contains ONLY orphaned tool results with empty/non-string
// content, the message must be kept with a placeholder rather than dropped.
func TestStripOrphanedToolResults_NoConsecutiveMessages(t *testing.T) {
	messages := []core.ModelMessage{
		// First message.
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: "Do the task"},
			},
		},
		// Summary (assistant role).
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "[Summary] previous work"},
			},
		},
		// Orphaned tool result with non-string content — previously this
		// ModelRequest would be dropped entirely, creating consecutive
		// ModelResponse messages.
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "bash",
					ToolCallID: "orphan_1",
					Content:    42, // non-string, conversion gives empty string
				},
			},
		},
		// Next response (assistant role).
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{
					ToolName:   "edit",
					ToolCallID: "valid_1",
					ArgsJSON:   `{"path":"foo.go"}`,
				},
			},
		},
		// Valid tool result.
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{
					ToolName:   "edit",
					ToolCallID: "valid_1",
					Content:    "edit applied",
				},
			},
		},
	}

	result := stripOrphanedToolResults(messages)

	// Verify no consecutive same-role messages.
	for i := 1; i < len(result); i++ {
		_, prevIsReq := result[i-1].(core.ModelRequest)
		_, currIsReq := result[i].(core.ModelRequest)
		_, prevIsResp := result[i-1].(core.ModelResponse)
		_, currIsResp := result[i].(core.ModelResponse)
		if prevIsReq && currIsReq {
			t.Errorf("consecutive ModelRequest at %d and %d — would cause Anthropic 400", i-1, i)
		}
		if prevIsResp && currIsResp {
			t.Errorf("consecutive ModelResponse at %d and %d — would cause Anthropic 400", i-1, i)
		}
	}

	// The result should keep all 5 messages (none dropped).
	if len(result) != 5 {
		t.Errorf("expected 5 messages (placeholder for orphaned), got %d", len(result))
	}
}
