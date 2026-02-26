package codetool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/fugue-labs/gollem/core"
)

// truncateOutput shortens long output by keeping the head and tail, connected
// by a truncation notice. This preserves error summaries that appear at the
// end of test or build output.
//
// The split ratio adapts based on content: if the output contains error/failure
// indicators, we keep more of the tail (where error details and summaries live).
func truncateOutput(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}

	// Check if the output looks like it contains errors/test results.
	// If so, keep more of the tail where summaries and error details appear.
	headRatio := 70 // default: 70% head, 30% tail
	lower := strings.ToLower(s[max(0, len(s)-5000):])
	if strings.Contains(lower, "error") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "failure") ||
		strings.Contains(lower, "traceback") ||
		strings.Contains(lower, "panic:") ||
		strings.Contains(lower, "assertion") {
		headRatio = 30 // flip: 30% head, 70% tail for error output
	}

	headLen := maxLen * headRatio / 100
	tailLen := maxLen - headLen - 100 // reserve space for the separator
	if tailLen < 0 {
		tailLen = 0
	}
	// Adjust head and tail cut points to valid UTF-8 rune boundaries
	// to avoid splitting multi-byte characters (CJK, emoji, etc.).
	head := truncateAtRuneBoundary(s, headLen)
	tailStart := len(s) - tailLen
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	return head +
		fmt.Sprintf("\n\n... [truncated %d bytes, showing first %d and last %d bytes] ...\n\n", len(s), headLen, tailLen) +
		s[tailStart:]
}

// BashParams are the parameters for the bash tool.
type BashParams struct {
	// Command is the shell command to execute.
	Command string `json:"command" jsonschema:"description=The bash command to execute"`

	// Timeout is an optional timeout in seconds. Overrides the default.
	Timeout *int `json:"timeout,omitempty" jsonschema:"description=Optional timeout in seconds (default: 300)"`
}

// BashResult is the result of a bash command execution (used in tests).
type BashResult struct {
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// Bash creates a tool that executes shell commands.
// Returns formatted text (not JSON) for efficient token usage and easier model parsing.
func Bash(opts ...Option) core.Tool {
	cfg := applyOpts(opts)

	// Track test failure fingerprints for stale-failure detection.
	// When the same test failure appears twice in a row, the agent's fix
	// was ineffective — warn it to try a different approach.
	// Safe because bash is WithToolSequential (no concurrent calls).
	var lastTestFailFingerprint string

	// Track compilation error fingerprints similarly — when the same
	// compilation error appears twice, the fix attempt was ineffective.
	var lastCompileErrorFingerprint string

	// Track pass/fail counts across test runs to detect stagnation.
	// When the agent runs tests 3+ times without improving the pass rate,
	// it's likely stuck — warn it to try a fundamentally different approach.
	type testRunRecord struct {
		passed, failed int
	}
	var testRunHistory []testRunRecord
	const maxTestHistory = 5

	return core.FuncTool[BashParams](
		"bash",
		"Execute a bash command in the shell. Use this for running programs, installing packages, "+
			"compiling code, running tests, git operations, and any other terminal commands. "+
			"Commands run in a persistent working directory. "+
			"Prefer this tool for exploring the filesystem and running build/test commands.",
		func(ctx context.Context, params BashParams) (string, error) {
			if strings.TrimSpace(params.Command) == "" {
				return "", &core.ModelRetryError{Message: "command must not be empty"}
			}

			// Block bash commands that destructively modify verifier test files.
			// The edit/write tools already block /tests/ modifications, but
			// agents can bypass that via bash redirects, rm, sed -i, etc.
			if isDestructiveTestCommand(params.Command) {
				return "", &core.ModelRetryError{
					Message: "BLOCKED: This command would modify files in /tests/ (verifier test directory). " +
						"The verifier runs the ORIGINAL tests — your changes will be ignored. " +
						"Fix YOUR code to pass the tests instead.",
				}
			}
			if isRiskyProcessKillCommand(params.Command) {
				return "", &core.ModelRetryError{
					Message: "BLOCKED: broad process-kill patterns are unsafe in benchmark containers " +
						"(e.g., `pkill -f`, `killall`) and can terminate unrelated processes. " +
						"Use PID-file lifecycle management instead: start with `nohup ... & echo $! > /tmp/<name>.pid` " +
						"and stop with `kill $(cat /tmp/<name>.pid)`.",
				}
			}

			timeout := cfg.BashTimeout
			if params.Timeout != nil && *params.Timeout > 0 {
				timeout = time.Duration(*params.Timeout) * time.Second
			}
			// Build/benchmark commands frequently exceed short timeouts.
			// Keep a 5m floor even when the model explicitly passes a smaller value.
			if isBuildCommand(params.Command) && timeout < 5*time.Minute {
				timeout = 5 * time.Minute
			} else if isLongRunningCommand(params.Command) && timeout < 5*time.Minute {
				timeout = 5 * time.Minute
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			cmd := exec.CommandContext(ctx, "bash", "-c", params.Command)
			if cfg.WorkDir != "" {
				cmd.Dir = cfg.WorkDir
			}

			// Auto-set PIP_BREAK_SYSTEM_PACKAGES=1 for pip commands to prevent
			// externally-managed-environment errors in Docker containers.
			// This saves a full turn of error → hint → retry.
			if isPipCommand(params.Command) {
				cmd.Env = append(os.Environ(), "PIP_BREAK_SYSTEM_PACKAGES=1")
			}

			// Run in a new process group so we can kill all children on timeout.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error {
				// Kill the entire process group (negative PID).
				return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}

			// Execute the command with one auto-retry for transient failures
			// (network errors, dpkg locks, etc.). This saves a full agent turn.
			var outStr, errStr string
			exitCode := 0
			timedOut := false
			retried := false

			for attempt := range 2 {
				var stdout, stderr bytes.Buffer
				if attempt == 0 {
					cmd.Stdout = &stdout
					cmd.Stderr = &stderr
				} else {
					// Re-create cmd for retry (exec.Cmd can only be started once).
					retried = true
					cmd = exec.CommandContext(ctx, "bash", "-c", params.Command)
					if cfg.WorkDir != "" {
						cmd.Dir = cfg.WorkDir
					}
					if isPipCommand(params.Command) {
						cmd.Env = append(os.Environ(), "PIP_BREAK_SYSTEM_PACKAGES=1")
					}
					cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
					cmd.Cancel = func() error {
						return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					}
					cmd.Stdout = &stdout
					cmd.Stderr = &stderr
					fmt.Fprintf(os.Stderr, "[gollem] bash: auto-retrying transient failure\n")
					// Brief pause before retry.
					time.Sleep(2 * time.Second)
				}

				err := cmd.Run()

				exitCode = 0
				timedOut = false
				if err != nil {
					if ctx.Err() == context.DeadlineExceeded {
						timedOut = true
						exitCode = 124
					} else {
						var exitErr *exec.ExitError
						if errors.As(err, &exitErr) {
							exitCode = exitErr.ExitCode()
						} else {
							return "", fmt.Errorf("failed to execute command: %w", err)
						}
					}
				}

				outStr = stdout.String()
				errStr = stderr.String()

				// Auto-retry on transient failures (first attempt only).
				// Pass the command so we can skip retries for non-install commands
				// where "connection refused" is expected (e.g., curl testing a service).
				if attempt == 0 && !timedOut && isTransientBashFailure(exitCode, outStr+errStr, params.Command) {
					continue
				}
				break
			}
			rawLen := len(outStr) + len(errStr)

			// Pre-compute test/compilation data from FULL output before truncation.
			// Fingerprints and counts are ALWAYS computed (cheap operations) to
			// ensure stagnation/regression/stale-failure detection works even for
			// short output from single-test or small-project tasks. Summaries are
			// only computed for long output where they condense information the
			// model can't easily see after truncation.
			fullCombined := outStr + errStr
			var preTestSummary string
			var preCompileSummary string

			// Always compute fingerprints and counts for tracking.
			preTestFingerprint := testFailureFingerprint(fullCombined)
			preTestPassed, preTestFailed, preTestCountsOK := extractTestCounts(fullCombined)

			// Only compute summaries for long output — the model can already
			// read short output directly without a condensed summary.
			if len(fullCombined) > 2000 {
				preTestSummary = testResultSummary(fullCombined)
				// Also pre-compute compilation error summary if no test output.
				// Long builds can lose error lines in the middle after truncation.
				if preTestSummary == "" {
					preCompileSummary = compilationErrorSummary(fullCombined, exitCode)
				}
			}

			// Truncate long output, keeping head and tail so the model can
			// see error summaries at the end.
			outStr = truncateOutput(outStr, cfg.MaxOutputLen)
			errStr = truncateOutput(errStr, cfg.MaxOutputLen)

			result := formatBashOutput(outStr, errStr, exitCode, timedOut, timeout, params.Command)

			// Context-aware timeout hint: server/daemon commands need background
			// execution, not optimization. This saves a turn of confusion.
			if timedOut {
				if hint := timeoutContextHint(params.Command); hint != "" {
					result += "\n" + hint
				}
				// Add optimization-specific hints when tests/builds timeout.
				if hint := testTimeoutOptimizationHint(params.Command); hint != "" {
					result += "\n" + hint
				}
			}

			// Note when the command succeeded after auto-retry.
			if retried && exitCode == 0 {
				result += "\n[auto-retried after transient failure — succeeded on second attempt]"
			}

			// Hint when output was heavily truncated — suggest file redirect.
			if rawLen > cfg.MaxOutputLen*2 {
				result += fmt.Sprintf("\n[hint: output was %d bytes (heavily truncated). For large output, redirect to a file: cmd > /tmp/out.txt 2>&1, then use view or grep to find what you need]", rawLen)
			}

			// Add hints for common errors — saves turns of troubleshooting.
			// Compute combined output once to avoid repeated string concatenation
			// (25+ hint functions would each allocate a new temp string).
			combinedOutput := errStr + outStr

			if exitCode == 127 || strings.Contains(errStr, "command not found") || strings.Contains(errStr, "No such file or directory") {
				if hint := commandNotFoundHint(errStr); hint != "" {
					result += "\n" + hint
				}
			}
			if strings.Contains(errStr, "ModuleNotFoundError") || strings.Contains(errStr, "ImportError") || strings.Contains(outStr, "ModuleNotFoundError") {
				if hint := moduleNotFoundHint(combinedOutput, cfg.WorkDir); hint != "" {
					result += "\n" + hint
				}
			}
			if hint := transientErrorHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := signalHint(exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := pythonErrorHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := pipInstallHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := compilationErrorHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := jsonErrorHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := encodingErrorHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := permissionHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := addressInUseHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := connectionRefusedHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := systemctlNotFoundHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := nodeErrorHint(combinedOutput, exitCode, cfg.WorkDir); hint != "" {
				result += "\n" + hint
			}
			if hint := outputMismatchHint(combinedOutput, exitCode, cfg.WorkDir); hint != "" {
				result += "\n" + hint
			}
			if hint := subprocessTimeoutHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := sharedLibraryHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := diskSpaceHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := makefileHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := cmakeHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := cargoHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := goModuleHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := gitHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := archiveHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := databaseHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := memoryHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := browserHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := sslHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := shellLimitHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := perlModuleHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := rubyGemHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := javaExceptionHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := elixirHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := yamlErrorHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := dockerHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := envVarHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := downloadHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}
			if hint := sedHint(combinedOutput, exitCode); hint != "" {
				result += "\n" + hint
			}

			// Test and compilation tracking: detect stagnation, regression, and
			// stale failures. Tracking always runs (even for short output) to
			// catch loops in single-test and small-project tasks. Summaries are
			// only displayed for long output where they condense truncated info.
			//
			// Determine if this output contains test results (failure detail or
			// parseable counts). This is more reliable than checking command
			// patterns since agents may run tests via custom scripts.
			hasTestOutput := preTestSummary != "" || preTestFingerprint != "" || preTestCountsOK

			if hasTestOutput {
				// Display summary for long output.
				if preTestSummary != "" {
					result += "\n" + preTestSummary
				}

				// Check for failures in the output.
				hasFailures := preTestFingerprint != ""
				if preTestSummary != "" {
					summaryLower := strings.ToLower(preTestSummary)
					hasFailures = hasFailures || strings.Contains(summaryLower, "fail") || strings.Contains(summaryLower, "error")
				}

				if exitCode != 0 && hasFailures {
					// Surface the first failure detail so the agent knows
					// exactly what's wrong without scanning the full output.
					fp := preTestFingerprint
					if fp != "" {
						if preTestSummary != "" && !strings.Contains(preTestSummary, fp) {
							result += "\n" + fp
						} else if preTestSummary == "" {
							// Short output: show failure detail for focus.
							result += "\n" + fp
						}
					} else if preTestSummary != "" {
						result += "\n[hint: read the FULL test failure output above — fix one failure at a time, starting with the first]"
					}
					if fp != "" && fp == lastTestFailFingerprint {
						result += "\n[hint: this test failure is IDENTICAL to the previous run — your edit did not fix the issue. Re-read the error, verify your edit was applied correctly, and try a fundamentally different approach]"
					}
					lastTestFailFingerprint = fp
					if preTestCountsOK {
						rec := testRunRecord{passed: preTestPassed, failed: preTestFailed}
						testRunHistory = append(testRunHistory, rec)
						if len(testRunHistory) > maxTestHistory {
							testRunHistory = testRunHistory[len(testRunHistory)-maxTestHistory:]
						}
						if len(testRunHistory) >= 2 {
							prev := testRunHistory[len(testRunHistory)-2]
							curr := testRunHistory[len(testRunHistory)-1]
							if curr.passed < prev.passed && prev.passed > 0 {
								result += fmt.Sprintf("\n[hint: REGRESSION — pass count dropped from %d/%d to %d/%d. "+
									"Your last change likely broke something. Consider: "+
									"(1) reverting the last edit, (2) re-reading what you changed, "+
									"(3) checking if you introduced a syntax error or typo]",
									prev.passed, prev.passed+prev.failed,
									curr.passed, curr.passed+curr.failed)
							}
						}
						if len(testRunHistory) >= 3 {
							last3 := testRunHistory[len(testRunHistory)-3:]
							bestPassed := last3[0].passed
							improving := false
							for _, r := range last3[1:] {
								if r.passed > bestPassed {
									improving = true
									bestPassed = r.passed
								}
							}
							if !improving && last3[len(last3)-1].failed > 0 {
								result += fmt.Sprintf("\n[hint: pass rate has NOT improved over last 3 test runs (%d/%d → %d/%d → %d/%d). Your current approach may be fundamentally wrong — consider: (1) re-reading the task requirements, (2) trying a completely different algorithm, (3) checking if you missed a constraint]",
									last3[0].passed, last3[0].passed+last3[0].failed,
									last3[1].passed, last3[1].passed+last3[1].failed,
									last3[2].passed, last3[2].passed+last3[2].failed)
							}
						}
					}
				} else {
					// Test passed or no failure indicators — clear tracking.
					lastTestFailFingerprint = ""
					testRunHistory = nil
				}
			} else if preCompileSummary != "" {
				// Use pre-computed compilation summary from FULL output.
				result += "\n" + preCompileSummary
				// Stale compilation error detection: fingerprint the first error
				// line and warn if it's identical to the last compilation error.
				fp := compilationFingerprint(fullCombined)
				if fp != "" && fp == lastCompileErrorFingerprint {
					result += "\n[hint: this compilation error is IDENTICAL to the previous build — your edit did not fix the issue. Re-read the error message carefully, verify your edit was applied to the correct file and line, and try a different fix]"
				}
				lastCompileErrorFingerprint = fp
			} else if exitCode != 0 {
				// No test output and no pre-computed compilation summary.
				// Still track compilation fingerprints for stale-error detection.
				fp := compilationFingerprint(fullCombined)
				if fp != "" {
					if fp == lastCompileErrorFingerprint {
						result += "\n[hint: this compilation error is IDENTICAL to the previous build — your edit did not fix the issue. Re-read the error message carefully, verify your edit was applied to the correct file and line, and try a different fix]"
					}
					lastCompileErrorFingerprint = fp
				}
				// Fallback: compute compilation summary from full output for long builds.
				// Use fullCombined (pre-truncation) to avoid losing error lines
				// that may have been cut during output truncation.
				if len(fullCombined) > 2000 {
					if summary := compilationErrorSummary(fullCombined, exitCode); summary != "" {
						result += "\n" + summary
					}
				}
			}

			// Clear tracking state on successful verification or build,
			// regardless of output length. Without this, short-output success
			// doesn't clear stale fingerprints, causing false warnings.
			if exitCode == 0 {
				if isBuildCommand(params.Command) {
					lastCompileErrorFingerprint = ""
				}
				if isVerificationString(strings.ToLower(params.Command)) {
					lastTestFailFingerprint = ""
					testRunHistory = nil
					lastCompileErrorFingerprint = ""
				}
			}

			return result, nil
		},
		core.WithToolSequential(true), // bash commands should run sequentially
	)
}

// formatBashOutput combines stdout, stderr, and exit code into a clean text
// format that's efficient on tokens and easy for models to parse.
func formatBashOutput(stdout, stderr string, exitCode int, timedOut bool, timeout time.Duration, command string) string {
	var b strings.Builder
	hasContent := stdout != "" || stderr != ""

	if stdout != "" {
		b.WriteString(stdout)
	}
	if stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n[stderr]\n")
		}
		b.WriteString(stderr)
	}

	if timedOut {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if isBuildCommand(command) {
			fmt.Fprintf(&b, "[timed out after %s — compilation took too long. Strategies: "+
				"(1) Use parallel builds: `make -j$(nproc)`, `cargo build -j$(nproc)` "+
				"(2) Add `-O0` instead of `-O2`/`-O3` for faster compilation (optimize for compile speed, not runtime) "+
				"(3) Use `timeout` parameter with a longer value "+
				"(4) Compile fewer files at once or split into stages "+
				"(5) If building from source, check if a pre-built package exists]", timeout)
		} else {
			fmt.Fprintf(&b, "[timed out after %s — if this is a test or benchmark, optimize YOUR code to be faster. Do NOT modify test/benchmark parameters. Use the timeout parameter for legitimately long-running commands.]", timeout)
		}
	} else if exitCode != 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[exit code: %d]", exitCode)
		if !hasContent {
			b.WriteString("\n(no output)")
		}
	}

	if b.Len() == 0 {
		return "(no output)"
	}

	return b.String()
}

// commandNotFoundHint generates an installation hint when a command is missing.
// This saves a turn of the model figuring out what package to install.
func commandNotFoundHint(stderr string) string {
	// Common command → package mappings for Debian/Ubuntu containers.
	packages := map[string]string{
		"python3":            "python3",
		"python":             "python3",
		"pip3":               "python3-pip",
		"pip":                "python3-pip",
		"node":               "nodejs",
		"npm":                "npm",
		"gcc":                "build-essential",
		"g++":                "build-essential",
		"cc":                 "build-essential",
		"make":               "build-essential",
		"cmake":              "cmake",
		"meson":              "meson",
		"bazel":              "bazel-bootstrap",
		"curl":               "curl",
		"wget":               "wget",
		"git":                "git",
		"java":               "default-jdk",
		"javac":              "default-jdk",
		"perl":               "perl",
		"ruby":               "ruby",
		"gfortran":           "gfortran",
		"ffmpeg":             "ffmpeg",
		"jq":                 "jq",
		"unzip":              "unzip",
		"zip":                "zip",
		"bc":                 "bc",
		"flex":               "flex",
		"bison":              "bison",
		"pkg-config":         "pkg-config",
		"autoconf":           "autoconf",
		"automake":           "automake",
		"libtool":            "libtool",
		"rsync":              "rsync",
		"sqlite3":            "sqlite3",
		"lsof":               "lsof",
		"netcat":             "netcat-openbsd",
		"nc":                 "netcat-openbsd",
		"socat":              "socat",
		"grpc":               "protobuf-compiler",
		"protoc":             "protobuf-compiler",
		"valgrind":           "valgrind",
		"gdb":                "gdb",
		"strace":             "strace",
		"ltrace":             "ltrace",
		"nasm":               "nasm",
		"yasm":               "yasm",
		"rustc":              "rustc",
		"cargo":              "cargo",
		"ghc":                "ghc",
		"ocaml":              "ocaml",
		"racket":             "racket",
		"guile":              "guile-3.0",
		"sbcl":               "sbcl",
		"gawk":               "gawk",
		"m4":                 "m4",
		"patch":              "patch",
		"diffutils":          "diffutils",
		"file":               "file",
		"xxd":                "xxd",
		"hexdump":            "bsdmainutils",
		"strings":            "binutils",
		"objdump":            "binutils",
		"readelf":            "binutils",
		"ar":                 "binutils",
		"nm":                 "binutils",
		"strip":              "binutils",
		"as":                 "binutils",
		"ld":                 "binutils",
		"expect":             "expect",
		"sshd":               "openssh-server",
		"ssh":                "openssh-client",
		"sshpass":            "sshpass",
		"postfix":            "postfix",
		"sendmail":           "sendmail",
		"tesseract":          "tesseract-ocr",
		"gnuplot":            "gnuplot-nox",
		"dot":                "graphviz",
		"neato":              "graphviz",
		"latex":              "texlive-latex-base",
		"pdflatex":           "texlive-latex-base",
		"xelatex":            "texlive-xetex",
		"convert":            "imagemagick",
		"identify":           "imagemagick",
		"sox":                "sox",
		"mplayer":            "mplayer",
		"qemu-system-x86_64": "qemu-system-x86",
		"qemu-system-mips":   "qemu-system-mips",
		"qemu-system-arm":    "qemu-system-arm",
		"qemu-img":           "qemu-utils",
		"cobc":               "gnucobol",
		"gnat":               "gnat",
		"fpc":                "fp-compiler",
		"swipl":              "swi-prolog",
		"mono":               "mono-runtime",
		"mcs":                "mono-mcs",
		"R":                  "r-base",
		"Rscript":            "r-base",
		"scala":              "scala",
		"lua":                "lua5.4",
		"luarocks":           "luarocks",
		"tcl":                "tcl",
		"wish":               "tk",
		"tclsh":              "tcl",
		"swift":              "swift",
		"swiftc":             "swift",
		"nim":                "nim",
		"nimble":             "nim",
		"choosenim":          "nim",
		"elixir":             "elixir",
		"mix":                "elixir",
		"iex":                "elixir",
		"erlc":               "erlang",
		"erl":                "erlang",
		"crystal":            "crystal",
		"shards":             "crystal",
		"zig":                "zig",
		"opam":               "opam",
		"dune":               "ocaml-dune",
		"ocamlfind":          "ocaml-findlib",
		"coqc":               "coq",
		"pmars":              "pmars",
		"xmllint":            "libxml2-utils",
		"mvn":                "maven",
		"gradle":             "gradle",
		"ant":                "ant",
		"sbt":                "sbt",
		"dart":               "dart",
		"flutter":            "flutter",
		"docker":             "docker.io",
		"podman":             "podman",
		"xsltproc":           "xsltproc",
		"tput":               "ncurses-bin",
		"column":             "bsdmainutils",
		"rename":             "rename",
		"iconv":              "libc-bin",
		"openssl":            "openssl",
		"php":                "php",
		"clang":              "clang",
		"lldb":               "lldb",
		"tree":               "tree",
		"tmux":               "tmux",
		"screen":             "screen",
		"dig":                "dnsutils",
		"nslookup":           "dnsutils",
		"host":               "dnsutils",
		"traceroute":         "traceroute",
		"ifconfig":           "net-tools",
		"inotifywait":        "inotify-tools",
		"rg":                 "ripgrep",
		"fd":                 "fd-find",
		"pigz":               "pigz",
		"pv":                 "pv",
		"entr":               "entr",
		"sshfs":              "sshfs",
		"parallel":           "parallel",
		"csvtool":            "csvtool",
		// JS/TS runtimes and package managers not covered above.
		"bun":  "bun (install via: curl -fsSL https://bun.sh/install | bash)",
		"deno": "deno (install via: curl -fsSL https://deno.land/install.sh | sh)",
		"pnpm": "pnpm (install via: npm install -g pnpm)",
		"npx":  "npm",
		"tsx":  "tsx (install via: npm install -g tsx)",
		// Python tool runners.
		"uv":      "uv (install via: curl -LsSf https://astral.sh/uv/install.sh | sh)",
		"ruff":    "ruff (install via: pip install ruff)",
		"mypy":    "mypy (install via: pip install mypy)",
		"black":   "black (install via: pip install black)",
		"isort":   "isort (install via: pip install isort)",
		"pyright": "pyright (install via: pip install pyright)",
		// Language servers (for LSP tool).
		"gopls":  "gopls (install via: go install golang.org/x/tools/gopls@latest)",
		"clangd": "clangd",
		// Haskell tools.
		"stack": "haskell-stack",
		"cabal": "cabal-install",
		"ghcup": "ghcup (install via: curl --proto '=https' --tlsv1.2 -sSf https://get-ghcup.haskell.org | sh)",
		// Clojure.
		"lein": "leiningen (install via: apt-get install -y leiningen)",
		"clj":  "clojure (install via: apt-get install -y clojure)",
		// Erlang.
		"rebar3": "rebar3 (install via: apt-get install -y erlang-dev && mix local.rebar --force)",
		// Python tools.
		"poetry":   "poetry (install via: pip install poetry)",
		"pdm":      "pdm (install via: pip install pdm)",
		"hatch":    "hatch (install via: pip install hatch)",
		"pytest":   "pytest (install via: pip install pytest)",
		"coverage": "coverage (install via: pip install coverage)",
	}

	// Extract the missing command name from stderr.
	lower := strings.ToLower(stderr)
	for cmd, pkg := range packages {
		if strings.Contains(lower, cmd+": command not found") ||
			strings.Contains(lower, cmd+": not found") {
			return fmt.Sprintf("[hint: try: apt-get install -y %s]", pkg)
		}
	}

	// Fallback for pip/ensurepip.
	if strings.Contains(lower, "no module named pip") {
		return "[hint: try: python3 -m ensurepip || apt-get install -y python3-pip]"
	}

	return ""
}

// moduleNotFoundHint generates a pip install hint when a Python import fails.
// This saves a turn of the model figuring out which package to install.
// When workDir is provided, it checks whether the module is a local file/package
// and suggests PYTHONPATH instead of pip install.
func moduleNotFoundHint(output string, workDir ...string) string {
	// Common module → pip package mappings where they differ.
	aliases := map[string]string{
		"cv2":             "opencv-python",
		"PIL":             "Pillow",
		"sklearn":         "scikit-learn",
		"skimage":         "scikit-image",
		"yaml":            "PyYAML",
		"bs4":             "beautifulsoup4",
		"attr":            "attrs",
		"dotenv":          "python-dotenv",
		"git":             "GitPython",
		"serial":          "pyserial",
		"usb":             "pyusb",
		"magic":           "python-magic",
		"Crypto":          "pycryptodome",
		"dateutil":        "python-dateutil",
		"jwt":             "PyJWT",
		"lxml":            "lxml",
		"wx":              "wxPython",
		"gi":              "PyGObject",
		"nacl":            "PyNaCl",
		"socks":           "PySocks",
		"zmq":             "pyzmq",
		"Levenshtein":     "python-Levenshtein",
		"Bio":             "biopython",
		"torch":           "torch",
		"torchvision":     "torchvision",
		"tensorflow":      "tensorflow",
		"tf":              "tensorflow",
		"scipy":           "scipy",
		"pandas":          "pandas",
		"matplotlib":      "matplotlib",
		"seaborn":         "seaborn",
		"flask":           "flask",
		"django":          "django",
		"fastapi":         "fastapi",
		"uvicorn":         "uvicorn",
		"gunicorn":        "gunicorn",
		"grpc":            "grpcio grpcio-tools",
		"google.protobuf": "protobuf",
		"pydantic":        "pydantic",
		"httpx":           "httpx",
		"aiohttp":         "aiohttp",
		"sqlalchemy":      "sqlalchemy",
		"alembic":         "alembic",
		"celery":          "celery",
		"redis":           "redis",
		"pymongo":         "pymongo",
		"psycopg2":        "psycopg2-binary",
		"MySQLdb":         "mysqlclient",
		"mysql":           "mysqlclient",
		"toml":            "toml",
		"tomli":           "tomli",
		"tomllib":         "tomli",
		"msgpack":         "msgpack",
		"protobuf":        "protobuf",
		"Cython":          "cython",
		"sympy":           "sympy",
		"networkx":        "networkx",
		"pyarrow":         "pyarrow",
		"h5py":            "h5py",
		"transformers":    "transformers",
		"datasets":        "datasets",
		"tokenizers":      "tokenizers",
		"tqdm":            "tqdm",
		"click":           "click",
		"rich":            "rich",
		"paramiko":        "paramiko",
		"fabric":          "fabric",
		"pexpect":         "pexpect",
		"ply":             "ply",
		"lark":            "lark",
		"pyparsing":       "pyparsing",
		"construct":       "construct",
		"bitstring":       "bitstring",
		"elftools":        "pyelftools",
		"imageio":         "imageio",
		"shapely":         "shapely",
		"geopandas":       "geopandas",
		"trio":            "trio",
		"anyio":           "anyio",
		"gevent":          "gevent",
		"cbor2":           "cbor2",
		"zstandard":       "zstandard",
		"lz4":             "lz4",
		"pulp":            "PuLP",
		"cvxpy":           "cvxpy",
		"z3":              "z3-solver",
		"typer":           "typer",
		"astropy":         "astropy",
		"pwn":             "pwntools",
		"pwnlib":          "pwntools",
		"capstone":        "capstone",
		"angr":            "angr",
	}

	// Try to extract the module name from common error patterns.
	// Patterns: "No module named 'foo'" or "No module named 'foo.bar'"
	for _, pattern := range []string{"No module named '", "No module named \""} {
		idx := strings.Index(output, pattern)
		if idx < 0 {
			continue
		}
		start := idx + len(pattern)
		rest := output[start:]
		end := strings.IndexAny(rest, "'\"")
		if end < 0 {
			continue
		}
		module := rest[:end]
		// Use the top-level package name (e.g., "foo" from "foo.bar.baz").
		if dot := strings.Index(module, "."); dot > 0 {
			module = module[:dot]
		}
		if module == "" {
			continue
		}
		// Check if the module is a local file/package in the work directory.
		// For local modules, PYTHONPATH is the fix — not pip install.
		if len(workDir) > 0 && workDir[0] != "" {
			wd := workDir[0]
			pyFile := filepath.Join(wd, module+".py")
			pkgDir := filepath.Join(wd, module)
			if fileExists(pyFile) || (dirExists(pkgDir) && fileExists(filepath.Join(pkgDir, "__init__.py"))) {
				return fmt.Sprintf("[hint: '%s' is a local module. Run with: cd %s && python3 <script> or PYTHONPATH=%s python3 <script>]",
					module, wd, wd)
			}
		}
		pkg := module
		if alias, ok := aliases[module]; ok {
			pkg = alias
		}
		if strings.TrimSpace(os.Getenv("VIRTUAL_ENV")) != "" {
			return fmt.Sprintf(
				"[hint: try: uv pip install %s (fallback: pip install %s)]",
				pkg, pkg,
			)
		}
		return fmt.Sprintf(
			"[hint: try: uv pip install --system %s (fallback: pip install --break-system-packages %s)]",
			pkg, pkg,
		)
	}

	return ""
}

// transientErrorHint detects common transient errors and suggests fixes.
// These are errors that waste turns if the model has to diagnose them itself.
func transientErrorHint(output string, exitCode int) string {
	lower := strings.ToLower(output)

	// pip: externally-managed-environment error.
	// Extremely common in Docker containers — wastes 1-2 turns without a hint.
	if strings.Contains(lower, "externally-managed-environment") ||
		strings.Contains(output, "externally managed") {
		return "[hint: add --break-system-packages flag to pip install]"
	}

	// apt/dpkg lock errors — another process is running dpkg.
	if strings.Contains(lower, "could not get lock") ||
		strings.Contains(lower, "dpkg was interrupted") ||
		strings.Contains(lower, "dpkg --configure -a") {
		return "[hint: try: dpkg --configure -a && apt-get install -f]"
	}

	// Network/download errors during package installs.
	if exitCode != 0 &&
		(strings.Contains(lower, "temporary failure resolving") ||
			strings.Contains(lower, "could not resolve") ||
			strings.Contains(lower, "connection timed out") ||
			strings.Contains(lower, "connection refused") && strings.Contains(lower, "apt") ||
			strings.Contains(lower, "failed to fetch") ||
			strings.Contains(lower, "retrying") && strings.Contains(lower, "download")) {
		return "[hint: network error — this container may not have internet access. " +
			"Use only locally available packages and tools. " +
			"For Python: check if the package is already installed with 'python3 -c \"import <module>\"'. " +
			"For apt: try 'dpkg -l | grep <package>' to check installed packages]"
	}

	// Permission errors in common locations.
	if strings.Contains(lower, "permission denied") && exitCode != 0 {
		if strings.Contains(lower, "/usr/") || strings.Contains(lower, "/etc/") ||
			strings.Contains(lower, "/var/") {
			return "[hint: try running with sudo or use --user flag for pip]"
		}
	}

	// Disk space errors — clean up or use /tmp.
	if strings.Contains(lower, "no space left on device") {
		return "[hint: no disk space — clean up with: rm -rf /tmp/* __pycache__ *.pyc .cache build/ dist/ node_modules/.cache; or write output to /tmp/]"
	}

	// Read-only filesystem — common in some container setups.
	if strings.Contains(lower, "read-only file system") {
		return "[hint: read-only filesystem — try writing to /tmp/ or /app/ instead]"
	}

	// Segfault in Python (common with numpy/scipy compiled extensions).
	if strings.Contains(lower, "segmentation fault") && (strings.Contains(lower, "python") || strings.Contains(output, "python")) {
		return "[hint: Python segfault — likely a compiled extension issue. Try: pip install --break-system-packages --force-reinstall numpy scipy, or use pure Python alternatives]"
	}

	return ""
}

// signalHint detects when a process was killed by a signal and provides guidance.
// Exit code 137 = SIGKILL (often OOM), 139 = SIGSEGV, 134 = SIGABRT.
func signalHint(exitCode int) string {
	switch exitCode {
	case 124:
		// Exit code 124 is used by the `timeout` command when it kills a process.
		return "[hint: command was killed by timeout — your solution is too slow. " +
			"Optimize: use more efficient algorithms, avoid O(n²) when O(n log n) works, " +
			"use built-in/vectorized operations (numpy, etc.), reduce data copies, " +
			"or process data in streaming fashion instead of loading all into memory]"
	case 137:
		return "[hint: process was killed (SIGKILL) — likely out of memory (OOM). " +
			"Try: (1) reduce batch size, process data in smaller chunks, " +
			"(2) use generators/iterators instead of loading all data into memory, " +
			"(3) use more memory-efficient data structures (arrays vs linked lists), " +
			"(4) reduce number of concurrent processes, " +
			"(5) check `dmesg | tail -20` — if you see 'Out of memory: Killed process', confirm OOM, " +
			"(6) for C/C++: check for memory leaks with `valgrind --tool=memcheck ./program`]"
	case 139:
		return "[hint: segmentation fault (SIGSEGV) — a memory access bug. " +
			"Debug: (1) run `valgrind --tool=memcheck ./program` to find the exact source, " +
			"(2) compile with debug symbols: `gcc -g -fsanitize=address`, " +
			"(3) common causes: array out of bounds, null pointer dereference, use-after-free, stack overflow from deep recursion, " +
			"(4) check `dmesg | tail` for kernel messages about the crash]"
	case 136:
		return "[hint: floating point exception (SIGFPE) — typically division by zero or integer overflow. " +
			"Check: (1) division operations where divisor could be 0, (2) modulo by zero, " +
			"(3) integer overflow in multiplication (use long/int64). " +
			"Add guards: `if (divisor == 0)` before every division. " +
			"In Python this would raise ZeroDivisionError instead, so this is a C/C++/Rust issue]"
	case 134:
		return "[hint: process aborted (SIGABRT) — likely an assertion failure or double-free. " +
			"Check: assert() failures, memory corruption, C++ exception in destructor]"
	case 141:
		return "[hint: process received SIGPIPE (broken pipe) — this usually happens when piping " +
			"to head, grep -m1, or a process that exits early. This is typically HARMLESS — the " +
			"program's output was likely correct before the pipe broke. If the actual output was " +
			"captured correctly, ignore this error. If you need to suppress it: redirect stderr " +
			"with 2>/dev/null or use `set +o pipefail` in bash scripts]"
	}
	return ""
}

// pythonErrorHint extracts actionable information from Python errors.
// Extracts file:line from tracebacks for ALL error types (not just syntax
// errors) and provides targeted guidance based on the error category.
func pythonErrorHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Only process if it looks like a Python traceback or error.
	// Check for "Traceback" (standard traceback), "Error:" (exception line),
	// or "Error (" (parenthesized exceptions like "Error (from subclass)").
	if !strings.Contains(output, "Error:") && !strings.Contains(output, "Error (") &&
		!strings.Contains(output, "Traceback") {
		return ""
	}

	lines := strings.Split(output, "\n")

	// Look for Python traceback patterns: "File "xxx", line N"
	// followed by the error line. Extract the LAST traceback frame
	// (innermost/most relevant).
	var lastFile, lastLine, errorType string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "File \"") {
			// Extract file and line number.
			if fileEnd := strings.Index(trimmed[6:], "\""); fileEnd > 0 {
				lastFile = trimmed[6 : 6+fileEnd]
			}
			if lineIdx := strings.Index(trimmed, "line "); lineIdx > 0 {
				rest := trimmed[lineIdx+5:]
				if commaIdx := strings.IndexAny(rest, ", \n"); commaIdx > 0 {
					lastLine = rest[:commaIdx]
				} else {
					lastLine = strings.TrimSpace(rest)
				}
			}
		}
		// Capture the error type line (the final error at the end of traceback).
		// Match common Python exception patterns.
		for _, errPrefix := range []string{
			"SyntaxError:", "IndentationError:", "TabError:",
			"TypeError:", "ValueError:", "KeyError:", "IndexError:",
			"FileNotFoundError:", "PermissionError:", "OSError:",
			"AttributeError:", "NameError:", "ImportError:",
			"ModuleNotFoundError:",
			"ZeroDivisionError:", "RuntimeError:", "StopIteration:",
			"RecursionError:", "OverflowError:", "AssertionError:",
			"UnicodeDecodeError:", "UnicodeEncodeError:",
			"NotImplementedError:", "ConnectionError:", "TimeoutError:",
			"BrokenPipeError:", "ProcessLookupError:",
			"json.decoder.JSONDecodeError:",
			// Framework-specific exceptions.
			"django.core.exceptions.", "pydantic.ValidationError:",
			"pydantic_core._pydantic_core.ValidationError:",
			"requests.exceptions.", "httpx.",
			"sqlalchemy.exc.", "celery.exceptions.",
			"fastapi.exceptions.", "starlette.exceptions.",
			"marshmallow.exceptions.", "werkzeug.exceptions.",
			"jwt.exceptions.", "paramiko.",
		} {
			if strings.Contains(trimmed, errPrefix) {
				errorType = trimmed
				break
			}
		}
		// Generic Python exception detection: catch custom/unknown exception
		// types that aren't in the hardcoded list. Python exception lines are
		// unindented and match "SomeName: message" or "module.path.Name: message".
		// Only set errorType if we haven't matched a known prefix yet, and the
		// line starts at column 0 (not indented in the traceback).
		if errorType == "" && len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			if looksLikePythonException(trimmed) {
				errorType = trimmed
			}
		}
	}

	if errorType != "" && lastFile != "" && lastLine != "" {
		// Skip files in standard library / site-packages — the error
		// is in user code, and the innermost user frame is more useful.
		// But if it's the only frame we found, still show it.
		hint := fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			truncateErrorLine(errorType, 150), lastFile, lastLine, lastLine)
		return hint
	}

	// NameError/AttributeError with a suggestion (Python 3.10+).
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if (strings.Contains(trimmed, "NameError:") || strings.Contains(trimmed, "AttributeError:")) &&
			strings.Contains(trimmed, "Did you mean:") {
			return "[hint: " + truncateErrorLine(trimmed, 150) + "]"
		}
	}

	// FileNotFoundError without traceback location — still useful to surface.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "FileNotFoundError:") {
			hint := "[hint: " + truncateErrorLine(trimmed, 150) + " — check the file path exists and is spelled correctly"
			// Common TB2 issue: running from wrong directory.
			if strings.Contains(trimmed, "input_data") || strings.Contains(trimmed, "task_file") {
				hint += ". If running a script, try: cd /app && python3 script.py, or use absolute paths"
			}
			hint += "]"
			return hint
		}
	}

	return ""
}

// looksLikePythonException checks if a line looks like a Python exception.
// Python exception lines follow the pattern: "ExceptionName: message" or
// "module.path.ExceptionName: message". The exception name must start with
// a capital letter. This catches custom/third-party exceptions that aren't
// in the hardcoded known-prefix list.
func looksLikePythonException(line string) bool {
	colonIdx := strings.Index(line, ": ")
	if colonIdx <= 0 || colonIdx > 100 {
		return false
	}
	name := line[:colonIdx]
	// Must start with a capital letter or module path (dotted name ending in capital).
	if len(name) == 0 {
		return false
	}
	// Reject lines that are clearly not exceptions: shell commands, paths, URLs.
	if strings.ContainsAny(name, " /\\=<>()[]{}\"'`@#$%^&*+~|") {
		return false
	}
	// For dotted names (module.path.ExceptionName), check the last segment.
	lastDot := strings.LastIndex(name, ".")
	var className string
	if lastDot >= 0 {
		className = name[lastDot+1:]
	} else {
		className = name
	}
	// Exception class names start with a capital letter and contain only letters/digits.
	if len(className) == 0 || className[0] < 'A' || className[0] > 'Z' {
		return false
	}
	for _, c := range className {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// truncateErrorLine shortens an error line for hint display.
func truncateErrorLine(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// pipInstallHint detects pip, poetry, and uv install failures and suggests
// fixes. These are common in eval containers and unfamiliar environments.
func pipInstallHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Build failures during pip install (missing C compiler, headers).
	if (strings.Contains(output, "pip install") || strings.Contains(output, "pip3 install") ||
		strings.Contains(output, "uv pip install")) &&
		(strings.Contains(output, "error: command") || strings.Contains(output, "Failed building wheel")) {
		return "[hint: package build failed — install build dependencies: " +
			"apt-get install -y build-essential python3-dev, then retry. " +
			"Or try installing a pre-built wheel: pip install --only-binary :all: <package>]"
	}

	// Version conflict.
	if strings.Contains(output, "ResolutionImpossible") || strings.Contains(output, "version conflict") ||
		strings.Contains(output, "incompatible versions") {
		return "[hint: dependency version conflict — try: pip install --use-deprecated=legacy-resolver <package>, " +
			"or pin specific versions to resolve conflicts]"
	}

	// No matching distribution (wrong Python version or platform).
	if strings.Contains(output, "No matching distribution") || strings.Contains(output, "Could not find a version") {
		return "[hint: package not found for this Python version or platform — " +
			"check: python3 --version, and verify the package name is correct. " +
			"Some packages have different names: e.g., Pillow not PIL, opencv-python not cv2]"
	}

	// Externally managed environment (PEP 668 — common on newer distros).
	if strings.Contains(output, "externally-managed-environment") {
		return "[hint: PEP 668 managed environment — use: python3 -m venv .venv && source .venv/bin/activate, " +
			"then install packages. Or use: pip install --break-system-packages <package> (not recommended)]"
	}

	return ""
}

// compilationErrorHint extracts the first file:line and error message from
// C/C++, Go, and Rust compiler errors so the agent can jump directly to the
// error location AND understand what's wrong without re-reading the output.
// Saves 1-2 turns of the agent reading the full error output and figuring out
// which file and line to view/edit.
func compilationErrorHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Linker errors: "undefined reference to 'foo'", "multiple definition",
	// "cannot find -l" — suggest common -l flags and fixes.
	// These save 1-2 turns of the agent figuring out which library to link.
	lower := strings.ToLower(output)
	if strings.Contains(lower, "undefined reference to") ||
		strings.Contains(lower, "multiple definition of") ||
		strings.Contains(lower, "cannot find -l") {
		if hint := linkerHint(output); hint != "" {
			return hint
		}
	}

	// Missing header file: suggest the apt package to install.
	if (strings.Contains(output, "fatal error") || strings.Contains(output, "error:")) &&
		strings.Contains(output, "No such file or directory") {
		if hint := missingHeaderHint(output); hint != "" {
			return hint
		}
	}

	lines := strings.Split(output, "\n")

	// Kotlin: "e: file.kt: (42, 5): message" or "e: file:///path.kt:42:5 message"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "e: ") {
			continue
		}
		rest := trimmed[3:] // strip "e: "
		// Format 1: "file.kt: (42, 5): message"
		if parenIdx := strings.Index(rest, ": ("); parenIdx > 0 {
			file := rest[:parenIdx]
			afterParen := rest[parenIdx+3:]
			closeIdx := strings.Index(afterParen, ")")
			if closeIdx > 0 {
				coords := afterParen[:closeIdx]
				parts := strings.SplitN(coords, ",", 2)
				if len(parts) >= 1 {
					lineNum := strings.TrimSpace(parts[0])
					if isNumeric(lineNum) {
						errMsg := ""
						if msgStart := strings.Index(afterParen[closeIdx:], ": "); msgStart >= 0 {
							errMsg = strings.TrimSpace(afterParen[closeIdx+msgStart+2:])
						}
						if errMsg != "" {
							return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
								truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
						}
						return fmt.Sprintf("[hint: Kotlin error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
							file, lineNum, lineNum)
					}
				}
			}
		}
		// Format 2: "file.kt:42:5 message" (no parens)
		colonParts := strings.SplitN(rest, ":", 4)
		if len(colonParts) >= 3 && isNumeric(colonParts[1]) {
			file := colonParts[0]
			lineNum := colonParts[1]
			errMsg := ""
			if len(colonParts) >= 4 {
				errMsg = strings.TrimSpace(colonParts[3])
			} else if len(colonParts) == 3 && !isNumeric(colonParts[2]) {
				errMsg = strings.TrimSpace(colonParts[2])
			}
			if errMsg != "" {
				return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
					truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
			}
			return fmt.Sprintf("[hint: Kotlin error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				file, lineNum, lineNum)
		}
	}

	// Nim: "file.nim(42, 5) Error: undeclared identifier: 'foo'"
	// Format: file(line, col) Error: message
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		parenIdx := strings.Index(trimmed, "(")
		if parenIdx <= 0 {
			continue
		}
		closeIdx := strings.Index(trimmed[parenIdx:], ")")
		if closeIdx <= 0 {
			continue
		}
		closeIdx += parenIdx
		after := trimmed[closeIdx+1:]
		// Nim uses " Error:" (space + capital E) after the closing paren.
		if !strings.HasPrefix(after, " Error:") {
			continue
		}
		file := trimmed[:parenIdx]
		if len(file) > 200 || !strings.HasSuffix(file, ".nim") {
			continue
		}
		coords := trimmed[parenIdx+1 : closeIdx]
		parts := strings.SplitN(coords, ",", 2)
		if len(parts) < 1 || !isNumeric(strings.TrimSpace(parts[0])) {
			continue
		}
		lineNum := strings.TrimSpace(parts[0])
		errMsg := strings.TrimSpace(strings.TrimPrefix(after, " Error:"))
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Nim error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// OCaml: 'File "src/main.ml", line 42, characters 5-10:' followed by 'Error: message'
	// The file reference and error message are on separate lines.
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "File \"") || !strings.Contains(trimmed, "\", line ") {
			continue
		}
		// Extract file path: between 'File "' and '"'
		fileStart := len("File \"")
		fileEnd := strings.Index(trimmed[fileStart:], "\"")
		if fileEnd < 0 {
			continue
		}
		file := trimmed[fileStart : fileStart+fileEnd]
		// Only match OCaml-like files (skip Python tracebacks which use similar format).
		if !strings.HasSuffix(file, ".ml") && !strings.HasSuffix(file, ".mli") {
			continue
		}
		// Extract line number: after '", line '
		lineIdx := strings.Index(trimmed, "\", line ")
		if lineIdx < 0 {
			continue
		}
		rest := trimmed[lineIdx+len("\", line "):]
		commaIdx := strings.IndexAny(rest, ",: ")
		lineNum := rest
		if commaIdx > 0 {
			lineNum = rest[:commaIdx]
		}
		lineNum = strings.TrimSpace(lineNum)
		if !isNumeric(lineNum) {
			continue
		}
		// Look ahead for the "Error:" line.
		errMsg := ""
		for j := i + 1; j < min(i+6, len(lines)); j++ {
			ahead := strings.TrimSpace(lines[j])
			if strings.HasPrefix(ahead, "Error:") {
				errMsg = ahead
				break
			}
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: OCaml error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Perl: "syntax error at script.pl line 42, near ..." or "Died at script.pl line 42."
	// Format: "... at FILE line N" where FILE is a .pl or .pm file.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		atIdx := strings.Index(trimmed, " at ")
		if atIdx < 0 {
			continue
		}
		after := trimmed[atIdx+4:]
		lineIdx := strings.Index(after, " line ")
		if lineIdx < 0 {
			continue
		}
		file := after[:lineIdx]
		// Only match Perl files.
		if !strings.HasSuffix(file, ".pl") && !strings.HasSuffix(file, ".pm") && !strings.HasSuffix(file, ".t") {
			continue
		}
		numStr := after[lineIdx+6:]
		endIdx := strings.IndexAny(numStr, ".,; \n")
		if endIdx > 0 {
			numStr = numStr[:endIdx]
		}
		numStr = strings.TrimSpace(numStr)
		if !isNumeric(numStr) {
			continue
		}
		// The error description is the part before "at FILE".
		errMsg := strings.TrimSpace(trimmed[:atIdx+4+0])
		if atIdx > 0 {
			errMsg = strings.TrimSpace(trimmed[:atIdx])
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, numStr, numStr)
		}
		return fmt.Sprintf("[hint: Perl error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, numStr, numStr)
	}

	// Julia: "ERROR: LoadError: ..." followed by "in expression starting at file.jl:42"
	// The error message and file location are on separate lines.
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "in expression starting at ") {
			continue
		}
		rest := strings.TrimPrefix(trimmed, "in expression starting at ")
		colonParts := strings.SplitN(rest, ":", 2)
		if len(colonParts) < 2 || !isNumeric(colonParts[1]) {
			continue
		}
		file := colonParts[0]
		lineNum := colonParts[1]
		if !strings.HasSuffix(file, ".jl") || len(file) > 200 {
			continue
		}
		// Look back for the "ERROR:" line to extract the error message.
		errMsg := ""
		for k := i - 1; k >= max(0, i-5); k-- {
			prev := strings.TrimSpace(lines[k])
			if strings.HasPrefix(prev, "ERROR:") {
				errMsg = prev
				break
			}
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Julia error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Ruby syntax/parse errors: "file.rb:42: syntax error, unexpected ..."
	// The C/C++ parser misses these because it looks for ": error" but Ruby uses ": syntax error".
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, ": syntax error") {
			continue
		}
		colonParts := strings.SplitN(trimmed, ":", 3)
		if len(colonParts) < 3 || !isNumeric(colonParts[1]) {
			continue
		}
		file := colonParts[0]
		lineNum := colonParts[1]
		if len(file) > 200 {
			continue
		}
		errMsg := strings.TrimSpace(colonParts[2])
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Ruby syntax error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Lua runtime/compile errors: "lua: file.lua:42: message" or "luac: file.lua:42: message"
	// Also handles LuaJIT: "luajit: file.lua:42: message"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		prefix := ""
		if strings.HasPrefix(trimmed, "lua: ") {
			prefix = "lua: "
		} else if strings.HasPrefix(trimmed, "luac: ") {
			prefix = "luac: "
		} else if strings.HasPrefix(trimmed, "luajit: ") {
			prefix = "luajit: "
		}
		if prefix == "" {
			continue
		}
		rest := trimmed[len(prefix):]
		colonParts := strings.SplitN(rest, ":", 3)
		if len(colonParts) < 3 || !isNumeric(colonParts[1]) {
			continue
		}
		file := colonParts[0]
		lineNum := colonParts[1]
		if len(file) > 200 {
			continue
		}
		errMsg := strings.TrimSpace(colonParts[2])
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Lua error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// D language (DMD): "file.d(42): Error: undefined identifier `foo`"
	// Format: file(line): Error: message (no column number)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		parenIdx := strings.Index(trimmed, "(")
		if parenIdx <= 0 {
			continue
		}
		closeIdx := strings.Index(trimmed[parenIdx:], ")")
		if closeIdx <= 0 {
			continue
		}
		closeIdx += parenIdx
		after := trimmed[closeIdx+1:]
		if !strings.HasPrefix(after, ": Error:") {
			continue
		}
		file := trimmed[:parenIdx]
		if len(file) > 200 || !strings.HasSuffix(file, ".d") {
			continue
		}
		lineStr := trimmed[parenIdx+1 : closeIdx]
		if !isNumeric(strings.TrimSpace(lineStr)) {
			continue
		}
		lineNum := strings.TrimSpace(lineStr)
		errMsg := strings.TrimSpace(strings.TrimPrefix(after, ": Error:"))
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: D error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Scala 3: "-- [E007] Type Mismatch Error: file.scala:42:5 ---"
	// or "-- Error: file.scala:42:5 ---"
	// Format: starts with "-- " and contains a file:line:col reference.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-- ") {
			continue
		}
		if !strings.Contains(trimmed, "Error") {
			continue
		}
		// Extract file:line:col from the error line.
		// Pattern: "Error: file.scala:42:5" followed by " ---" dashes
		errIdx := strings.Index(trimmed, "Error:")
		if errIdx < 0 {
			continue
		}
		// For "[EXXXX] Some Error:" format, the file ref follows the last "Error:".
		lastErrIdx := strings.LastIndex(trimmed, "Error:")
		if lastErrIdx < 0 {
			continue
		}
		rest := strings.TrimSpace(trimmed[lastErrIdx+len("Error:"):])
		// Strip trailing dashes.
		if dashIdx := strings.Index(rest, " ---"); dashIdx > 0 {
			rest = strings.TrimSpace(rest[:dashIdx])
		} else if dashIdx := strings.Index(rest, " -"); dashIdx > 0 {
			rest = strings.TrimSpace(rest[:dashIdx])
		}
		// Parse file:line:col.
		colonParts := strings.SplitN(rest, ":", 3)
		if len(colonParts) >= 2 && isNumeric(colonParts[1]) {
			file := colonParts[0]
			lineNum := colonParts[1]
			if len(file) > 200 {
				continue
			}
			// Extract the error type from the prefix.
			prefix := strings.TrimSpace(trimmed[3:lastErrIdx])
			prefix = strings.TrimRight(prefix, " ")
			errLabel := "Scala error"
			if prefix != "" {
				// e.g., "[E007] Type Mismatch " → "Type Mismatch"
				if bracketEnd := strings.Index(prefix, "] "); bracketEnd > 0 {
					prefix = strings.TrimSpace(prefix[bracketEnd+2:])
				}
				if prefix != "" {
					errLabel = strings.TrimSpace(prefix)
				}
			}
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errLabel, 120), file, lineNum, lineNum)
		}
	}

	// Fortran (gfortran): error message is on a separate line from file:line:col.
	// Format:
	//   file.f90:42:5:
	//       42 |   call foo(x, y)
	//          |     1
	//   Error: Symbol 'foo' at (1) has no IMPLICIT type
	// The file:line:col line ends with ":" but doesn't contain "error".
	// We match standalone "Error:" lines and search backwards for the file reference.
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Error:") && !strings.HasPrefix(trimmed, "Fatal Error:") {
			continue
		}
		errMsg := trimmed
		// Search backwards for "file:line:col:" reference (within 5 lines).
		for k := i - 1; k >= max(0, i-5); k-- {
			prev := strings.TrimSpace(lines[k])
			colonParts := strings.SplitN(prev, ":", 4)
			if len(colonParts) >= 3 && isNumeric(colonParts[1]) {
				file := colonParts[0]
				lineNum := colonParts[1]
				if len(file) > 200 {
					continue
				}
				// Verify it looks like a Fortran file or at least a valid path.
				return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
					truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
			}
		}
	}

	// TypeScript (tsc): "src/index.ts(42,5): error TS2322: message"
	// C#/MSBuild:       "Program.cs(5,17): error CS0029: message"
	// VB.NET:           "Module1.vb(3,5): error BC30451: message"
	// Format: file(line,col): error XXxxxx: message
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		parenIdx := strings.Index(trimmed, "(")
		if parenIdx <= 0 {
			continue
		}
		// Check for MSBuild-style error pattern after the closing paren.
		closeIdx := strings.Index(trimmed[parenIdx:], ")")
		if closeIdx <= 0 {
			continue
		}
		closeIdx += parenIdx
		after := trimmed[closeIdx+1:]
		if !strings.HasPrefix(after, ": error ") {
			continue
		}
		// Verify it looks like a structured error code (TS/CS/BC + digits).
		errRest := after[len(": error "):]
		if len(errRest) < 3 || errRest[0] < 'A' || errRest[0] > 'Z' {
			continue
		}
		// Extract file, line, error message.
		file := trimmed[:parenIdx]
		if len(file) > 200 {
			continue
		}
		coords := trimmed[parenIdx+1 : closeIdx]
		parts := strings.SplitN(coords, ",", 2)
		if len(parts) < 1 || !isNumeric(parts[0]) {
			continue
		}
		lineNum := parts[0]
		errMsg := strings.TrimPrefix(after, ": ")
		return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
	}

	// GHC (Haskell): error message is on the NEXT line(s), indented.
	// Modern format:
	//   Main.hs:42:5: error: [GHC-88464]
	//       Variable not in scope: fooBar :: Int -> Bool
	// Or without error code:
	//   Main.hs:42:5: error:
	//       • Could not deduce (Num String) arising from a use of '+'
	// Older format (no "error:" keyword):
	//   Main.hs:42:5:
	//       Not in scope: 'foo'
	// Must run BEFORE the generic C/C++/Go parser which would catch "file.hs:42:5: error:"
	// but extract only "error:" as the message (useless).
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match file.hs:line:col: pattern (Haskell source files).
		if !strings.HasSuffix(strings.SplitN(trimmed, ":", 2)[0], ".hs") &&
			!strings.HasSuffix(strings.SplitN(trimmed, ":", 2)[0], ".lhs") {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 4)
		if len(parts) < 3 || !isNumeric(parts[1]) {
			continue
		}
		file := parts[0]
		lineNum := parts[1]
		if len(file) > 200 {
			continue
		}
		// Check if this line ends with "error:" or "error: [GHC-XXXXX]" or just ":"
		// (older format). The actual error detail is on subsequent indented lines.
		rest := ""
		if len(parts) >= 4 {
			rest = strings.TrimSpace(parts[3])
		}
		// Skip "warning:" lines — only process errors.
		if strings.HasPrefix(rest, " warning") || strings.HasPrefix(rest, "warning") {
			continue
		}
		// Look ahead for the actual error message on indented continuation lines.
		errMsg := ""
		for j := i + 1; j < min(i+6, len(lines)); j++ {
			ahead := lines[j]
			// GHC continuation lines are indented with spaces.
			if len(ahead) == 0 || (ahead[0] != ' ' && ahead[0] != '\t') {
				break
			}
			candidate := strings.TrimSpace(ahead)
			if candidate == "" || candidate == "|" {
				continue
			}
			// Skip Unicode bullet markers and grab the message.
			candidate = strings.TrimPrefix(candidate, "• ")
			candidate = strings.TrimPrefix(candidate, "· ")
			if candidate != "" && errMsg == "" {
				errMsg = candidate
			}
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Haskell error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Clojure: "Syntax error compiling at (src/core.clj:42:5)." or
	// "Syntax error (ExceptionType) compiling at (file.clj:42:5)."
	// Also: "compiling:(file.clj:42:5)" for inline compile errors.
	if strings.Contains(lower, "compiling") && (strings.Contains(output, ".clj:") || strings.Contains(output, ".cljc:") || strings.Contains(output, ".cljs:")) {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Find the file reference in parentheses: "(file.clj:line:col)"
			parenIdx := strings.LastIndex(trimmed, "(")
			if parenIdx < 0 {
				continue
			}
			closeIdx := strings.Index(trimmed[parenIdx:], ")")
			if closeIdx <= 0 {
				continue
			}
			ref := trimmed[parenIdx+1 : parenIdx+closeIdx]
			colonParts := strings.SplitN(ref, ":", 3)
			if len(colonParts) < 2 || !isNumeric(colonParts[1]) {
				continue
			}
			file := colonParts[0]
			lineNum := colonParts[1]
			if len(file) > 200 {
				continue
			}
			// Check it's a Clojure file.
			if !strings.HasSuffix(file, ".clj") && !strings.HasSuffix(file, ".cljc") && !strings.HasSuffix(file, ".cljs") {
				continue
			}
			// Extract error type from the line.
			errMsg := trimmed
			if len(errMsg) > 150 {
				errMsg = errMsg[:150] + "..."
			}
			return fmt.Sprintf("[hint: %s — use view tool with offset=%s to see %s, then fix with edit]",
				truncateErrorLine(errMsg, 120), lineNum, file)
		}
	}

	// Elixir: "** (CompileError) lib/app.ex:42: undefined function foo/1"
	// Also: "== Compilation error in file lib/app.ex ==" followed by "** (CompileError) ..."
	// Must run before Erlang parser since Elixir stacktraces contain .erl file references.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "** (") {
			continue
		}
		// Extract after the closing paren: "** (CompileError) lib/app.ex:42: msg"
		parenClose := strings.Index(trimmed, ") ")
		if parenClose < 0 {
			continue
		}
		rest := trimmed[parenClose+2:]
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) < 2 || !isNumeric(parts[1]) {
			continue
		}
		file := parts[0]
		if len(file) > 200 || (!strings.HasSuffix(file, ".ex") && !strings.HasSuffix(file, ".exs")) {
			continue
		}
		lineNum := parts[1]
		errMsg := ""
		if len(parts) >= 3 {
			errMsg = strings.TrimSpace(parts[2])
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Elixir error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Erlang: "src/module.erl:42: function foo/1 undefined" or
	// "src/module.erl:42: head mismatch" or "src/module.erl:42: syntax error before: 'X'"
	// Format: file.erl:line: message (doesn't use ": error" like C/C++).
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		colonParts := strings.SplitN(trimmed, ":", 3)
		if len(colonParts) < 3 || !isNumeric(colonParts[1]) {
			continue
		}
		file := colonParts[0]
		if len(file) > 200 {
			continue
		}
		// Only match Erlang source files.
		if !strings.HasSuffix(file, ".erl") && !strings.HasSuffix(file, ".hrl") {
			continue
		}
		lineNum := colonParts[1]
		errMsg := strings.TrimSpace(colonParts[2])
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Erlang error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Gleam: "error: Unknown variable" followed by "  ┌─ src/main.gleam:42:5"
	// The error message is on a preceding line; the file reference uses box-drawing chars.
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match the distinctive Gleam file reference: "┌─ file:line:col"
		// The box-drawing prefix is "┌─ " (U+250C, U+2500, space).
		if !strings.Contains(trimmed, "─ ") {
			continue
		}
		// Find the file reference after "┌─ " or "┌── ".
		refIdx := strings.Index(trimmed, "─ ")
		if refIdx < 0 {
			continue
		}
		ref := strings.TrimSpace(trimmed[refIdx+len("─ "):])
		colonParts := strings.SplitN(ref, ":", 3)
		if len(colonParts) < 2 || !isNumeric(colonParts[1]) {
			continue
		}
		file := colonParts[0]
		if len(file) > 200 || !strings.HasSuffix(file, ".gleam") {
			continue
		}
		lineNum := colonParts[1]
		// Look back for the "error:" line.
		errMsg := ""
		for k := i - 1; k >= max(0, i-3); k-- {
			prev := strings.TrimSpace(lines[k])
			if strings.HasPrefix(prev, "error:") {
				errMsg = strings.TrimSpace(strings.TrimPrefix(prev, "error:"))
				break
			}
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Gleam error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// PHP: "PHP Parse error: syntax error, ... in /path/file.php on line 42"
	// Also: "PHP Fatal error: Call to undefined function ... in /path/file.php on line 42"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "PHP ") || !strings.Contains(trimmed, " in ") || !strings.Contains(trimmed, " on line ") {
			continue
		}
		// Extract "in /path/file.php on line 42".
		inIdx := strings.LastIndex(trimmed, " in ")
		if inIdx < 0 {
			continue
		}
		tail := trimmed[inIdx+4:]
		onLineIdx := strings.LastIndex(tail, " on line ")
		if onLineIdx < 0 {
			continue
		}
		file := tail[:onLineIdx]
		lineNum := strings.TrimSpace(tail[onLineIdx+9:])
		if !isNumeric(lineNum) || len(file) > 200 || !strings.HasSuffix(file, ".php") {
			continue
		}
		// Error message is between "PHP Parse error:" and " in ".
		errMsg := ""
		if colonIdx := strings.Index(trimmed, ": "); colonIdx > 0 && colonIdx < inIdx {
			errMsg = strings.TrimSpace(trimmed[colonIdx+2 : inIdx])
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: PHP error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Crystal: "Error in src/main.cr:42: undefined local variable or method 'foo'"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Error in ") {
			continue
		}
		rest := trimmed[9:] // strip "Error in "
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) < 2 || !isNumeric(parts[1]) {
			continue
		}
		file := parts[0]
		if len(file) > 200 || !strings.HasSuffix(file, ".cr") {
			continue
		}
		lineNum := parts[1]
		errMsg := ""
		if len(parts) >= 3 {
			errMsg = strings.TrimSpace(parts[2])
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Crystal error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Elm: "-- TYPE MISMATCH --------- src/Main.elm" or
	// "-- NAMING ERROR --------- src/Main.elm"
	// The file reference is at the end of a line starting with "-- "
	// followed by an error type and dashes. Line numbers appear inline
	// in the body (e.g., "42|  div [] [text model]").
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-- ") || !strings.Contains(trimmed, ".elm") {
			continue
		}
		// Extract file from end: "-- TYPE MISMATCH --- src/Main.elm"
		parts := strings.Fields(trimmed)
		if len(parts) < 3 {
			continue
		}
		file := parts[len(parts)-1]
		if !strings.HasSuffix(file, ".elm") || len(file) > 200 {
			continue
		}
		// Extract error type: everything between "-- " and the dashes/file.
		errType := ""
		for _, p := range parts[1:] {
			if strings.HasPrefix(p, "-") || strings.HasSuffix(p, ".elm") {
				break
			}
			if errType != "" {
				errType += " "
			}
			errType += p
		}
		// Look ahead for a line number reference: "42| code..."
		lineNum := ""
		for j := i + 1; j < min(i+15, len(lines)); j++ {
			ahead := strings.TrimSpace(lines[j])
			barIdx := strings.Index(ahead, "|")
			if barIdx > 0 && barIdx <= 6 {
				candidate := strings.TrimSpace(ahead[:barIdx])
				if isNumeric(candidate) {
					lineNum = candidate
					break
				}
			}
		}
		if lineNum != "" && errType != "" {
			return fmt.Sprintf("[hint: Elm %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				errType, file, lineNum, lineNum)
		}
		if errType != "" {
			return fmt.Sprintf("[hint: Elm %s in %s — open the file with view and check the error]", errType, file)
		}
	}

	// Terraform/OpenTofu: "on main.tf line 42, in resource ..." preceded by "Error: message"
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "on ") || !strings.Contains(trimmed, " line ") {
			continue
		}
		// Parse "on main.tf line 42, in ..."
		rest := trimmed[3:]
		lineIdx := strings.Index(rest, " line ")
		if lineIdx <= 0 {
			continue
		}
		file := rest[:lineIdx]
		if len(file) > 200 || (!strings.HasSuffix(file, ".tf") && !strings.HasSuffix(file, ".hcl") && !strings.HasSuffix(file, ".tofu")) {
			continue
		}
		afterLine := rest[lineIdx+6:]
		lineNum := strings.TrimRight(strings.SplitN(afterLine, ",", 2)[0], " ")
		if !isNumeric(lineNum) {
			continue
		}
		// Look back for "Error: message" line.
		errMsg := ""
		for k := i - 1; k >= max(0, i-3); k-- {
			prev := strings.TrimSpace(lines[k])
			if strings.HasPrefix(prev, "Error:") || strings.HasPrefix(prev, "Warning:") {
				errMsg = strings.TrimSpace(prev)
				break
			}
		}
		if errMsg != "" {
			return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
		}
		return fmt.Sprintf("[hint: Terraform error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// Nix: "error: ... at /path/file.nix:42:5:" or
	// "error: ... at «string»:42:5:" for inline nix expressions.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "at ") || !strings.Contains(trimmed, ".nix:") {
			continue
		}
		ref := trimmed[3:]
		// Strip trailing colon if present.
		ref = strings.TrimRight(ref, ":")
		colonParts := strings.SplitN(ref, ":", 3)
		if len(colonParts) < 2 || !isNumeric(colonParts[1]) {
			continue
		}
		file := colonParts[0]
		if len(file) > 200 {
			continue
		}
		lineNum := colonParts[1]
		return fmt.Sprintf("[hint: Nix error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
			file, lineNum, lineNum)
	}

	// C/C++/clang: "file.c:42:5: error: ..."
	// Go: "./main.go:42:5: ..." or "main.go:42:5: ..."
	// Rust (cargo): " --> src/main.rs:42:5"
	// Solidity (solc/forge): " --> contracts/Token.sol:42:5"
	// Dart: "lib/main.dart:42:5: Error: ..."
	// Java (javac): "Main.java:42: error: ..."
	// Swift: "main.swift:10:5: error: ..."
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Rust/Solidity: " --> file:line:col"
		// Error message is on the preceding line: "error[EXXXX]: msg" (Rust)
		// or "Error (XXXX): msg" (Solidity).
		if strings.HasPrefix(trimmed, "--> ") {
			rest := strings.TrimPrefix(trimmed, "--> ")
			parts := strings.SplitN(rest, ":", 3)
			if len(parts) >= 2 && isNumeric(parts[1]) {
				// Look back for the error line that precedes " -->"
				errMsg := ""
				for k := i - 1; k >= max(0, i-3); k-- {
					prev := strings.TrimSpace(lines[k])
					if (strings.HasPrefix(prev, "error") || strings.HasPrefix(prev, "Error")) && strings.Contains(prev, ":") {
						errMsg = prev
						break
					}
				}
				if errMsg != "" {
					return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
						truncateErrorLine(errMsg, 120), parts[0], parts[1], parts[1])
				}
				return fmt.Sprintf("[hint: error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
					parts[0], parts[1], parts[1])
			}
		}

		// C/C++/Go/Dart/Java/Swift: "file:line:col: error:" or "file:line: error:"
		if !strings.Contains(trimmed, ": error") && !strings.Contains(trimmed, ": Error") &&
			!strings.Contains(trimmed, ": fatal error") &&
			!strings.Contains(trimmed, ": cannot ") && !strings.Contains(trimmed, ": undefined") &&
			!strings.Contains(trimmed, "cannot find") {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 4)
		if len(parts) >= 3 && isNumeric(parts[1]) {
			file := parts[0]
			line := parts[1]
			// Skip very long file paths (probably not real file references)
			if len(file) > 200 {
				continue
			}
			// Extract the error message portion after file:line:col.
			// For "file.c:42:5: error: undeclared identifier 'x'", extract "error: undeclared identifier 'x'"
			errMsg := ""
			if len(parts) >= 4 {
				// parts[3] contains everything after file:line:col:
				errMsg = strings.TrimSpace(parts[3])
				// For "col: error: msg", skip the col part.
				if colIdx := strings.Index(errMsg, ": "); colIdx > 0 && colIdx < 8 {
					errMsg = strings.TrimSpace(errMsg[colIdx+2:])
				}
			}
			if errMsg != "" {
				return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
					truncateErrorLine(errMsg, 120), file, line, line)
			}
			return fmt.Sprintf("[hint: error at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
				file, line, line)
		}
	}

	return ""
}

// linkerHint maps "undefined reference to" errors to the right -l flags.
// These are the most common linker errors in C/C++ tasks.
func linkerHint(output string) string {
	lower := strings.ToLower(output)

	// "multiple definition of `X'" — same symbol defined in multiple files.
	// Check first since it's a distinct error type from undefined reference.
	if strings.Contains(lower, "multiple definition of") {
		return "[hint: LINKER ERROR — multiple definition means the same function/variable is defined in more than one file. " +
			"Fix: (1) move the definition to one .c file and use 'extern' declarations in headers, " +
			"(2) use 'static' for file-local functions, (3) use include guards (#ifndef) in headers]"
	}

	// "ld: cannot find -lX" — missing library. Check before library-specific
	// hints since library names (e.g., "ncurses") can appear in both patterns.
	if strings.Contains(lower, "cannot find -l") {
		idx := strings.Index(lower, "cannot find -l")
		if idx >= 0 {
			rest := lower[idx+len("cannot find -l"):]
			end := strings.IndexAny(rest, " \n\r\t")
			lib := rest
			if end > 0 {
				lib = rest[:end]
			}
			return fmt.Sprintf("[hint: LINKER ERROR — library '%s' not found. Try: apt-get install -y lib%s-dev]", lib, lib)
		}
	}

	// Map function names to libraries for undefined reference errors.
	libHints := []struct {
		patterns []string
		flag     string
		desc     string
	}{
		{[]string{"pthread_create", "pthread_join", "pthread_mutex"}, "-lpthread", "pthread functions"},
		{[]string{"'sin'", "'cos'", "'tan'", "'sqrt'", "'pow'", "'log'", "'exp'", "'fabs'", "'ceil'", "'floor'", "'round'"}, "-lm", "math functions"},
		{[]string{"dlopen", "dlsym", "dlclose"}, "-ldl", "dynamic loading functions"},
		{[]string{"curl_easy", "curl_global"}, "-lcurl", "libcurl functions"},
		{[]string{"ssl_", "ssl_new", "ssl_ctx"}, "-lssl -lcrypto", "OpenSSL functions"},
		// jpeg/png before zlib — zlib's "compress" substring-matches "jpeg_start_compress".
		{[]string{"jpeg_start_compress", "jpeg_create_compress", "jpeg_create_decompress"}, "-ljpeg", "libjpeg functions"},
		{[]string{"png_create_write_struct", "png_create_read_struct", "png_init_io"}, "-lpng", "libpng functions"},
		{[]string{"deflate", "inflate", "compress", "uncompress"}, "-lz", "zlib functions"},
		{[]string{"sqlite3_"}, "-lsqlite3", "SQLite functions"},
		{[]string{"readline"}, "-lreadline", "readline functions"},
		{[]string{"ncurses", "initscr", "endwin", "printw", "mvprintw"}, "-lncurses", "ncurses functions"},
		{[]string{"clock_gettime", "timer_create", "timer_settime", "shm_open"}, "-lrt", "POSIX realtime functions"},
		{[]string{"__gmpz_init", "mpz_init", "mpz_set", "mpq_init"}, "-lgmp", "GMP (arbitrary precision) functions"},
		{[]string{"snd_pcm_open", "snd_mixer_open", "snd_ctl_open"}, "-lasound", "ALSA audio functions"},
		{[]string{"pcap_open_live", "pcap_lookupdev", "pcap_compile"}, "-lpcap", "libpcap packet capture functions"},
		{[]string{"xmlparsefile", "xmlnewdoc", "xmlfreedoc", "xmlreadfile"}, "-lxml2", "libxml2 functions"},
		{[]string{"ft_init_freetype", "ft_new_face", "ft_load_glyph"}, "-lfreetype", "FreeType font functions"},
	}

	for _, lh := range libHints {
		for _, p := range lh.patterns {
			if strings.Contains(lower, p) {
				return fmt.Sprintf("[hint: undefined reference to %s — add %s to your link flags (e.g., gcc ... %s)]",
					lh.desc, lh.flag, lh.flag)
			}
		}
	}

	// Generic linker error hint.
	return "[hint: undefined reference — check that all required source files are compiled and linked. Common fixes: add -lm (math), -lpthread (threads), -lz (zlib)]"
}

// missingHeaderHint maps missing header files to apt packages.
// Saves 1-2 turns of the agent searching for the right package.
func missingHeaderHint(output string) string {
	// Common header → apt package mappings for Debian/Ubuntu containers.
	headerPkgs := map[string]string{
		"curl/curl.h":             "libcurl4-openssl-dev",
		"openssl/ssl.h":           "libssl-dev",
		"openssl/evp.h":           "libssl-dev",
		"zlib.h":                  "zlib1g-dev",
		"png.h":                   "libpng-dev",
		"jpeglib.h":               "libjpeg-dev",
		"sqlite3.h":               "libsqlite3-dev",
		"ncurses.h":               "libncurses-dev",
		"curses.h":                "libncurses-dev",
		"readline/readline.h":     "libreadline-dev",
		"uuid/uuid.h":             "uuid-dev",
		"X11/Xlib.h":              "libx11-dev",
		"SDL2/SDL.h":              "libsdl2-dev",
		"glib.h":                  "libglib2.0-dev",
		"ffi.h":                   "libffi-dev",
		"pcre.h":                  "libpcre3-dev",
		"yaml.h":                  "libyaml-dev",
		"jansson.h":               "libjansson-dev",
		"event.h":                 "libevent-dev",
		"boost/":                  "libboost-all-dev",
		"mysql/mysql.h":           "libmysqlclient-dev",
		"postgresql/libpq-fe.h":   "libpq-dev",
		"libpq-fe.h":              "libpq-dev",
		"gsl/":                    "libgsl-dev",
		"tiff.h":                  "libtiff-dev",
		"lzma.h":                  "liblzma-dev",
		"bzlib.h":                 "libbz2-dev",
		"expat.h":                 "libexpat1-dev",
		"MagickWand/":             "libmagickwand-dev",
		"cairo.h":                 "libcairo2-dev",
		"lapacke.h":               "liblapack-dev",
		"gmp.h":                   "libgmp-dev",
		"mpfr.h":                  "libmpfr-dev",
		"alsa/asoundlib.h":        "libasound2-dev",
		"pcap.h":                  "libpcap-dev",
		"pcap/pcap.h":             "libpcap-dev",
		"libxml/parser.h":         "libxml2-dev",
		"libxml/tree.h":           "libxml2-dev",
		"ft2build.h":              "libfreetype-dev",
		"sndfile.h":               "libsndfile1-dev",
		"hdf5.h":                  "libhdf5-dev",
		"archive.h":               "libarchive-dev",
		"X11/extensions/Xrandr.h": "libxrandr-dev",
		"X11/Xft/Xft.h":           "libxft-dev",
		"netcdf.h":                "libnetcdf-dev",
		"pcre2.h":                 "libpcre2-dev",
		"cblas.h":                 "libopenblas-dev",
		"openblas/":               "libopenblas-dev",
	}

	for header, pkg := range headerPkgs {
		if strings.Contains(output, header) {
			return fmt.Sprintf("[hint: missing header %s — install with: apt-get install -y %s]", header, pkg)
		}
	}
	return ""
}

// isNumeric returns true if the string contains only digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// jsonErrorHint extracts position info from JSON decode errors. Many TB2 tasks
// involve creating JSON output files, and a decode error with "line X column Y"
// saves the agent from having to count characters manually.
func jsonErrorHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Python: json.decoder.JSONDecodeError: Expecting ',' delimiter: line 5 column 3 (char 42)
	if idx := strings.Index(output, "JSONDecodeError:"); idx >= 0 {
		rest := output[idx:]
		if end := strings.IndexAny(rest, "\n\r"); end > 0 {
			errLine := strings.TrimSpace(rest[:end])
			return "[hint: " + errLine + " — check the JSON file at that line/column for missing commas, brackets, or quotes]"
		}
		if len(rest) < 200 {
			return "[hint: " + strings.TrimSpace(rest) + " — check JSON syntax]"
		}
	}

	// Node.js: SyntaxError: Unexpected token } in JSON at position 42
	if strings.Contains(output, "SyntaxError:") && strings.Contains(output, "JSON") {
		for _, line := range strings.Split(output, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "SyntaxError:") && strings.Contains(trimmed, "JSON") {
				return "[hint: " + trimmed + " — check JSON syntax at that position]"
			}
		}
	}

	// jq: parse error (Invalid numeric literal at line 3, column 1)
	if strings.Contains(output, "parse error") && strings.Contains(output, "jq") {
		for _, line := range strings.Split(output, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "parse error") {
				return "[hint: " + trimmed + "]"
			}
		}
	}

	return ""
}

// yamlErrorHint detects YAML and TOML parsing errors. These are common when
// editing config files (Docker Compose, Kubernetes, pyproject.toml, etc.)
// and the error messages are often confusing without pointing at the exact issue.
func yamlErrorHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Python PyYAML: yaml.scanner.ScannerError, yaml.parser.ParserError
	if strings.Contains(output, "yaml.scanner.ScannerError") || strings.Contains(output, "yaml.parser.ParserError") {
		return "[hint: YAML syntax error — common causes: (1) wrong indentation (YAML uses spaces, not tabs), " +
			"(2) missing colon after key, (3) unquoted special characters (use quotes around strings with :, #, {, }, [, ])]"
	}

	// Ruby/YAML: Psych::SyntaxError
	if strings.Contains(output, "Psych::SyntaxError") || strings.Contains(output, "Psych::BadAlias") {
		return "[hint: YAML syntax error — check indentation (must be spaces, not tabs) and quoting of special characters]"
	}

	// Go yaml.v3: "yaml: line N: ..."
	if strings.Contains(output, "yaml: line") {
		for _, line := range strings.Split(output, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "yaml: line") {
				return "[hint: " + trimmed + " — check indentation and YAML syntax at that line]"
			}
		}
	}

	// Node.js: YAMLException
	if strings.Contains(output, "YAMLException") || strings.Contains(output, "YAMLSemanticError") || strings.Contains(output, "YAMLSyntaxError") {
		return "[hint: YAML parsing error — check: (1) indentation uses spaces not tabs, " +
			"(2) colons followed by space, (3) strings with special chars are quoted]"
	}

	// docker-compose/docker compose YAML errors
	if (strings.Contains(output, "docker") || strings.Contains(output, "compose")) &&
		(strings.Contains(output, "yaml:") || strings.Contains(output, "YAML")) {
		return "[hint: Docker Compose YAML error — check indentation and structure. " +
			"Common issues: wrong indent level for services/volumes/networks, missing colons, tab characters]"
	}

	// TOML errors: common with pyproject.toml, Cargo.toml, etc.
	if strings.Contains(output, "toml") || strings.Contains(output, "TOML") {
		lower := strings.ToLower(output)
		if strings.Contains(lower, "toml") && (strings.Contains(lower, "error") || strings.Contains(lower, "invalid") || strings.Contains(lower, "expected")) {
			return "[hint: TOML syntax error — common causes: (1) missing quotes around string values, " +
				"(2) wrong bracket nesting for tables/arrays, (3) trailing commas (not allowed in TOML)]"
		}
	}

	return ""
}

// dockerHint detects common Docker and container runtime errors. These waste
// 2-3 turns as agents debug daemon connectivity, image pulls, or port conflicts.
func dockerHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	lower := strings.ToLower(output)

	// Docker daemon not running.
	if strings.Contains(lower, "cannot connect to the docker daemon") ||
		strings.Contains(lower, "is the docker daemon running") {
		return "[hint: Docker daemon is not running. Try: sudo service docker start, " +
			"or: sudo systemctl start docker. In some containers, Docker-in-Docker is not available]"
	}

	// Permission denied on docker socket.
	if strings.Contains(output, "permission denied") && strings.Contains(lower, "docker.sock") {
		return "[hint: Docker socket permission denied — try: sudo docker <command>, " +
			"or add user to docker group: sudo usermod -aG docker $USER]"
	}

	// Image not found / pull failed.
	if strings.Contains(output, "manifest unknown") || strings.Contains(output, "not found: manifest unknown") ||
		(strings.Contains(lower, "pull") && strings.Contains(lower, "not found")) {
		return "[hint: Docker image not found — check the image name and tag. " +
			"Use: docker search <name> to find available images, or check the registry URL]"
	}

	// Port already in use during container start.
	if strings.Contains(lower, "docker") && strings.Contains(lower, "port is already allocated") {
		return "[hint: Docker port conflict — the host port is already in use. " +
			"Either stop the conflicting container: docker ps && docker stop <id>, " +
			"or use a different host port: -p <other_port>:<container_port>]"
	}

	// Dockerfile build errors.
	if strings.Contains(output, "COPY failed:") || strings.Contains(output, "ADD failed:") {
		return "[hint: Docker COPY/ADD failed — the source file/directory doesn't exist in the build context. " +
			"Check that the file path is relative to the Dockerfile's directory, and isn't excluded by .dockerignore]"
	}

	// No space left on device during docker build/run.
	if strings.Contains(lower, "docker") && strings.Contains(lower, "no space left on device") {
		return "[hint: Docker out of disk space — try: docker system prune -f to remove unused images/containers, " +
			"or: docker builder prune to clear build cache]"
	}

	return ""
}

// encodingErrorHint detects Python UnicodeDecodeError/UnicodeEncodeError and
// provides the fix. These waste 1-2 turns as the agent figures out encoding.
func encodingErrorHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	if strings.Contains(output, "UnicodeDecodeError:") {
		return "[hint: UnicodeDecodeError — add encoding='utf-8' and errors='replace' to open() calls, " +
			"or use: open(file, 'r', encoding='utf-8', errors='replace'). " +
			"For binary files, use open(file, 'rb')]"
	}
	if strings.Contains(output, "UnicodeEncodeError:") {
		return "[hint: UnicodeEncodeError — add encoding='utf-8' to open() calls for writing, " +
			"or encode strings with .encode('utf-8', errors='replace')]"
	}
	return ""
}

// permissionHint detects "Permission denied" on executable scripts (exit code 126)
// and suggests chmod +x. Saves 1 turn of troubleshooting.
func permissionHint(output string, exitCode int) string {
	if exitCode == 126 {
		return "[hint: permission denied — the script is not executable. Run: chmod +x <script_path>, then retry]"
	}
	// Also catch "Permission denied" when trying to run a script directly.
	if exitCode != 0 && strings.Contains(output, "Permission denied") {
		lower := strings.ToLower(output)
		// Check if it's a script execution issue (not a file access issue).
		if strings.Contains(lower, ".sh") || strings.Contains(lower, ".py") ||
			strings.Contains(lower, "./") {
			return "[hint: permission denied — try: chmod +x <script>, then retry. Or run with: bash <script> or python3 <script>]"
		}
	}
	return ""
}

// addressInUseHint detects "Address already in use" errors from server processes.
func addressInUseHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	if strings.Contains(output, "Address already in use") || strings.Contains(output, "EADDRINUSE") {
		return "[hint: port already in use — find the process with: lsof -i :<port> or ss -tlnp | grep <port>, " +
			"then kill it with: kill <pid>. Or use a different port.]"
	}
	return ""
}

// connectionRefusedHint detects "Connection refused" errors in non-package-install
// contexts. Common in service tasks where tests try to connect before a server is
// ready, or the service failed to start, or it's listening on the wrong port.
// This saves 1-2 turns of the agent debugging why tests can't connect.
func connectionRefusedHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "connection refused") && !strings.Contains(output, "ECONNREFUSED") {
		return ""
	}
	// Skip if this is a package install error (handled by transientErrorHint).
	if strings.Contains(lower, "apt") || strings.Contains(lower, "pip") ||
		strings.Contains(lower, "npm") || strings.Contains(lower, "gem") {
		return ""
	}

	// Extract port number from common patterns.
	port := ""
	for _, prefix := range []string{"localhost:", "127.0.0.1:", "0.0.0.0:"} {
		idx := strings.Index(lower, prefix)
		if idx >= 0 {
			start := idx + len(prefix)
			end := start
			for end < len(lower) && lower[end] >= '0' && lower[end] <= '9' {
				end++
			}
			if end > start && end-start <= 5 {
				port = lower[start:end]
				break
			}
		}
	}
	// Also try curl-style "localhost port NNNN" or "host port NNNN".
	if port == "" {
		if idx := strings.Index(lower, " port "); idx >= 0 {
			start := idx + 6 // len(" port ")
			end := start
			for end < len(lower) && lower[end] >= '0' && lower[end] <= '9' {
				end++
			}
			if end > start && end-start <= 5 {
				port = lower[start:end]
			}
		}
	}
	// Also try ":PORT" from ECONNREFUSED messages like "connect ECONNREFUSED 127.0.0.1:8080"
	if port == "" {
		if idx := strings.Index(output, "ECONNREFUSED"); idx >= 0 {
			rest := output[idx:]
			colonIdx := strings.LastIndex(rest[:min(len(rest), 60)], ":")
			if colonIdx > 0 {
				start := colonIdx + 1
				end := start
				for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
					end++
				}
				if end > start && end-start <= 5 {
					port = rest[start:end]
				}
			}
		}
	}

	hint := "[hint: 'Connection refused' — no service is listening. Check: "
	if port != "" {
		hint += fmt.Sprintf("(1) `ss -tlnp | grep %s` to see if anything listens on port %s, ", port, port)
	} else {
		hint += "(1) `ss -tlnp` to see what ports are listening, "
	}
	hint += "(2) if you just started a service, it may need time — use `sleep 2` or a retry loop before testing, " +
		"(3) check service logs for startup errors, " +
		"(4) verify the service is configured for the correct host/port]"
	return hint
}

// systemctlNotFoundHint detects when systemctl/systemd is unavailable (common in
// Docker containers) and suggests alternative service persistence methods.
// This saves 1-2 turns of the agent trying different systemd incantations.
func systemctlNotFoundHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)
	detected := (strings.Contains(lower, "systemctl") &&
		(strings.Contains(lower, "command not found") || strings.Contains(lower, "no such file"))) ||
		strings.Contains(lower, "failed to connect to bus") ||
		strings.Contains(lower, "system has not been booted with systemd")
	if !detected {
		return ""
	}
	return "[hint: systemd/systemctl is not available in this container. Alternative service persistence methods: " +
		"(1) Start daemons directly (e.g., `nginx`, `sshd`, `postgres` — most server programs daemonize by default), " +
		"(2) Use `service <name> start` (SysV init — works in many containers), " +
		"(3) For custom processes: `nohup <command> > /var/log/<name>.log 2>&1 &`, " +
		"(4) Add to `/etc/rc.local` or `crontab -e` with `@reboot <command>` for persistence across container restarts. " +
		"Do NOT keep trying systemctl — it will never work without systemd]"
}

// subprocessTimeoutHint detects when test output contains subprocess timeout
// errors (e.g., Python's subprocess.TimeoutExpired). This indicates the agent's
// code runs too slowly and needs performance optimization — a different fix
// strategy than compilation or logic errors.
func subprocessTimeoutHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// Detect subprocess/test timeouts across languages.
	// Python: "TimeoutExpired" or "timed out after N seconds"
	// Java: "TimeoutException" or "java.util.concurrent.TimeoutException"
	// Ruby: "Timeout::Error" or "execution expired"
	// Go: "context deadline exceeded" or "test timed out after"
	// Node.js: "TimeoutError" or "test timeout"
	// Competitive programming: "Time Limit Exceeded" or "TLE"
	// Generic: "exceeded the time limit"
	isTimeout := strings.Contains(lower, "timeoutexpired") ||
		(strings.Contains(lower, "timed out after") && strings.Contains(lower, "seconds")) ||
		strings.Contains(lower, "timeoutexception") ||
		(strings.Contains(output, "Timeout::Error") || strings.Contains(lower, "execution expired")) ||
		strings.Contains(lower, "context deadline exceeded") ||
		(strings.Contains(lower, "test timed out") && strings.Contains(lower, "after")) ||
		strings.Contains(lower, "timeouterror") ||
		strings.Contains(lower, "time limit exceeded") ||
		(strings.Contains(lower, "exceeded") && strings.Contains(lower, "time limit"))

	if isTimeout {
		return "[hint: A subprocess timed out during testing. Your solution is too SLOW — it works correctly but " +
			"exceeds the test's time limit. Performance optimizations: " +
			"(1) Reduce algorithmic complexity (use hash maps, avoid nested loops), " +
			"(2) Remove debug prints and unnecessary I/O, " +
			"(3) Pre-compute expensive values, use lookup tables instead of runtime computation, " +
			"(4) For C/C++: ensure -O2 or -O3 optimization flags, avoid unnecessary memory allocations, " +
			"(5) For Monte Carlo/simulations: reduce iteration count if the test allows approximate results, " +
			"(6) Check if the program runs in an infinite loop or unnecessarily waits for input]"
	}

	return ""
}

// sharedLibraryHint detects missing shared library errors and suggests fixes.
// These occur when a compiled binary can't find a required .so file at runtime.
func sharedLibraryHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	if strings.Contains(output, "cannot open shared object file") ||
		strings.Contains(output, "error while loading shared libraries") {
		// Try to extract the library name.
		for _, prefix := range []string{
			"error while loading shared libraries: ",
			"cannot open shared object file",
		} {
			idx := strings.Index(output, prefix)
			if idx < 0 {
				continue
			}
			// Look backwards from "error while loading" to find the lib name.
			if prefix == "error while loading shared libraries: " {
				start := idx + len(prefix)
				rest := output[start:]
				if colon := strings.Index(rest, ":"); colon > 0 {
					lib := strings.TrimSpace(rest[:colon])
					return fmt.Sprintf("[hint: missing shared library '%s'. Try: "+
						"(1) `ldconfig` to refresh the linker cache, "+
						"(2) `apt-get install -y` the dev package (e.g., lib%s-dev), "+
						"(3) `find / -name '%s*' 2>/dev/null` to check if it exists elsewhere, "+
						"(4) set LD_LIBRARY_PATH to the directory containing the library]",
						lib, strings.TrimPrefix(strings.TrimPrefix(lib, "lib"), ".so"), lib)
				}
			}
		}
		return "[hint: missing shared library. Try: (1) `ldconfig`, (2) install the dev package with apt-get, " +
			"(3) check LD_LIBRARY_PATH, (4) `find / -name '*.so*' 2>/dev/null` to locate it]"
	}
	return ""
}

// diskSpaceHint detects "No space left on device" and suggests cleanup strategies.
func diskSpaceHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	if strings.Contains(output, "No space left on device") ||
		strings.Contains(output, "ENOSPC") {
		return "[hint: disk full. Free space: " +
			"(1) `df -h` to check filesystem usage, " +
			"(2) remove build artifacts: `rm -rf /tmp/* build/ *.o`, " +
			"(3) remove package caches: `apt-get clean`, `pip cache purge`, " +
			"(4) remove large unnecessary files: `find / -size +100M -type f 2>/dev/null | head -10`, " +
			"(5) if compiling, consider a smaller build (disable optional features)]"
	}
	return ""
}

// makefileHint detects common Makefile/make errors and provides targeted fixes.
// This saves 1-2 turns of the agent diagnosing make-specific syntax issues.
func makefileHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// "missing separator" — usually a tab vs spaces issue in Makefile.
	if strings.Contains(output, "missing separator") {
		return "[hint: Makefile syntax error — 'missing separator' means recipe lines must start with a TAB character, not spaces. " +
			"Use: sed -i 's/^    /\t/' Makefile (replace leading 4 spaces with tab) or rewrite the Makefile with proper tabs]"
	}

	// "No rule to make target" — missing file or target.
	if strings.Contains(output, "No rule to make target") {
		// Try to extract the target name.
		idx := strings.Index(output, "No rule to make target")
		if idx >= 0 {
			rest := output[idx:]
			// Format: "No rule to make target 'foo'"
			if qStart := strings.IndexAny(rest, "'`"); qStart > 0 {
				after := rest[qStart+1:]
				if qEnd := strings.IndexAny(after, "'`"); qEnd > 0 {
					target := after[:qEnd]
					return fmt.Sprintf("[hint: make cannot find target '%s'. Check: "+
						"(1) is the file/target spelled correctly? "+
						"(2) does it need to be created first (e.g., extract archive, run configure)? "+
						"(3) check Makefile target names with: grep '^[a-zA-Z].*:' Makefile]", target)
				}
			}
		}
		return "[hint: make cannot find the target. Check file names and Makefile target definitions]"
	}

	// "recipe for target ... failed" — the command itself failed.
	// Not much we can add beyond what the command output says, but we can
	// suggest checking the specific failing command.
	if strings.Contains(output, "recipe for target") && strings.Contains(output, "failed") {
		return "[hint: a make recipe failed. The error is in the command output above — fix that specific command. " +
			"Use `make -n <target>` to see what commands would run without executing them]"
	}

	// "*** No targets specified and no makefile found" — wrong directory.
	if strings.Contains(output, "No targets specified and no makefile found") ||
		strings.Contains(output, "No targets.  Stop") {
		return "[hint: no Makefile found in current directory. Check: " +
			"(1) ls *.mk Makefile makefile GNUmakefile, " +
			"(2) you may need to run ./configure or cmake first, " +
			"(3) check if you're in the right directory]"
	}

	return ""
}

// cmakeHint detects CMake configuration errors and suggests fixes.
// CMake errors are verbose but the fix is usually installing a missing package.
func cmakeHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// "Could NOT find <Package>" — extremely common CMake error.
	if strings.Contains(output, "Could NOT find") || strings.Contains(output, "Could not find") {
		// Map common CMake package names to apt packages.
		cmakePkgs := map[string]string{
			"OpenSSL":    "libssl-dev",
			"ZLIB":       "zlib1g-dev",
			"CURL":       "libcurl4-openssl-dev",
			"Boost":      "libboost-all-dev",
			"PkgConfig":  "pkg-config",
			"Threads":    "build-essential",
			"PNG":        "libpng-dev",
			"JPEG":       "libjpeg-dev",
			"TIFF":       "libtiff-dev",
			"GTest":      "libgtest-dev",
			"Protobuf":   "libprotobuf-dev protobuf-compiler",
			"Python3":    "python3-dev",
			"SQLite3":    "libsqlite3-dev",
			"LibXml2":    "libxml2-dev",
			"Freetype":   "libfreetype-dev",
			"X11":        "libx11-dev",
			"FFmpeg":     "libavcodec-dev libavformat-dev libswscale-dev",
			"OpenCV":     "libopencv-dev",
			"BZip2":      "libbz2-dev",
			"LibLZMA":    "liblzma-dev",
			"Curses":     "libncurses-dev",
			"FFTW3":      "libfftw3-dev",
			"HDF5":       "libhdf5-dev",
			"LAPACK":     "liblapack-dev",
			"BLAS":       "libblas-dev",
			"Cairo":      "libcairo2-dev",
			"ALSA":       "libasound2-dev",
			"PulseAudio": "libpulse-dev",
			"LibArchive": "libarchive-dev",
			"PCAP":       "libpcap-dev",
			"GMP":        "libgmp-dev",
			"MPFR":       "libmpfr-dev",
			"SDL2":       "libsdl2-dev",
			"GLEW":       "libglew-dev",
			"GLUT":       "freeglut3-dev",
			"Readline":   "libreadline-dev",
		}
		for pkg, apt := range cmakePkgs {
			if strings.Contains(output, pkg) {
				return fmt.Sprintf("[hint: CMake cannot find %s — install with: apt-get install -y %s]", pkg, apt)
			}
		}
		return "[hint: CMake cannot find a required package. Install the -dev package with apt-get. " +
			"Use `apt-cache search <name>` to find the right package name]"
	}

	// "CMake Error at CMakeLists.txt" — general CMake configuration error.
	if strings.Contains(output, "CMake Error") {
		if strings.Contains(output, "cmake_minimum_required") || strings.Contains(output, "VERSION") {
			return "[hint: CMake version may be too old. Check: cmake --version. " +
				"If needed: apt-get install -y cmake or pip install cmake]"
		}
	}

	return ""
}

// cargoHint detects Rust/Cargo-specific errors and provides targeted fixes.
// The compilationErrorHint already handles Rust compiler file:line extraction,
// but this covers cargo-level errors that don't produce file references.
func cargoHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Missing crate — suggest cargo add.
	// "error[E0432]: unresolved import `serde`" or "can't find crate for `serde`"
	if strings.Contains(output, "can't find crate for") {
		for _, pattern := range []string{"can't find crate for `", "can't find crate for '"} {
			if idx := strings.Index(output, pattern); idx >= 0 {
				start := idx + len(pattern)
				rest := output[start:]
				if end := strings.IndexAny(rest, "`'"); end > 0 {
					crate := rest[:end]
					return fmt.Sprintf("[hint: missing Rust crate '%s' — try: cargo add %s, or add it to [dependencies] in Cargo.toml]",
						crate, crate)
				}
			}
		}
		return "[hint: missing Rust crate — add it to Cargo.toml [dependencies] or use: cargo add <crate>]"
	}

	// Unresolved import — often a missing dependency or wrong path.
	if strings.Contains(output, "unresolved import") {
		return "[hint: unresolved import — check: (1) is the crate listed in Cargo.toml [dependencies]? " +
			"(2) does it need a feature flag? (e.g., serde = { version = \"1\", features = [\"derive\"] }) " +
			"(3) is the module path correct? (use `mod` declarations or `use crate::` for local modules)]"
	}

	// Edition-related errors (e.g., async/await requires edition 2018+).
	if strings.Contains(output, "edition") && strings.Contains(output, "required") {
		return "[hint: Rust edition may be too old. Check/set edition in Cargo.toml: [package] edition = \"2021\"]"
	}

	// cargo fetch/build network error.
	if strings.Contains(output, "failed to download") || strings.Contains(output, "failed to fetch") {
		if strings.Contains(output, "Couldn't resolve host") || strings.Contains(output, "network") {
			return "[hint: cargo network error — try again, or if offline, check if crate sources are already cached in target/]"
		}
	}

	// "no matching package" or version mismatch.
	if strings.Contains(output, "no matching package") || strings.Contains(output, "failed to select a version") {
		return "[hint: cargo can't find a matching version. Check crate version in Cargo.toml — " +
			"use `cargo search <crate>` to find available versions, or use a looser version bound (e.g., \"1\" instead of \"1.2.3\")]"
	}

	// Common borrow checker patterns — don't try to explain borrow checking,
	// just tell the agent where to look and suggest common fixes.
	if strings.Contains(output, "cannot borrow") && strings.Contains(output, "as mutable") {
		return "[hint: borrow checker error — cannot have a mutable reference while other references exist. " +
			"Common fixes: (1) clone the value, (2) restructure to avoid simultaneous borrows, " +
			"(3) use RefCell/Rc for interior mutability]"
	}
	if strings.Contains(output, "does not live long enough") {
		return "[hint: lifetime error — a value is being dropped while still borrowed. " +
			"Common fixes: (1) clone the data, (2) restructure ownership so the value lives longer, " +
			"(3) use String instead of &str for owned strings]"
	}

	// Trait bound not satisfied — very common with generics.
	if strings.Contains(output, "the trait bound") && strings.Contains(output, "is not satisfied") {
		return "[hint: trait bound not satisfied — the type doesn't implement the required trait. " +
			"Common fixes: (1) add #[derive(Trait)] to your type, (2) implement the trait manually, " +
			"(3) check if you need a different type that already implements the trait]"
	}

	// Move error — common ownership issue.
	if strings.Contains(output, "value used here after move") || strings.Contains(output, "move occurs because") {
		return "[hint: ownership/move error — a value was used after being moved. " +
			"Common fixes: (1) clone the value before the move, (2) use references (&/&mut) instead, " +
			"(3) for iterators, use .iter() instead of .into_iter() to borrow rather than consume]"
	}

	// Missing lifetime specifier.
	if strings.Contains(output, "missing lifetime specifier") {
		return "[hint: missing lifetime specifier — the compiler can't infer lifetimes. " +
			"Common fixes: (1) add explicit lifetime annotations (fn foo<'a>(x: &'a str) -> &'a str), " +
			"(2) return an owned type (String instead of &str) to avoid lifetimes entirely, " +
			"(3) use 'static for string literals and compile-time constants]"
	}

	// Mismatched types — common generic/type error.
	if strings.Contains(output, "mismatched types") {
		return "[hint: type mismatch — check the expected vs actual types in the error. " +
			"Common causes: (1) &str vs String (use .to_string() or &*s), " +
			"(2) Option<T> vs T (use .unwrap() or match), (3) Result<T,E> vs T (use ? operator)]"
	}

	return ""
}

// goModuleHint detects Go module errors and suggests fixes.
// These are common when working with Go projects in containers.
func goModuleHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// "no required module provides package" — missing dependency.
	if strings.Contains(output, "no required module provides package") {
		// Extract the package path.
		pattern := "no required module provides package "
		if idx := strings.Index(output, pattern); idx >= 0 {
			rest := output[idx+len(pattern):]
			// Package path ends at semicolon, comma, or newline.
			end := strings.IndexAny(rest, ";,\n ")
			if end > 0 {
				pkg := strings.TrimSpace(rest[:end])
				return fmt.Sprintf("[hint: missing Go module — try: go get %s && go mod tidy]", pkg)
			}
		}
		return "[hint: missing Go module — try: go mod tidy, or go get <package>]"
	}

	// "go.sum mismatch" or checksum errors.
	if strings.Contains(output, "verifying") && strings.Contains(output, "checksum mismatch") ||
		strings.Contains(output, "go.sum") && strings.Contains(output, "mismatch") {
		return "[hint: go.sum checksum mismatch — try: go mod tidy, or if that fails: rm go.sum && go mod tidy]"
	}

	// "cannot find module" — GOPATH vs module mode confusion.
	if strings.Contains(output, "cannot find module") {
		return "[hint: cannot find Go module — check: (1) go.mod exists in the project root, " +
			"(2) try: go mod init <module-name> if starting fresh, " +
			"(3) run: go mod tidy to resolve dependencies]"
	}

	// "go: module ... found but does not contain package"
	if strings.Contains(output, "found") && strings.Contains(output, "does not contain package") {
		return "[hint: Go module found but doesn't contain the expected package. " +
			"Check the import path — it may need a /v2 or /v3 suffix for major versions, " +
			"or the package may have moved. Try: go doc <module> to see available packages]"
	}

	// "build constraints exclude all Go files"
	if strings.Contains(output, "build constraints exclude all Go files") {
		return "[hint: build tags or OS/arch constraints exclude all files. " +
			"Check //go:build or // +build tags in source files. " +
			"For CGo: set CGO_ENABLED=1 if the code requires C bindings]"
	}

	return ""
}

// gitHint detects common git errors: merge conflicts, detached HEAD, push
// rejections, rebase failures, and dirty working tree issues. Coding agents
// frequently use git and waste 1-2 turns on these recoverable errors.
func gitHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Merge conflicts: "CONFLICT (content): Merge conflict in ..."
	if strings.Contains(output, "CONFLICT") && strings.Contains(output, "Merge conflict") {
		return "[hint: git merge conflict — resolve conflicts in the affected files " +
			"(look for <<<<<<< / ======= / >>>>>>> markers), then: git add <files> && git commit]"
	}

	// Rebase conflicts.
	if strings.Contains(output, "could not apply") && strings.Contains(output, "rebase") ||
		strings.Contains(output, "CONFLICT") && strings.Contains(output, "rebase") {
		return "[hint: git rebase conflict — resolve conflicts, then: git add <files> && git rebase --continue. " +
			"To abort: git rebase --abort]"
	}

	// Cherry-pick conflicts.
	if strings.Contains(output, "could not apply") && strings.Contains(output, "cherry-pick") ||
		strings.Contains(output, "CONFLICT") && strings.Contains(output, "cherry-pick") {
		return "[hint: git cherry-pick conflict — resolve conflicts, then: git add <files> && git cherry-pick --continue. " +
			"To abort: git cherry-pick --abort]"
	}

	// Detached HEAD warning.
	if strings.Contains(output, "detached HEAD") || strings.Contains(output, "HEAD detached") {
		return "[hint: you are in detached HEAD state. To keep changes: " +
			"git checkout -b <new-branch-name>. To return to a branch: git checkout <branch>]"
	}

	// Push rejected: non-fast-forward.
	if strings.Contains(output, "rejected") && strings.Contains(output, "non-fast-forward") ||
		strings.Contains(output, "failed to push some refs") {
		return "[hint: git push rejected — remote has changes you don't have. " +
			"Pull first: git pull --rebase, then push again]"
	}

	// Authentication failure.
	if strings.Contains(output, "Authentication failed") || strings.Contains(output, "could not read Username") ||
		strings.Contains(output, "Permission denied (publickey)") {
		return "[hint: git authentication failed — check credentials, SSH keys, or access token. " +
			"For HTTPS: use a personal access token. For SSH: ensure ssh-agent has the key (ssh-add)]"
	}

	// Dirty working tree: "Please commit your changes or stash them"
	if strings.Contains(output, "Please commit your changes or stash them") ||
		strings.Contains(output, "Your local changes to the following files would be overwritten") {
		return "[hint: uncommitted changes blocking git operation — either: " +
			"(1) git stash, run the operation, git stash pop, or " +
			"(2) git commit the changes first]"
	}

	// Unmerged paths: "you need to resolve your current index first"
	if strings.Contains(output, "you need to resolve your current index first") ||
		strings.Contains(output, "Unmerged") && strings.Contains(output, "fix conflicts") {
		return "[hint: unmerged paths remain — resolve all conflicts, git add the files, then continue]"
	}

	// Branch already exists.
	if strings.Contains(output, "already exists") && strings.Contains(output, "branch") {
		return "[hint: branch already exists — use a different name, or: " +
			"git checkout <branch> to switch to it, " +
			"git branch -D <branch> to delete and recreate]"
	}

	// Not a git repository.
	if strings.Contains(output, "not a git repository") {
		return "[hint: not inside a git repository — either cd to the right directory, " +
			"or initialize with: git init]"
	}

	// Pathspec / file not found.
	if strings.Contains(output, "pathspec") && strings.Contains(output, "did not match any file") {
		return "[hint: git pathspec error — the file or branch name doesn't exist. " +
			"Check spelling, or use: git ls-files to see tracked files, git branch -a for branches]"
	}

	// Lock file: "Unable to create '.../.git/index.lock'"
	if strings.Contains(output, ".git/index.lock") || strings.Contains(output, ".git/HEAD.lock") {
		return "[hint: git lock file exists — another git process may be running. " +
			"If not, remove the stale lock: rm -f .git/index.lock]"
	}

	// Patch apply failures: "git apply" or "git am"
	if strings.Contains(output, "patch does not apply") || strings.Contains(output, "error: patch failed") {
		return "[hint: git patch failed to apply — the file has changed since the patch was created. " +
			"Try: git apply --3way <patch> (allows merge), or apply manually by reading the .patch file]"
	}
	if strings.Contains(output, "git am") && strings.Contains(output, "patch does not apply") ||
		strings.Contains(output, "Patch failed at") {
		return "[hint: git am failed — resolve the conflict manually, then: git am --continue. " +
			"To skip this patch: git am --skip. To abort: git am --abort]"
	}

	// Diverged branches: "have diverged"
	if strings.Contains(output, "have diverged") {
		return "[hint: branches have diverged — they each have commits the other doesn't. " +
			"Options: (1) git pull --rebase to replay your commits on top, " +
			"(2) git merge to create a merge commit]"
	}

	// Empty commit.
	if strings.Contains(output, "nothing to commit") {
		return "[hint: nothing to commit — working tree is clean. " +
			"If you expected changes: check git status, ensure files are saved]"
	}

	return ""
}

// archiveHint detects common tar, unzip, and gzip errors. Archive extraction
// is failure mode #9 — the directory structure after extraction often doesn't
// match what tests expect, and the agent wastes turns diagnosing.
func archiveHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// tar errors.
	if strings.Contains(output, "tar:") {
		if strings.Contains(output, "Cannot open: No such file or directory") ||
			strings.Contains(output, "Error opening archive") {
			return "[hint: tar cannot find the archive file. Check: (1) ls -la to find the actual filename, " +
				"(2) check if you need to download it first, (3) check if it's in a different directory]"
		}
		if strings.Contains(output, "not in gzip format") || strings.Contains(output, "gzip: stdin: not in gzip format") {
			return "[hint: file is not gzip compressed despite the name. Try: " +
				"(1) `file <archive>` to check the real format, " +
				"(2) `tar xf <archive>` (without -z) for plain tar, " +
				"(3) `tar xjf <archive>` for bzip2, " +
				"(4) `tar xJf <archive>` for xz]"
		}
		if strings.Contains(output, "Cannot change ownership") {
			return "[hint: tar ownership warning — safe to ignore in containers. Add --no-same-owner flag: tar --no-same-owner -xf <archive>]"
		}
	}

	// unzip errors.
	if strings.Contains(output, "unzip:") || strings.Contains(output, "End-of-central-directory") {
		if strings.Contains(output, "cannot find") || strings.Contains(output, "No such file") {
			return "[hint: unzip cannot find the archive. Check with: ls -la *.zip]"
		}
		if strings.Contains(output, "End-of-central-directory") || strings.Contains(output, "not a zip") {
			return "[hint: file is not a valid ZIP archive. Check with: `file <filename>`. " +
				"It might be a tar.gz, tar.bz2, or other format — use the appropriate tool]"
		}
	}

	// gzip/gunzip errors.
	if strings.Contains(output, "gzip:") {
		if strings.Contains(output, "unexpected end of file") || strings.Contains(output, "invalid compressed data") {
			return "[hint: gzip file is corrupted or incomplete. Try: (1) re-download, (2) check file size with ls -la, " +
				"(3) `file <filename>` to verify format]"
		}
		if strings.Contains(output, "already has .gz suffix") {
			return "[hint: gunzip: file already decompressed. Use the file without .gz extension]"
		}
	}

	return ""
}

// databaseHint detects common database errors (SQLite, PostgreSQL, MySQL) and
// provides actionable fix suggestions. Database tasks are common in coding
// benchmarks and agents waste turns debugging connection/schema issues.
func databaseHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// SQLite errors.
	if strings.Contains(lower, "sqlite") || strings.Contains(lower, "operationalerror") {
		if strings.Contains(lower, "no such table") {
			return "[hint: SQLite table missing — your schema hasn't been applied. " +
				"Create tables before inserting data. Check if there's a schema.sql or migrations file to run first]"
		}
		if strings.Contains(lower, "database is locked") {
			return "[hint: SQLite database is locked — another process has an open transaction. " +
				"Close other connections, use WAL mode (PRAGMA journal_mode=WAL), or add timeout: sqlite3_busy_timeout()]"
		}
		if strings.Contains(lower, "unable to open database") || strings.Contains(lower, "no such file or directory") {
			return "[hint: SQLite database file not found. Check: (1) path is correct, (2) directory exists (mkdir -p), " +
				"(3) file permissions]"
		}
		if strings.Contains(lower, "near \"") && strings.Contains(lower, "syntax error") {
			return "[hint: SQL syntax error — check your query. Common causes: missing quotes around strings, " +
				"reserved words used as column names (wrap in double quotes or backticks), missing commas]"
		}
	}

	// PostgreSQL errors.
	if strings.Contains(lower, "psycopg") || strings.Contains(lower, "postgresql") || strings.Contains(lower, "pg_") ||
		strings.Contains(output, "FATAL:") && strings.Contains(lower, "role") {
		if strings.Contains(lower, "connection refused") {
			return "[hint: PostgreSQL not running. Start with: service postgresql start. " +
				"If that fails: pg_ctlcluster <version> main start, or su - postgres -c 'pg_ctl start -D /var/lib/postgresql/data']"
		}
		if strings.Contains(lower, "role") && strings.Contains(lower, "does not exist") {
			return "[hint: PostgreSQL role missing. Create with: su - postgres -c \"createuser --superuser <username>\". " +
				"Or use the postgres superuser: psql -U postgres]"
		}
		if strings.Contains(lower, "database") && strings.Contains(lower, "does not exist") {
			return "[hint: PostgreSQL database missing. Create with: su - postgres -c \"createdb <dbname>\". " +
				"Or: psql -U postgres -c 'CREATE DATABASE <dbname>']"
		}
		if strings.Contains(lower, "password authentication failed") || strings.Contains(lower, "peer authentication failed") {
			return "[hint: PostgreSQL auth failed. Options: " +
				"(1) Edit pg_hba.conf to use 'trust' for local connections, " +
				"(2) Set password: ALTER USER <user> PASSWORD '<pass>', " +
				"(3) Use -U postgres with sudo/su]"
		}
	}

	// MySQL/MariaDB errors.
	if strings.Contains(lower, "mysql") || strings.Contains(lower, "mariadb") ||
		strings.Contains(output, "ERROR 1") || strings.Contains(output, "ERROR 2") {
		if strings.Contains(lower, "can't connect") || strings.Contains(lower, "connection refused") {
			return "[hint: MySQL/MariaDB not running. Start with: service mysql start || service mariadb start. " +
				"Check status with: mysqladmin ping]"
		}
		if strings.Contains(lower, "access denied") {
			return "[hint: MySQL access denied. Try: mysql -u root (no password), or " +
				"mysql -u root -p, or set up: mysqladmin -u root password '<newpass>']"
		}
		if strings.Contains(lower, "unknown database") {
			return "[hint: MySQL database doesn't exist. Create with: mysql -u root -e 'CREATE DATABASE <dbname>']"
		}
	}

	return ""
}

// memoryHint detects out-of-memory errors and segfaults, suggesting optimization
// strategies. OOM is a common failure mode for agents processing large datasets
// or running inefficient algorithms.
func memoryHint(output string, exitCode int) string {
	lower := strings.ToLower(output)

	// Python MemoryError — distinct from signal-based OOM (exit 137).
	// signalHint already covers exit code 137; this catches the Python-specific
	// error message which can appear with any exit code.
	if strings.Contains(output, "MemoryError") {
		return "[hint: Python MemoryError — your program ran out of RAM. Strategies: " +
			"(1) Process data in chunks/streaming instead of loading all at once, " +
			"(2) Use generators instead of lists, " +
			"(3) Use numpy arrays instead of Python lists (10x less memory), " +
			"(4) Delete large objects with 'del' when no longer needed, " +
			"(5) For pandas: use read_csv(chunksize=N) or dtype optimizations]"
	}

	// OOM killer text in output (but NOT exit code 137 alone — signalHint handles that).
	if exitCode != 137 {
		if (strings.Contains(lower, "killed") && strings.Contains(lower, "memory")) ||
			strings.Contains(lower, "out of memory") ||
			(strings.Contains(lower, "oom") && strings.Contains(lower, "kill")) {
			return "[hint: Process was killed (likely OOM). Your program uses too much memory. " +
				"Reduce memory usage: (1) process data in streaming/chunked fashion, " +
				"(2) use memory-efficient data structures, (3) avoid loading entire files into memory, " +
				"(4) for C/C++: check for memory leaks with valgrind]"
		}
	}

	// Segmentation fault text in output (but NOT exit code 139 alone — signalHint handles that).
	if exitCode != 139 {
		if strings.Contains(lower, "segmentation fault") || strings.Contains(lower, "segfault") {
			return "[hint: Segmentation fault — your program accessed invalid memory. Common causes: " +
				"(1) Array/buffer out of bounds, (2) NULL pointer dereference, (3) Use-after-free, " +
				"(4) Stack overflow from deep recursion. Debug with: compile with -g and run under gdb, " +
				"or add bounds checking. For C: use -fsanitize=address]"
		}
	}

	return ""
}

// browserHint detects when the agent is trying to use browser/GUI tools
// (Selenium, Playwright, Puppeteer, Chromium) in a headless container.
// This wastes many turns. The verifier handles browser testing — the agent
// should focus on creating the required files, not setting up browsers.
func browserHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// Chromium/Chrome not found or failed to launch.
	if (strings.Contains(lower, "chrome") || strings.Contains(lower, "chromium")) &&
		(strings.Contains(lower, "not found") || strings.Contains(lower, "failed to launch") ||
			strings.Contains(lower, "no such file") || strings.Contains(lower, "cannot find")) {
		return "[hint: Browser (Chrome/Chromium) is not available in this environment. " +
			"DO NOT try to install or configure a browser — it wastes turns. " +
			"If tests use browser automation (Selenium/Playwright), the verifier runs them separately. " +
			"Focus on creating the required files and verifying with non-browser tests.]"
	}

	// Selenium-specific errors.
	if strings.Contains(output, "selenium.common.exceptions") ||
		(strings.Contains(lower, "webdriver") && (strings.Contains(lower, "not found") || strings.Contains(lower, "error"))) {
		return "[hint: Selenium WebDriver error — browser automation is not available in this environment. " +
			"DO NOT spend turns installing chromedriver or browser packages. " +
			"The verifier handles browser tests. Focus on your core code.]"
	}

	// Playwright-specific errors.
	if strings.Contains(output, "playwright._impl") ||
		(strings.Contains(lower, "playwright") && strings.Contains(lower, "browser")) {
		return "[hint: Playwright browser error — browsers are not installed in this environment. " +
			"DO NOT run 'playwright install' or try to set up browsers. " +
			"The verifier handles browser tests. Focus on your core code.]"
	}

	// X11/display errors (GUI tools in headless container).
	if (strings.Contains(lower, "display") || strings.Contains(lower, "x11")) &&
		(strings.Contains(lower, "not set") || strings.Contains(lower, "cannot open") ||
			strings.Contains(lower, "could not connect") || strings.Contains(lower, "no protocol")) {
		return "[hint: No display server (X11) available — this is a headless environment. " +
			"DO NOT try to set up Xvfb or GUI tools. If you need headless rendering, " +
			"use a non-GUI library (e.g., matplotlib with Agg backend: matplotlib.use('Agg')). " +
			"For browser tests, the verifier handles them separately.]"
	}

	return ""
}

// sslHint detects SSL/TLS certificate errors that commonly occur in
// container environments where CA certificates may not be configured.
// Provides workarounds for pip, curl, wget, and git.
func sslHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	if strings.Contains(lower, "certificate verify failed") ||
		strings.Contains(lower, "ssl: certificate_verify_failed") ||
		strings.Contains(lower, "ssl_error_syscall") ||
		(strings.Contains(lower, "ssl") && strings.Contains(lower, "certificate") && strings.Contains(lower, "error")) {
		// Detect if this is a pip-related SSL error.
		if strings.Contains(lower, "pip") || strings.Contains(lower, "pypi") {
			return "[hint: SSL certificate error during pip install. Fix: " +
				"pip install --trusted-host pypi.org --trusted-host files.pythonhosted.org <package>]"
		}
		// Curl SSL error.
		if strings.Contains(lower, "curl") {
			return "[hint: SSL certificate error with curl. Fix: use curl -k (insecure) or " +
				"install CA certificates: apt-get install -y ca-certificates && update-ca-certificates]"
		}
		// Git SSL error.
		if strings.Contains(lower, "git") {
			return "[hint: SSL certificate error with git. Fix: " +
				"git config --global http.sslVerify false (for this session only)]"
		}
		// Generic SSL error.
		return "[hint: SSL certificate verification failed. This is common in container environments. " +
			"Try: (1) apt-get install -y ca-certificates && update-ca-certificates, " +
			"(2) For Python: set PYTHONHTTPSVERIFY=0 or use --trusted-host flags with pip, " +
			"(3) For curl: use -k flag]"
	}

	return ""
}

// shellLimitHint detects common shell/OS resource limit errors and provides fixes.
// These are general-purpose errors that waste turns if the agent has to diagnose them.
func shellLimitHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// "Argument list too long" (E2BIG) — common when glob expands to too many files.
	// e.g., `rm *.log` in a directory with 100K log files.
	if strings.Contains(lower, "argument list too long") {
		return "[hint: 'Argument list too long' — the shell glob expanded to too many files. " +
			"Use find with -exec or xargs instead:\n" +
			"  find . -name '*.log' -delete              # delete matching files\n" +
			"  find . -name '*.txt' | xargs rm            # rm via xargs\n" +
			"  find . -name '*.py' -exec command {} \\;   # exec on each file\n" +
			"  ls | head -100                             # list first 100 files]"
	}

	// "Too many open files" (EMFILE/ENFILE) — process ran out of file descriptors.
	if strings.Contains(lower, "too many open files") || strings.Contains(output, "EMFILE") ||
		strings.Contains(output, "ENFILE") {
		return "[hint: 'Too many open files' — process exhausted file descriptor limit. Fix: " +
			"(1) ulimit -n 65535 (increase limit for current shell), " +
			"(2) close files in your code (use 'with open()' in Python, defer f.Close() in Go), " +
			"(3) check for file descriptor leaks (opening files in loops without closing)]"
	}

	return ""
}

// perlModuleHint detects Perl module load failures and suggests installation.
// Perl's "Can't locate Foo/Bar.pm" is the equivalent of Python's ModuleNotFoundError.
func perlModuleHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Perl: "Can't locate Foo/Bar.pm in @INC"
	if idx := strings.Index(output, "Can't locate "); idx >= 0 {
		rest := output[idx+len("Can't locate "):]
		end := strings.Index(rest, " in @INC")
		if end < 0 {
			end = strings.IndexAny(rest, " \n")
		}
		if end > 0 {
			module := rest[:end]
			// Convert path to module name: Foo/Bar.pm → Foo::Bar
			modName := strings.TrimSuffix(module, ".pm")
			modName = strings.ReplaceAll(modName, "/", "::")
			return fmt.Sprintf("[hint: missing Perl module '%s' — install with: "+
				"cpanm %s || apt-get install -y lib%s-perl (lowercase, dashes for ::)]",
				modName, modName, strings.ToLower(strings.ReplaceAll(modName, "::", "-")))
		}
	}

	return ""
}

// rubyGemHint detects Ruby LoadError (missing gems) and Bundler errors.
// Suggests gem install or bundle install to save a turn of troubleshooting.
func rubyGemHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Ruby LoadError: "LoadError: cannot load such file -- foo_bar"
	if strings.Contains(output, "cannot load such file") {
		idx := strings.Index(output, "cannot load such file -- ")
		if idx >= 0 {
			rest := output[idx+len("cannot load such file -- "):]
			if nl := strings.IndexAny(rest, "\n\r"); nl > 0 {
				rest = rest[:nl]
			}
			gemName := strings.TrimSpace(rest)
			// Remove trailing "(LoadError)" or similar.
			if pidx := strings.Index(gemName, " ("); pidx > 0 {
				gemName = gemName[:pidx]
			}
			if gemName != "" {
				return fmt.Sprintf("[hint: Ruby cannot load '%s' — try: gem install %s (or bundle install if Gemfile exists)]",
					gemName, gemName)
			}
		}
		return "[hint: Ruby LoadError — install the missing gem: gem install <name> (or bundle install)]"
	}

	// Bundler::GemNotFound or "Could not find gem"
	if strings.Contains(output, "Bundler::GemNotFound") || strings.Contains(output, "Could not find gem") {
		return "[hint: missing Ruby gems — run: bundle install]"
	}

	return ""
}

// javaExceptionHint detects common Java/JVM runtime errors and provides
// actionable fix suggestions. These save 1-2 turns of the agent debugging
// classpath issues, memory settings, or infinite recursion.
func javaExceptionHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// ClassNotFoundException — wrong classpath.
	if strings.Contains(output, "ClassNotFoundException") {
		// Extract class name.
		idx := strings.Index(output, "ClassNotFoundException:")
		if idx >= 0 {
			rest := output[idx+len("ClassNotFoundException:"):]
			if nl := strings.IndexAny(rest, "\n\r"); nl > 0 {
				rest = rest[:nl]
			}
			className := strings.TrimSpace(rest)
			if className != "" {
				return fmt.Sprintf("[hint: Java ClassNotFoundException: %s — check classpath (-cp flag). "+
					"Maven: mvn compile. Gradle: ./gradlew build. javac: compile all .java files]", className)
			}
		}
		return "[hint: Java ClassNotFoundException — check classpath (-cp) and ensure all classes are compiled]"
	}

	// NoClassDefFoundError — class found at compile time but not runtime.
	if strings.Contains(output, "NoClassDefFoundError") {
		return "[hint: Java NoClassDefFoundError — class was found at compile time but not runtime. " +
			"Check that -cp includes all required JARs and class directories]"
	}

	// OutOfMemoryError
	if strings.Contains(output, "OutOfMemoryError") {
		if strings.Contains(output, "Java heap space") {
			return "[hint: Java OutOfMemoryError: heap space — increase with: java -Xmx512m (or -Xmx1g). " +
				"Also check for memory leaks (unbounded collections, unclosed streams)]"
		}
		return "[hint: Java OutOfMemoryError — increase memory: java -Xmx512m -Xms256m]"
	}

	// StackOverflowError
	if strings.Contains(output, "StackOverflowError") {
		return "[hint: Java StackOverflowError — likely infinite recursion. Check recursive methods for missing " +
			"base cases. If recursion depth is legitimate, increase stack: java -Xss4m]"
	}

	return ""
}

// elixirHint detects Elixir/Mix compilation and runtime errors.
// Elixir errors have distinctive patterns that differ from other languages.
func elixirHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Mix dependency errors: "** (Mix) Could not find Hex"
	if strings.Contains(output, "(Mix)") {
		if strings.Contains(output, "Could not find Hex") || strings.Contains(output, "Hex is not available") {
			return "[hint: Hex package manager not installed. Run: mix local.hex --force]"
		}
		if strings.Contains(output, "Dependencies have diverged") || strings.Contains(output, "the dependency") {
			return "[hint: Elixir dependency conflict — try: mix deps.clean --all && mix deps.get]"
		}
	}

	// Missing dependency: "** (UndefinedFunctionError) function :module.func/arity is undefined"
	if strings.Contains(output, "UndefinedFunctionError") {
		return "[hint: Elixir UndefinedFunctionError — check: (1) module is in deps (mix.exs), " +
			"(2) run `mix deps.get && mix compile`, (3) verify function name and arity are correct]"
	}

	// CompileError with file:line
	if strings.Contains(output, "(CompileError)") {
		// Extract file:line from "** (CompileError) lib/file.ex:42: ..."
		idx := strings.Index(output, "(CompileError)")
		if idx >= 0 {
			rest := output[idx+len("(CompileError)"):]
			rest = strings.TrimSpace(rest)
			// Find file:line pattern
			parts := strings.SplitN(rest, ":", 3)
			if len(parts) >= 2 && isNumeric(strings.TrimSpace(parts[1])) {
				file := strings.TrimSpace(parts[0])
				lineNum := strings.TrimSpace(parts[1])
				errMsg := ""
				if len(parts) >= 3 {
					errMsg = strings.TrimSpace(parts[2])
					if nl := strings.IndexAny(errMsg, "\n\r"); nl > 0 {
						errMsg = errMsg[:nl]
					}
				}
				if errMsg != "" {
					return fmt.Sprintf("[hint: %s at %s:%s — use view tool with offset=%s to see the code, then fix with edit]",
						truncateErrorLine(errMsg, 120), file, lineNum, lineNum)
				}
				return fmt.Sprintf("[hint: Elixir CompileError at %s:%s — use view tool with offset=%s to see the code]",
					file, lineNum, lineNum)
			}
		}
		return "[hint: Elixir CompileError — check the error message for file and line, then view and fix the code]"
	}

	// Missing module: "** (Code.LoadError) could not load module/file"
	if strings.Contains(output, "Code.LoadError") || (strings.Contains(output, "module") && strings.Contains(output, "is not available")) {
		return "[hint: Elixir module not found — check: (1) module name is correct, " +
			"(2) deps are installed: `mix deps.get`, (3) project compiles: `mix compile`]"
	}

	return ""
}

// nodeErrorHint extracts actionable information from Node.js errors.
// Maps MODULE_NOT_FOUND to npm install hints and extracts file:line from stacks.
func nodeErrorHint(output string, exitCode int, workDir ...string) string {
	if exitCode == 0 {
		return ""
	}

	// MODULE_NOT_FOUND — suggest install with the right package manager.
	if strings.Contains(output, "MODULE_NOT_FOUND") || strings.Contains(output, "Cannot find module") {
		// Determine package manager from lockfile (same logic as auto-install).
		pm := "npm"
		installCmd := "install"
		if len(workDir) > 0 && workDir[0] != "" {
			wd := workDir[0]
			if fileExists(filepath.Join(wd, "bun.lockb")) || fileExists(filepath.Join(wd, "bun.lock")) {
				pm = "bun"
				installCmd = "add"
			} else if fileExists(filepath.Join(wd, "pnpm-lock.yaml")) {
				pm = "pnpm"
				installCmd = "add"
			} else if fileExists(filepath.Join(wd, "yarn.lock")) {
				pm = "yarn"
				installCmd = "add"
			}
		}
		// Extract the module name.
		for _, pattern := range []string{"Cannot find module '", "Cannot find module \""} {
			idx := strings.Index(output, pattern)
			if idx < 0 {
				continue
			}
			start := idx + len(pattern)
			rest := output[start:]
			end := strings.IndexAny(rest, "'\"")
			if end > 0 {
				module := rest[:end]
				// Skip relative paths — those are user code errors, not missing packages.
				if !strings.HasPrefix(module, ".") && !strings.HasPrefix(module, "/") {
					return fmt.Sprintf("[hint: missing Node module — try: %s %s %s]", pm, installCmd, module)
				}
			}
		}
		return fmt.Sprintf("[hint: missing Node module — try: %s %s]", pm, installCmd)
	}

	// ESM/CJS module system errors — extremely confusing for agents.
	if strings.Contains(output, "ERR_REQUIRE_ESM") ||
		strings.Contains(output, "require() of ES Module") {
		return "[hint: this package is ESM-only and cannot be require()'d. " +
			"Either (1) add \"type\": \"module\" to package.json and use import, " +
			"or (2) use dynamic import: const pkg = await import('package')]"
	}
	if strings.Contains(output, "Cannot use import statement outside a module") {
		return "[hint: this file is treated as CommonJS but uses import syntax. " +
			"Either (1) add \"type\": \"module\" to package.json, " +
			"(2) rename the file to .mjs, " +
			"or (3) use require() instead of import]"
	}
	if strings.Contains(output, "ERR_UNKNOWN_FILE_EXTENSION") && strings.Contains(output, ".ts") {
		return "[hint: Node.js cannot run .ts files directly. " +
			"Use tsx (npx tsx file.ts), ts-node (npx ts-node file.ts), " +
			"or compile first with tsc]"
	}
	// ESM-specific module not found (different from CJS MODULE_NOT_FOUND).
	if strings.Contains(output, "ERR_MODULE_NOT_FOUND") {
		return "[hint: ESM module not found — check import paths include file extensions " +
			"(e.g., import './foo.js' not import './foo'). ESM requires explicit extensions.]"
	}

	// ReferenceError, TypeError with stack trace — extract file:line.
	for _, errType := range []string{"ReferenceError:", "TypeError:", "SyntaxError:"} {
		if strings.Contains(output, errType) {
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				// Node stack format: "    at functionName (file:line:col)"
				if strings.HasPrefix(trimmed, "at ") && strings.Contains(trimmed, "(") {
					parenIdx := strings.Index(trimmed, "(")
					if parenIdx > 0 {
						locEnd := strings.Index(trimmed[parenIdx:], ")")
						if locEnd > 0 {
							loc := trimmed[parenIdx+1 : parenIdx+locEnd]
							// Skip node_modules and internal paths.
							if !strings.Contains(loc, "node_modules") && !strings.HasPrefix(loc, "node:") {
								return fmt.Sprintf("[hint: %s — at %s]", errType, loc)
							}
						}
					}
				}
			}
		}
	}

	// npm/yarn/pnpm install failures.
	if strings.Contains(output, "ERESOLVE") || strings.Contains(output, "peer dep") {
		return "[hint: dependency conflict — try: npm install --legacy-peer-deps, " +
			"or pin specific versions to resolve peer dependency conflicts]"
	}
	if strings.Contains(output, "EACCES") && strings.Contains(output, "npm") {
		return "[hint: npm permission error — avoid sudo. Use: npm config set prefix ~/.npm-global " +
			"and add ~/.npm-global/bin to PATH]"
	}
	if strings.Contains(output, "ERR_SOCKET_TIMEOUT") || strings.Contains(output, "ETIMEDOUT") ||
		(strings.Contains(output, "npm") && strings.Contains(output, "registry")) {
		return "[hint: npm registry timeout — try again, or use: npm config set registry https://registry.npmjs.org/]"
	}
	if strings.Contains(output, "ENOENT") && strings.Contains(output, "package.json") {
		return "[hint: no package.json found — run npm init -y first, or cd to the project root]"
	}

	// Node.js heap out of memory.
	if strings.Contains(output, "FATAL ERROR") && strings.Contains(output, "heap") {
		return "[hint: Node.js ran out of heap memory. " +
			"Increase with: NODE_OPTIONS='--max-old-space-size=4096' node script.js " +
			"Or optimize: use streams instead of loading entire files, process data in chunks.]"
	}

	// Experimental feature warnings that block execution.
	if strings.Contains(output, "ExperimentalWarning") || strings.Contains(output, "--experimental") {
		return "[hint: Node.js experimental feature — run with the required flag (e.g., --experimental-vm-modules, --experimental-specifier-resolution=node)]"
	}

	// Deno-specific errors (Deno shares the JS/TS ecosystem but has different error formats).
	if strings.Contains(output, "error: Module not found") || strings.Contains(output, "error: Relative import path") {
		return "[hint: Deno module not found — check import paths use full URLs or file extensions. " +
			"Local imports need extensions: import { foo } from './foo.ts' (not './foo'). " +
			"For npm packages: import pkg from 'npm:package-name']"
	}
	if strings.Contains(output, "error: Uncaught") {
		// Extract the error type from "error: Uncaught (in promise) TypeError: ..."
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "error: Uncaught") {
				return "[hint: " + truncateErrorLine(trimmed, 150) +
					" — check the stack trace below for file and line number]"
			}
		}
		return "[hint: Deno uncaught error — read the stack trace to find the file and line]"
	}
	if strings.Contains(output, "error: TS") && strings.Contains(output, "[ERROR]") {
		// Deno TypeScript errors: "error: TS2345 [ERROR]: ..."
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "error: TS") {
				return "[hint: " + truncateErrorLine(trimmed, 150) +
					" — check the 'at' line below for file:line location]"
			}
		}
	}
	if strings.Contains(output, "error: The source code is invalid") {
		return "[hint: Deno syntax error — check for typos, missing brackets, or invalid TypeScript syntax]"
	}
	if strings.Contains(output, "PermissionDenied") && strings.Contains(output, "deno") {
		return "[hint: Deno permission denied — add the required flags: --allow-read, --allow-write, " +
			"--allow-net, --allow-env, --allow-run, or use --allow-all (deno run --allow-all script.ts)]"
	}

	return ""
}

// outputMismatchHint detects output comparison failures in test output and
// suggests specific diff/comparison commands. This addresses the common failure
// mode where the agent's output is close to correct but has subtle format
// differences (extra newlines, encoding, precision) that it tries to fix by
// guessing instead of doing a character-by-character comparison.
func outputMismatchHint(output string, exitCode int, workDir string) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// Detect output mismatch patterns from various test frameworks.
	isMismatch := (strings.Contains(lower, "expected") && strings.Contains(lower, "got")) ||
		(strings.Contains(lower, "expected") && strings.Contains(lower, "actual")) ||
		(strings.Contains(lower, "assertequal") && strings.Contains(lower, "!=")) ||
		strings.Contains(lower, "files differ") ||
		// diff(1) output: "Files X and Y differ" (with filenames between).
		(strings.Contains(lower, "files ") && strings.Contains(lower, " differ")) ||
		strings.Contains(lower, "differ: char") ||
		strings.Contains(lower, "diff: ") ||
		(strings.Contains(lower, "mismatch") && !strings.Contains(lower, "hash"))

	if !isMismatch {
		return ""
	}

	// Build a specific suggestion based on what diff targets are available.
	hint := "[hint: OUTPUT MISMATCH detected. Debug character-by-character:\n"
	hint += "  1. xxd <your_output> | head -20   # inspect exact bytes\n"
	hint += "  2. xxd <expected_output> | head -20\n"
	hint += "  3. diff <expected_output> <your_output>\n"
	hint += "  Common causes: trailing newline (echo adds \\n, printf doesn't), "
	hint += "BOM marker (\\xEF\\xBB\\xBF), Windows line endings (\\r\\n), "
	hint += "numeric precision (1.0 vs 1.00), whitespace]"
	return hint
}

// testResultSummary extracts a concise summary from test runner output.
// Returns empty string if the output doesn't look like test results.
// This helps the model quickly understand what passed/failed without
// parsing the entire output itself — especially valuable after truncation.
//
// In addition to pass/fail counts, it extracts the FIRST failure's assertion
// detail (e.g., "Expected X, got Y") which is the most actionable information
// for fixing the issue.
func testResultSummary(output string) string {
	lower := strings.ToLower(output)

	// pytest: "X passed", "X failed", "X error"
	if strings.Contains(lower, "passed") && (strings.Contains(lower, "failed") || strings.Contains(lower, "error")) ||
		strings.Contains(lower, "===") && strings.Contains(lower, "passed") {
		// Find the pytest summary line (usually the last line with "passed" and/or "failed")
		lines := strings.Split(output, "\n")
		var summary string
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if (strings.Contains(lineLower, "passed") || strings.Contains(lineLower, "failed")) &&
				(strings.Contains(line, "=") || strings.Contains(line, "|") || strings.Contains(lineLower, "error")) {
				summary = "[test summary: " + line + "]"
				break
			}
		}
		// Extract all FAILED test names for actionable debugging.
		// pytest outputs "FAILED path::test_name" lines in its summary.
		var failedTests []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "FAILED ") {
				name := strings.TrimPrefix(trimmed, "FAILED ")
				// Strip the error description after " - "
				if dashIdx := strings.Index(name, " - "); dashIdx > 0 {
					name = name[:dashIdx]
				}
				failedTests = append(failedTests, name)
			}
		}
		if len(failedTests) > 0 && summary != "" {
			shown := failedTests
			if len(shown) > 10 {
				shown = shown[:10]
			}
			summary += "\n[failed tests: " + strings.Join(shown, ", ")
			if len(failedTests) > 10 {
				summary += fmt.Sprintf("... and %d more", len(failedTests)-10)
			}
			summary += "]"
		}
		// Append first failure detail for actionable debugging.
		// Only supplement an existing summary — don't return just the detail
		// when no pytest summary line was found, as the output may match a
		// more specific framework section below (cargo, jest, etc.).
		if detail := firstFailureDetail(output); detail != "" && summary != "" {
			summary += "\n" + detail
		}
		if summary != "" {
			return summary
		}
	}

	// Go test: look for "--- FAIL:" and "ok" lines
	if strings.Contains(output, "--- FAIL:") || strings.Contains(output, "FAIL\t") {
		var fails []string
		var passes []string
		for _, line := range strings.Split(output, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "--- FAIL: ") {
				// Extract test name
				name := strings.TrimPrefix(trimmed, "--- FAIL: ")
				if paren := strings.Index(name, " ("); paren > 0 {
					name = name[:paren]
				}
				fails = append(fails, name)
			} else if strings.HasPrefix(trimmed, "ok \t") || strings.HasPrefix(trimmed, "ok  \t") {
				passes = append(passes, trimmed)
			}
		}
		if len(fails) > 0 {
			summary := fmt.Sprintf("[test summary: %d test(s) FAILED", len(fails))
			if len(passes) > 0 {
				summary += fmt.Sprintf(", %d package(s) passed", len(passes))
			}
			// Show failing test names (up to 10)
			shown := fails
			if len(shown) > 10 {
				shown = shown[:10]
			}
			summary += ": " + strings.Join(shown, ", ")
			if len(fails) > 10 {
				summary += fmt.Sprintf("... and %d more", len(fails)-10)
			}
			summary += "]"
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// Python unittest: "Ran X tests in Y.ZZZs" + "OK" or "FAILED (failures=N, errors=M)"
	if strings.Contains(output, "Ran ") && strings.Contains(lower, "tests") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "failed") && strings.Contains(lineLower, "failures") {
				var summary string
				// Find the "Ran X tests" line too.
				for j := i - 1; j >= max(0, i-3); j-- {
					if strings.HasPrefix(strings.TrimSpace(lines[j]), "Ran ") {
						summary = "[test summary: " + strings.TrimSpace(lines[j]) + " — " + line + "]"
						break
					}
				}
				if summary == "" {
					summary = "[test summary: " + line + "]"
				}
				// Extract all FAIL: test_name lines for actionable debugging.
				var failedTests []string
				for _, fline := range lines {
					trimmed := strings.TrimSpace(fline)
					if strings.HasPrefix(trimmed, "FAIL: ") {
						name := strings.TrimPrefix(trimmed, "FAIL: ")
						failedTests = append(failedTests, name)
					}
				}
				if len(failedTests) > 0 {
					shown := failedTests
					if len(shown) > 10 {
						shown = shown[:10]
					}
					summary += "\n[failed tests: " + strings.Join(shown, ", ")
					if len(failedTests) > 10 {
						summary += fmt.Sprintf("... and %d more", len(failedTests)-10)
					}
					summary += "]"
				}
				if detail := firstFailureDetail(output); detail != "" {
					summary += "\n" + detail
				}
				return summary
			}
			if line == "OK" && i > 0 {
				prev := strings.TrimSpace(lines[i-1])
				if strings.HasPrefix(prev, "Ran ") {
					return "[test summary: " + prev + " — OK]"
				}
			}
		}
	}

	// Clojure (lein test / clj -X:test): "Ran N tests containing M assertions."
	// followed by "K failures, L errors." on the next line.
	// Must run before Jest (which also matches "tests:" + "total").
	if strings.Contains(output, "Ran ") && strings.Contains(lower, "assertions") &&
		strings.Contains(lower, "failures") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			// Match "N failures, M errors" (Clojure format, no "FAILED" keyword).
			if strings.Contains(lineLower, "failures") && strings.Contains(lineLower, "errors") &&
				!strings.Contains(lineLower, "failed") {
				var summary string
				for j := i - 1; j >= max(0, i-3); j-- {
					if strings.HasPrefix(strings.TrimSpace(lines[j]), "Ran ") {
						summary = "[test summary: " + strings.TrimSpace(lines[j]) + " — " + line + "]"
						break
					}
				}
				if summary == "" {
					summary = "[test summary: " + line + "]"
				}
				if !strings.HasPrefix(lineLower, "0 failures") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// npm/jest/vitest: "Tests: X failed, Y passed, Z total"
	if strings.Contains(lower, "tests:") && strings.Contains(lower, "total") {
		lines := strings.Split(output, "\n")
		var summary string
		for _, line := range lines {
			lineLower := strings.ToLower(strings.TrimSpace(line))
			if strings.Contains(lineLower, "tests:") && strings.Contains(lineLower, "total") {
				summary = "[test summary: " + strings.TrimSpace(line) + "]"
				break
			}
		}
		// Extract individual failing test suite names.
		// Jest/Vitest output "FAIL path/to/test.js" for failing suites.
		var failedSuites []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "FAIL ") && !strings.Contains(trimmed, "Tests:") {
				name := strings.TrimPrefix(trimmed, "FAIL ")
				failedSuites = append(failedSuites, name)
			}
		}
		if len(failedSuites) > 0 && summary != "" {
			shown := failedSuites
			if len(shown) > 10 {
				shown = shown[:10]
			}
			summary += "\n[failed suites: " + strings.Join(shown, ", ")
			if len(failedSuites) > 10 {
				summary += fmt.Sprintf("... and %d more", len(failedSuites)-10)
			}
			summary += "]"
		}
		if summary != "" {
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// Cargo test: "test result: ok. X passed; Y failed; Z ignored"
	if strings.Contains(output, "test result:") {
		lines := strings.Split(output, "\n")
		var summary string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "test result:") {
				summary = "[test summary: " + trimmed + "]"
				break
			}
		}
		// Extract individual failing test names.
		// Cargo outputs "test test_name ... FAILED" for each failing test.
		var failedTests []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "test ") && strings.HasSuffix(trimmed, " ... FAILED") {
				name := strings.TrimPrefix(trimmed, "test ")
				name = strings.TrimSuffix(name, " ... FAILED")
				failedTests = append(failedTests, name)
			}
		}
		if len(failedTests) > 0 && summary != "" {
			shown := failedTests
			if len(shown) > 10 {
				shown = shown[:10]
			}
			summary += "\n[failed tests: " + strings.Join(shown, ", ")
			if len(failedTests) > 10 {
				summary += fmt.Sprintf("... and %d more", len(failedTests)-10)
			}
			summary += "]"
		}
		if summary != "" {
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// Meson test: "Ok: N", "Fail: N", "Skipped: N", "Timeout: N" summary block.
	// Individual test lines: "1/3 test_name  OK  0.03s" / "2/3 test_name  FAIL  0.02s"
	if strings.Contains(output, "Ok:") &&
		(strings.Contains(output, "Timeout:") || strings.Contains(output, "Expected Fail:")) {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
			line := strings.TrimSpace(lines[i])
			if strings.HasPrefix(line, "Ok:") {
				// Gather the full summary block (Ok, Expected Fail, Fail, etc.)
				var parts []string
				for j := i; j < min(i+7, len(lines)); j++ {
					part := strings.TrimSpace(lines[j])
					if part == "" {
						break
					}
					parts = append(parts, part)
				}
				summary := "[test summary: meson — " + strings.Join(parts, " | ") + "]"
				if detail := firstFailureDetail(output); detail != "" {
					summary += "\n" + detail
				}
				return summary
			}
		}
	}

	// Bazel test: "Executed N out of N tests: X tests pass and Y fails locally."
	// Per-target: "//target:name  PASSED in 0.3s" / "//target:name  FAILED in 0.5s"
	if strings.Contains(lower, "executed") && strings.Contains(lower, " out of ") &&
		strings.Contains(lower, "test") {
		lines := strings.Split(output, "\n")
		var summary string
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.HasPrefix(lineLower, "executed") && strings.Contains(lineLower, "out of") &&
				strings.Contains(lineLower, "test") {
				summary = "[test summary: " + line + "]"
				break
			}
		}
		if summary != "" {
			// Extract failing target names (//target:name FAILED in Xs).
			var failedTargets []string
			for _, fline := range lines {
				trimmed := strings.TrimSpace(fline)
				if strings.HasPrefix(trimmed, "//") && strings.Contains(trimmed, "FAILED") {
					idx := strings.Index(trimmed, "FAILED")
					if idx > 0 {
						failedTargets = append(failedTargets, strings.TrimSpace(trimmed[:idx]))
					}
				}
			}
			if len(failedTargets) > 0 {
				shown := failedTargets
				if len(shown) > 10 {
					shown = shown[:10]
				}
				summary += "\n[failed targets: " + strings.Join(shown, ", ")
				if len(failedTargets) > 10 {
					summary += fmt.Sprintf("... and %d more", len(failedTargets)-10)
				}
				summary += "]"
			}
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// "X/Y tests passed" or "X out of Y" pattern (custom test scripts).
	// These are common in TB2 tasks that use custom test harnesses.
	{
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if (strings.Contains(lineLower, "passed") || strings.Contains(lineLower, "failed")) &&
				(strings.Contains(line, "/") || strings.Contains(lineLower, " out of ") || strings.Contains(lineLower, " of ")) &&
				!strings.Contains(line, "===") && // skip pytest output
				!strings.Contains(lineLower, ": error:") { // skip compiler/XCTest error lines
				return "[test summary: " + line + "]"
			}
		}
	}

	// Shell test scripts: look for reward/score output (TB2 pattern).
	if strings.Contains(lower, "reward") || strings.Contains(lower, "score") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if (strings.Contains(lineLower, "reward") || strings.Contains(lineLower, "score")) &&
				(strings.Contains(line, ":") || strings.Contains(line, "=")) {
				return "[test summary: " + line + "]"
			}
		}
	}

	// RSpec: "3 examples, 1 failure" or "5 examples, 0 failures"
	if strings.Contains(lower, "example") && strings.Contains(lower, "failure") {
		lines := strings.Split(output, "\n")
		var summary string
		for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "example") && strings.Contains(lineLower, "failure") {
				summary = "[test summary: " + line + "]"
				break
			}
		}
		// Extract failing example names from "rspec ./spec/path:42 # description" lines.
		// RSpec outputs these in a "Failed examples:" section at the end.
		var failedExamples []string
		inFailedSection := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Failed examples:") {
				inFailedSection = true
				continue
			}
			if inFailedSection && strings.HasPrefix(trimmed, "rspec ") {
				failedExamples = append(failedExamples, strings.TrimPrefix(trimmed, "rspec "))
			} else if inFailedSection && trimmed == "" {
				// Empty line after failed examples section — stop.
				if len(failedExamples) > 0 {
					break
				}
			}
		}
		if len(failedExamples) > 0 && summary != "" {
			shown := failedExamples
			if len(shown) > 10 {
				shown = shown[:10]
			}
			summary += "\n[failed examples: " + strings.Join(shown, ", ")
			if len(failedExamples) > 10 {
				summary += fmt.Sprintf("... and %d more", len(failedExamples)-10)
			}
			summary += "]"
		}
		if detail := firstFailureDetail(output); detail != "" && summary != "" {
			summary += "\n" + detail
		}
		if summary != "" {
			return summary
		}
	}

	// Ruby minitest: "X runs, Y assertions, Z failures, W errors"
	if strings.Contains(lower, "runs") && strings.Contains(lower, "assertions") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "runs") && strings.Contains(lineLower, "assertions") {
				summary := "[test summary: " + line + "]"
				if strings.Contains(lineLower, "failure") && !strings.Contains(lineLower, "0 failures") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// Mocha (Node.js): "N passing (Xms)" / "M failing" on separate lines.
	// Mocha uses "passing" and "failing" (present participle), not "passed"/"failed".
	if strings.Contains(lower, "passing") {
		lines := strings.Split(output, "\n")
		var passingLine, failingLine string
		for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
			line := strings.TrimSpace(lines[i])
			words := strings.Fields(strings.ToLower(line))
			if len(words) >= 2 && words[1] == "passing" && isNumeric(words[0]) {
				passingLine = line
			} else if len(words) >= 2 && words[1] == "failing" && isNumeric(words[0]) {
				failingLine = line
			}
		}
		if passingLine != "" {
			summary := "[test summary: " + passingLine
			if failingLine != "" {
				summary += ", " + failingLine
			}
			summary += "]"
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// Bun test: "N pass" / "N fail" on separate lines (not "passed"/"failed").
	// Also has "Ran N tests across M files." summary line.
	// Must check BEFORE PHPUnit — Bun uses bare "pass"/"fail" words.
	if strings.Contains(lower, "expect() calls") ||
		(strings.Contains(lower, "ran ") && strings.Contains(lower, " tests across ")) {
		lines := strings.Split(output, "\n")
		var passLine, failLine, ranLine string
		for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
			line := strings.TrimSpace(lines[i])
			words := strings.Fields(strings.ToLower(line))
			if len(words) >= 2 {
				if words[1] == "pass" && isNumeric(words[0]) && passLine == "" {
					passLine = line
				} else if words[1] == "fail" && isNumeric(words[0]) && failLine == "" {
					failLine = line
				}
			}
			lineLower := strings.ToLower(line)
			if strings.HasPrefix(lineLower, "ran ") && strings.Contains(lineLower, " tests across ") {
				ranLine = line
			}
		}
		if passLine != "" || failLine != "" {
			summary := "[test summary: "
			if passLine != "" {
				summary += passLine
			}
			if failLine != "" {
				if passLine != "" {
					summary += ", "
				}
				summary += failLine
			}
			if ranLine != "" {
				summary += " (" + ranLine + ")"
			}
			summary += "]"
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// PHPUnit: "Tests: N, Assertions: M, Failures: F" or "OK (N tests, M assertions)"
	if strings.Contains(lower, "phpunit") ||
		(strings.Contains(lower, "tests:") && strings.Contains(lower, "assertions:")) {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if (strings.Contains(lineLower, "tests:") && strings.Contains(lineLower, "assertions:")) ||
				(strings.HasPrefix(lineLower, "ok (") && strings.Contains(lineLower, "test")) {
				summary := "[test summary: " + line + "]"
				if strings.Contains(lineLower, "failure") || strings.Contains(lineLower, "error") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// GoogleTest (C++): "[  PASSED  ] N test(s)." / "[  FAILED  ] N test(s), listed below:"
	// Individual failures: "[  FAILED  ] TestSuite.TestCase"
	// Final line: "N FAILED TEST(S)"
	if strings.Contains(output, "[  PASSED  ]") || strings.Contains(output, "[  FAILED  ]") {
		lines := strings.Split(output, "\n")
		var passLine, failLine string
		var failedTests []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[  PASSED  ]") {
				passLine = trimmed
			} else if strings.HasPrefix(trimmed, "[  FAILED  ]") {
				if strings.Contains(trimmed, "listed below") || strings.Contains(trimmed, "test,") || strings.Contains(trimmed, "tests,") {
					failLine = trimmed
				} else {
					// Individual failing test name.
					name := strings.TrimPrefix(trimmed, "[  FAILED  ] ")
					// Strip timing suffix like " (0 ms)".
					if paren := strings.LastIndex(name, " ("); paren > 0 {
						name = name[:paren]
					}
					failedTests = append(failedTests, name)
				}
			}
		}
		if passLine != "" || failLine != "" {
			summary := "[test summary: "
			if passLine != "" {
				summary += passLine
			}
			if failLine != "" {
				if passLine != "" {
					summary += " / "
				}
				summary += failLine
			}
			summary += "]"
			if len(failedTests) > 0 {
				shown := failedTests
				if len(shown) > 10 {
					shown = shown[:10]
				}
				summary += "\n[failed tests: " + strings.Join(shown, ", ")
				if len(failedTests) > 10 {
					summary += fmt.Sprintf("... and %d more", len(failedTests)-10)
				}
				summary += "]"
			}
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// Catch2 (C++): "X test cases - Y assertions - Z failures"
	// or "All tests passed (X assertions in Y test cases)"
	if strings.Contains(lower, "test case") && strings.Contains(lower, "assertion") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "test case") && strings.Contains(lineLower, "assertion") {
				summary := "[test summary: " + line + "]"
				if strings.Contains(lineLower, "failed") || strings.Contains(lineLower, "failure") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// Maven/JUnit: "Tests run: X, Failures: Y, Errors: Z, Skipped: W"
	if strings.Contains(lower, "tests run:") && strings.Contains(lower, "failures:") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "tests run:") && strings.Contains(lineLower, "failures:") {
				summary := "[test summary: " + line + "]"
				if !strings.Contains(lineLower, "failures: 0") || strings.Contains(lineLower, "errors:") && !strings.Contains(lineLower, "errors: 0") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// Gradle: "X tests completed, Y failed" or "BUILD SUCCESSFUL/FAILED"
	if strings.Contains(lower, "tests completed") || (strings.Contains(lower, "build ") && (strings.Contains(lower, "successful") || strings.Contains(lower, "failed"))) {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "tests completed") ||
				(strings.HasPrefix(lineLower, "build ") && (strings.Contains(lineLower, "successful") || strings.Contains(lineLower, "failed"))) {
				summary := "[test summary: " + line + "]"
				if strings.Contains(lineLower, "failed") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// SBT (Scala): "[info] Tests: succeeded X, failed Y, canceled Z, ignored W, pending P"
	if strings.Contains(lower, "succeeded") && strings.Contains(lower, "failed") &&
		(strings.Contains(output, "[info]") || strings.Contains(lower, "sbt")) {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "succeeded") && strings.Contains(lineLower, "failed") {
				// Strip [info] prefix for cleaner summary.
				display := strings.TrimPrefix(line, "[info] ")
				summary := "[test summary: " + display + "]"
				if strings.Contains(lineLower, "failed") && !strings.HasSuffix(strings.TrimSpace(lineLower), "failed 0") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// Dart test: summary line "+X -Y: Some tests failed." or "+X: All tests passed!"
	// Individual lines: "00:00 +1 -1: test description"
	if (strings.Contains(output, "+") && strings.Contains(lower, "tests")) ||
		strings.Contains(output, "All tests passed") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			if (strings.Contains(line, "All tests passed") ||
				strings.Contains(strings.ToLower(line), "some tests failed")) &&
				strings.Contains(line, "+") {
				summary := "[test summary: " + line + "]"
				if strings.Contains(strings.ToLower(line), "failed") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// .NET: "Total tests: X, Passed: Y, Failed: Z"
	if strings.Contains(lower, "total tests:") && strings.Contains(lower, "passed:") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "total tests:") && strings.Contains(lineLower, "passed:") {
				summary := "[test summary: " + line + "]"
				if strings.Contains(lineLower, "failed:") && !strings.Contains(lineLower, "failed: 0") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// CTest: "X% tests passed, Y tests failed out of Z"
	if strings.Contains(lower, "tests passed") && strings.Contains(lower, "tests failed") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "tests passed") && strings.Contains(lineLower, "tests failed") {
				summary := "[test summary: " + line + "]"
				if detail := firstFailureDetail(output); detail != "" {
					summary += "\n" + detail
				}
				return summary
			}
		}
	}

	// Lua busted: "X successes / Y failures / Z errors / W pending : T seconds"
	// Note: busted uses "failure" (singular) or "failures" (plural).
	if strings.Contains(lower, "successes") && strings.Contains(lower, "failure") &&
		!strings.Contains(lower, "test result:") && !strings.Contains(lower, "test case") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "successes") && strings.Contains(lineLower, "failure") {
				summary := "[test summary: " + line + "]"
				if !strings.Contains(lineLower, "0 failure") {
					if detail := firstFailureDetail(output); detail != "" {
						summary += "\n" + detail
					}
				}
				return summary
			}
		}
	}

	// ExUnit (Elixir): "5 tests, 1 failure" or "3 doctests, 5 tests, 0 failures"
	// ExUnit uses "failure"/"failures", NOT "failed"/"passed" like most frameworks.
	if strings.Contains(lower, "failure") && !strings.Contains(lower, "examples") &&
		!strings.Contains(lower, "assertions") && !strings.Contains(lower, "tests run:") &&
		!strings.Contains(lower, "test result:") {
		lines := strings.Split(output, "\n")
		var summary string
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if !strings.Contains(lineLower, "test") || !strings.Contains(lineLower, "failure") {
				continue
			}
			// Verify ExUnit format: has "N test(s)" word pair.
			words := strings.Fields(lineLower)
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if nextWord == "test" || nextWord == "tests" {
						summary = "[test summary: " + line + "]"
						break
					}
				}
			}
			if summary != "" {
				break
			}
		}
		if summary != "" {
			// Extract failing test names: ExUnit outputs "N) test name (Module)"
			var failedTests []string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if len(trimmed) > 3 && trimmed[0] >= '1' && trimmed[0] <= '9' &&
					(strings.Contains(trimmed, ") test ") || strings.Contains(trimmed, ") doctest ")) {
					if parenIdx := strings.Index(trimmed, ") "); parenIdx > 0 {
						name := trimmed[parenIdx+2:]
						failedTests = append(failedTests, name)
					}
				}
			}
			if len(failedTests) > 0 {
				shown := failedTests
				if len(shown) > 10 {
					shown = shown[:10]
				}
				summary += "\n[failed tests: " + strings.Join(shown, ", ")
				if len(failedTests) > 10 {
					summary += fmt.Sprintf("... and %d more", len(failedTests)-10)
				}
				summary += "]"
			}
			if detail := firstFailureDetail(output); detail != "" {
				summary += "\n" + detail
			}
			return summary
		}
	}

	// XCTest (Swift): "Executed 5 tests, with 2 failures (0 unexpected) in 0.003 seconds"
	if strings.Contains(lower, "executed") && strings.Contains(lower, "test") &&
		strings.Contains(lower, "failure") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "executed") && strings.Contains(lineLower, "test") &&
				strings.Contains(lineLower, "failure") {
				summary := "[test summary: " + line + "]"
				if detail := firstFailureDetail(output); detail != "" {
					summary += "\n" + detail
				}
				return summary
			}
		}
	}

	// Zig test: "All N tests passed." (success-only format, no assertions/test cases
	// like Catch2). Zig failure format "N passed; N skipped; N failed." is already
	// caught by the pytest parser above.
	if strings.Contains(lower, "all") && strings.Contains(lower, "tests passed") &&
		!strings.Contains(lower, "assertion") && !strings.Contains(lower, "test case") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.HasPrefix(lineLower, "all ") && strings.Contains(lineLower, "tests passed") {
				return "[test summary: " + line + "]"
			}
		}
	}

	// R testthat: "[ FAIL 1 | WARN 0 | SKIP 0 | PASS 2 ]"
	if strings.Contains(output, "[ FAIL") || strings.Contains(output, "[ PASS") {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			if strings.HasPrefix(line, "[ FAIL") || strings.HasPrefix(line, "[ PASS") {
				if strings.Contains(line, "|") && strings.Contains(line, "]") {
					summary := "[test summary: " + line + "]"
					if strings.Contains(line, "FAIL") && !strings.HasPrefix(line, "[ FAIL 0") {
						if detail := firstFailureDetail(output); detail != "" {
							summary += "\n" + detail
						}
					}
					return summary
				}
			}
		}
	}

	// TAP (Test Anything Protocol): used by Perl prove, Node tap/tape, pg_prove, etc.
	// Format: "ok 1 - test name" / "not ok 2 - test name", plan "1..N"
	if strings.Contains(output, "ok ") && (strings.Contains(output, "1..") || strings.Contains(output, "not ok")) {
		p, f, tapOK := extractTAPCounts(output)
		if tapOK {
			total := p + f
			summary := fmt.Sprintf("[test summary: TAP %d/%d passed", p, total)
			if f > 0 {
				summary += fmt.Sprintf(", %d failed", f)
			}
			summary += "]"
			// Extract first failing test name.
			var summarySb4608 strings.Builder
			for _, line := range strings.Split(output, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "not ok ") {
					detail := trimmed
					if len(detail) > 150 {
						detail = detail[:150] + "..."
					}
					summarySb4608.WriteString("\n[first failure: " + detail + "]")
					break
				}
			}
			summary += summarySb4608.String()
			return summary
		}
	}

	// Generic: count PASS/FAIL lines
	if strings.Contains(upper(output), "FAIL") || strings.Contains(upper(output), "PASS") {
		passCount := strings.Count(upper(output), "\nPASS")
		failCount := strings.Count(upper(output), "\nFAIL")
		if passCount+failCount >= 3 {
			return fmt.Sprintf("[test summary: %d PASS, %d FAIL out of %d tests]",
				passCount, failCount, passCount+failCount)
		}
	}

	// "No tests collected" / "no tests ran" — pytest exits with code 5,
	// other frameworks print "0 tests". The agent often doesn't understand
	// why tests weren't found. Provide actionable guidance.
	if strings.Contains(lower, "no tests ran") ||
		strings.Contains(lower, "collected 0 items") ||
		strings.Contains(lower, "no tests were run") ||
		strings.Contains(lower, "no test classes") ||
		(strings.Contains(lower, "0 tests") && !strings.Contains(lower, "0 tests passed")) {
		return "[test summary: NO TESTS FOUND — check that test files and test function names match the framework's discovery patterns (e.g., pytest needs test_*.py files with test_ functions, unittest needs test* methods in TestCase classes, Go needs Test* functions in *_test.go files)]"
	}

	return ""
}

func upper(s string) string {
	return strings.ToUpper(s)
}

// firstFailureDetail extracts the assertion detail from the FIRST test failure
// in the output. This is the most actionable information for the agent —
// knowing "Expected 42, got 43" or "AssertionError: lists differ" tells it
// exactly what to fix, saving 1-2 turns of reading test output.
//
// Scans for common assertion patterns across pytest, unittest, Go, Rust, Jest.
func firstFailureDetail(output string) string {
	lines := strings.Split(output, "\n")

	// Patterns that indicate an assertion/comparison failure with details.
	// We want the FIRST one — it's the most useful for "fix one at a time".
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// pytest: "AssertionError: assert X == Y" or "E       assert X == Y"
		if strings.HasPrefix(trimmed, "E ") && (strings.Contains(trimmed, "assert") ||
			strings.Contains(trimmed, "==") || strings.Contains(trimmed, "!=") ||
			strings.Contains(trimmed, "Error") || strings.Contains(trimmed, "not in") ||
			strings.Contains(trimmed, "in ")) {
			detail := strings.TrimPrefix(trimmed, "E ")
			detail = strings.TrimSpace(detail)
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// pytest/unittest/Deno: "AssertionError: ..." on its own line.
		// Deno prefixes with "error: " → "error: AssertionError: Values are not equal:"
		if strings.HasPrefix(trimmed, "AssertionError:") || strings.HasPrefix(trimmed, "AssertionError(") ||
			strings.HasPrefix(trimmed, "error: AssertionError:") {
			detail := trimmed
			// Look ahead for diff details (Deno format: "[Diff] Actual / Expected").
			var detailSb4684 strings.Builder
			for j := i + 1; j < min(i+6, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				if strings.HasPrefix(ahead, "-") || strings.HasPrefix(ahead, "+") {
					detailSb4684.WriteString(" / " + ahead)
					break
				}
			}
			detail += detailSb4684.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// Python unittest: "FAIL: test_name (module.TestClass)"
		// followed by assertion details in subsequent lines
		if strings.HasPrefix(trimmed, "FAIL: ") && strings.Contains(trimmed, "(") {
			// Look ahead for the actual assertion
			for j := i + 1; j < min(i+8, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				if strings.HasPrefix(ahead, "AssertionError:") ||
					strings.Contains(ahead, "!=") && strings.Contains(ahead, "assert") ||
					strings.HasPrefix(ahead, "Expected") ||
					strings.HasPrefix(ahead, "Got") {
					if len(ahead) > 200 {
						ahead = ahead[:200] + "..."
					}
					return "[first failure: " + ahead + "]"
				}
			}
		}

		// Go test: lines right after "--- FAIL:" often have "expected X, got Y"
		// or "want X, got Y"
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			for j := i + 1; j < min(i+8, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if (strings.Contains(aheadLower, "expected") || strings.Contains(aheadLower, "want ")) &&
					(strings.Contains(aheadLower, "got ") || strings.Contains(aheadLower, "actual")) {
					if len(ahead) > 200 {
						ahead = ahead[:200] + "..."
					}
					return "[first failure: " + ahead + "]"
				}
				// Go test: Error() or Errorf() messages
				if strings.Contains(ahead, "Error Trace:") {
					// Skip trace, look for the message
					for k := j + 1; k < min(j+5, len(lines)); k++ {
						msg := strings.TrimSpace(lines[k])
						if strings.HasPrefix(msg, "Error:") || strings.HasPrefix(msg, "Messages:") {
							if len(msg) > 200 {
								msg = msg[:200] + "..."
							}
							return "[first failure: " + msg + "]"
						}
					}
				}
			}
		}

		// Classic diff format: "< expected\n---\n> actual" (common in TB2 test.sh scripts)
		if strings.HasPrefix(trimmed, "< ") && i+2 < len(lines) {
			sep := strings.TrimSpace(lines[i+1])
			nextLine := strings.TrimSpace(lines[i+2])
			if sep == "---" && strings.HasPrefix(nextLine, "> ") {
				expected := strings.TrimPrefix(trimmed, "< ")
				actual := strings.TrimPrefix(nextLine, "> ")
				detail := fmt.Sprintf("diff: expected %q, got %q", expected, actual)
				if len(detail) > 200 {
					detail = detail[:200] + "..."
				}
				return "[first failure: " + detail + "]"
			}
		}

		// Unified diff header: "@@ -N,M +N,M @@" — extract first changed line pair
		if strings.HasPrefix(trimmed, "@@ ") && len(trimmed) > 3 && strings.Contains(trimmed[3:], " @@") {
			for j := i + 1; j < min(i+20, len(lines)); j++ {
				jLine := lines[j]
				if strings.HasPrefix(jLine, "-") && !strings.HasPrefix(jLine, "---") {
					expected := strings.TrimPrefix(jLine, "-")
					if j+1 < len(lines) && strings.HasPrefix(lines[j+1], "+") && !strings.HasPrefix(lines[j+1], "+++") {
						actual := strings.TrimPrefix(lines[j+1], "+")
						detail := fmt.Sprintf("diff: expected %q, got %q", strings.TrimSpace(expected), strings.TrimSpace(actual))
						if len(detail) > 200 {
							detail = detail[:200] + "..."
						}
						return "[first failure: " + detail + "]"
					}
					break
				}
			}
		}

		// ExUnit (Elixir): "Assertion with == failed" followed by "left:"/"right:"
		if strings.HasPrefix(strings.ToLower(trimmed), "assertion with") && strings.Contains(strings.ToLower(trimmed), "failed") {
			detail := trimmed
			var detailSb4781 strings.Builder
			for j := i + 1; j < min(i+6, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "left:") || strings.HasPrefix(aheadLower, "right:") {
					detailSb4781.WriteString(" / " + ahead)
				}
			}
			detail += detailSb4781.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// XCTest (Swift): "XCTAssertEqual failed: (\"X\") is not equal to (\"Y\")"
		if strings.Contains(trimmed, "XCTAssert") && strings.Contains(strings.ToLower(trimmed), "failed") {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return "[first failure: " + trimmed + "]"
		}

		// NUnit (.NET): "Expected: X" / "But was:  Y" on consecutive lines.
		// Also handles Dart test and xUnit.net which use "Actual:" instead.
		if strings.HasPrefix(trimmed, "Expected:") && i+1 < len(lines) {
			nextTrimmed := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(nextTrimmed, "But was:") || strings.HasPrefix(nextTrimmed, "Actual:") {
				detail := trimmed + " / " + nextTrimmed
				if len(detail) > 200 {
					detail = detail[:200] + "..."
				}
				return "[first failure: " + detail + "]"
			}
		}

		// GoogleTest (C++): "Value of: X" / "  Actual: Y" / "Expected: Z"
		// or "Expected equality of these values:" followed by values on next lines.
		if strings.HasPrefix(trimmed, "Value of:") && i+2 < len(lines) {
			detail := trimmed
			var detailSb4819 strings.Builder
			for j := i + 1; j < min(i+4, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "actual:") || strings.HasPrefix(aheadLower, "expected:") ||
					strings.HasPrefix(aheadLower, "which is:") {
					detailSb4819.WriteString(" / " + ahead)
				}
			}
			detail += detailSb4819.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}
		if strings.HasPrefix(trimmed, "Expected equality of these values:") && i+1 < len(lines) {
			detail := trimmed
			var detailSb4834 strings.Builder
			for j := i + 1; j < min(i+5, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				if ahead == "" {
					break
				}
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "which is:") {
					detailSb4834.WriteString(" / " + ahead)
				} else if !strings.HasPrefix(ahead, "#") {
					detail += " / " + ahead
				}
			}
			detail += detailSb4834.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// Catch2 (C++): "file.cpp:42: FAILED:" followed by assertion and expansion.
		// Format:
		//   /path/test.cpp:42: FAILED:
		//     CHECK( result == 42 )
		//   with expansion:
		//     43 == 42
		if strings.HasSuffix(trimmed, "FAILED:") && strings.Contains(trimmed, ":") {
			// Verify it looks like a file:line: prefix (not just a random "FAILED:").
			parts := strings.SplitN(trimmed, ":", 3)
			if len(parts) >= 3 && isNumeric(strings.TrimSpace(parts[len(parts)-2])) {
				detail := ""
				for j := i + 1; j < min(i+6, len(lines)); j++ {
					ahead := strings.TrimSpace(lines[j])
					aheadLower := strings.ToLower(ahead)
					if strings.HasPrefix(aheadLower, "check(") || strings.HasPrefix(aheadLower, "require(") ||
						strings.HasPrefix(aheadLower, "check_") || strings.HasPrefix(aheadLower, "require_") ||
						strings.HasPrefix(ahead, "CHECK(") || strings.HasPrefix(ahead, "REQUIRE(") ||
						strings.HasPrefix(ahead, "CHECK_") || strings.HasPrefix(ahead, "REQUIRE_") {
						detail = ahead
					}
					if aheadLower == "with expansion:" {
						// Next line has the expanded values.
						if j+1 < len(lines) {
							expansion := strings.TrimSpace(lines[j+1])
							if detail != "" {
								detail += " => " + expansion
							} else {
								detail = expansion
							}
						}
						break
					}
				}
				if detail == "" {
					detail = trimmed
				}
				if len(detail) > 200 {
					detail = detail[:200] + "..."
				}
				return "[first failure: " + detail + "]"
			}
		}

		// Boost.Test (C++): "file.cpp(42): error: in \"test_name\": check X == Y has failed [A != B]"
		if strings.Contains(trimmed, ": error:") && strings.Contains(strings.ToLower(trimmed), "has failed") {
			// Extract the check expression and optional expansion in brackets.
			idx := strings.Index(strings.ToLower(trimmed), "check ")
			if idx >= 0 {
				detail := trimmed[idx:]
				if len(detail) > 200 {
					detail = detail[:200] + "..."
				}
				return "[first failure: " + detail + "]"
			}
		}

		// Rust: thread 'test_name' panicked at 'assertion `left == right` failed'
		if strings.Contains(trimmed, "panicked at") {
			lower := strings.ToLower(trimmed)
			if strings.Contains(lower, "assert") || strings.Contains(lower, "left") || strings.Contains(lower, "unwrap") {
				detail := trimmed
				var detailSb4913 strings.Builder
				for j := i + 1; j < min(i+5, len(lines)); j++ {
					ahead := strings.TrimSpace(lines[j])
					aheadLower := strings.ToLower(ahead)
					if strings.HasPrefix(aheadLower, "left:") || strings.HasPrefix(aheadLower, "right:") {
						detailSb4913.WriteString(" / " + ahead)
					}
				}
				detail += detailSb4913.String()
				if len(detail) > 200 {
					detail = detail[:200] + "..."
				}
				return "[first failure: " + detail + "]"
			}
		}

		// RSpec/Ruby/HSpec: "expected: X" / "got: Y" or "but got: Y" on separate lines
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(trimmed)), "expected:") &&
			!strings.HasPrefix(trimmed, "Expected:") { // skip Jest (already handled above)
			detail := trimmed
			if i+1 < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[i+1])
				nextLower := strings.ToLower(nextTrimmed)
				if strings.HasPrefix(nextLower, "got:") || strings.HasPrefix(nextLower, "actual:") ||
					strings.HasPrefix(nextLower, "but got:") { // HSpec uses "but got:"
					detail += " / " + nextTrimmed
				}
			}
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// Generic "Expected X, got Y" pattern (many test frameworks)
		lower := strings.ToLower(trimmed)
		if (strings.Contains(lower, "expected") && strings.Contains(lower, "got")) ||
			(strings.Contains(lower, "expected") && strings.Contains(lower, "actual")) ||
			(strings.Contains(lower, "expected") && strings.Contains(lower, "received")) {
			// Make sure it's an actual assertion, not just a comment or header.
			// Headers like "Comparing expected vs actual:" don't contain values.
			if !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "//") &&
				!strings.HasPrefix(trimmed, "*") && len(trimmed) > 10 &&
				!strings.HasSuffix(trimmed, ":") { // skip headers ending in colon
				if len(trimmed) > 200 {
					trimmed = trimmed[:200] + "..."
				}
				return "[first failure: " + trimmed + "]"
			}
		}

		// Jest/Vitest: "Expected: X" / "Received: Y" (consecutive lines)
		if strings.HasPrefix(trimmed, "Expected:") || strings.HasPrefix(trimmed, "- Expected") {
			detail := trimmed
			// Grab the "Received:" line too if it follows
			if i+1 < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(nextTrimmed, "Received:") || strings.HasPrefix(nextTrimmed, "+ Received") {
					detail += " / " + nextTrimmed
				}
			}
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// JUnit/Maven: "expected:<X> but was:<Y>" (Java test frameworks)
		if strings.Contains(lower, "expected:<") && strings.Contains(lower, "but was:<") {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return "[first failure: " + trimmed + "]"
		}

		// JUnit5: "expected: <X> but was: <Y>" (with spaces around angle brackets)
		if strings.Contains(lower, "expected: <") && strings.Contains(lower, "but was: <") {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return "[first failure: " + trimmed + "]"
		}

		// Mocha/Chai: "AssertionError: expected X to equal Y" or "to deeply equal"
		if strings.Contains(trimmed, "AssertionError:") && strings.Contains(lower, " to ") {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return "[first failure: " + trimmed + "]"
		}

		// PHPUnit: "Failed asserting that X matches/equals/is Y"
		if strings.HasPrefix(trimmed, "Failed asserting that") {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return "[first failure: " + trimmed + "]"
		}

		// Shell test scripts: "FAIL: expected 'X', got 'Y'" or "FAIL - expected X got Y"
		// Common in TB2 custom test harnesses.
		if (strings.HasPrefix(upper(trimmed), "FAIL") || strings.HasPrefix(trimmed, "ERROR")) &&
			(strings.Contains(lower, "expected") || strings.Contains(lower, "mismatch")) {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return "[first failure: " + trimmed + "]"
		}

		// Perl Test::More/Test2: "# Failed test 'name'" followed by
		// "#          got: 'X'" and "#     expected: 'Y'" (with # prefix).
		if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "Failed test") {
			var got, expected string
			for j := i + 1; j < min(i+6, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				if !strings.HasPrefix(ahead, "#") {
					break
				}
				inner := strings.TrimSpace(strings.TrimPrefix(ahead, "#"))
				innerLower := strings.ToLower(inner)
				if strings.HasPrefix(innerLower, "got:") {
					got = inner
				} else if strings.HasPrefix(innerLower, "expected:") {
					expected = inner
				}
			}
			if got != "" && expected != "" {
				detail := got + " / " + expected
				if len(detail) > 200 {
					detail = detail[:200] + "..."
				}
				return "[first failure: " + detail + "]"
			}
		}

		// R testthat: "Failure (test-file.R:line:col): description" followed by
		// "`expr` not equal to expected." or "value` not identical to ..."
		if strings.Contains(trimmed, "Failure (") && strings.HasSuffix(trimmed, ")") ||
			(strings.Contains(trimmed, "Failure") && strings.Contains(trimmed, ".R:")) {
			detail := trimmed
			// Look ahead for the assertion detail.
			var detailSb5052 strings.Builder
			for j := i + 1; j < min(i+4, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.Contains(aheadLower, "not equal") || strings.Contains(aheadLower, "not identical") ||
					strings.Contains(aheadLower, "not true") || strings.Contains(aheadLower, "threw an error") {
					detailSb5052.WriteString(" — " + ahead)
					break
				}
			}
			detail += detailSb5052.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// ScalaTest: "X did not equal Y" or "X was not equal to Y"
		if (strings.Contains(lower, "did not equal") || strings.Contains(lower, "was not equal to")) &&
			!strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "//") {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return "[first failure: " + trimmed + "]"
		}

		// CTest/CMake: "The following tests FAILED:" followed by test list
		if strings.Contains(trimmed, "The following tests FAILED:") {
			// Look ahead for specific test names
			for j := i + 1; j < min(i+5, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				if ahead != "" && !strings.HasPrefix(ahead, "Errors while") {
					if len(ahead) > 150 {
						ahead = ahead[:150] + "..."
					}
					return "[first failure: " + ahead + "]"
				}
			}
		}

		// Nim unittest: "[FAILED] test_name" followed by check details.
		// Format:
		//   [FAILED] test addition
		//     /path/test.nim(42)
		//     Check failed: actual == expected
		//     actual: 3
		//     expected: 4
		if strings.HasPrefix(trimmed, "[FAILED]") {
			detail := trimmed
			var detailSb5099 strings.Builder
			for j := i + 1; j < min(i+6, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "check failed") ||
					strings.HasPrefix(aheadLower, "actual:") ||
					strings.HasPrefix(aheadLower, "expected:") {
					detailSb5099.WriteString(" / " + ahead)
				}
			}
			detail += detailSb5099.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// Zig test: "Test [N/M] test.name... FAIL" followed by error detail.
		// Or: "error: expected X, found Y"
		if strings.Contains(trimmed, "... FAIL") && strings.Contains(trimmed, "Test [") {
			detail := trimmed
			// Look ahead for the error detail line.
			var detailSb5119 strings.Builder
			for j := i + 1; j < min(i+5, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "error:") ||
					strings.Contains(aheadLower, "expected") {
					detailSb5119.WriteString(" — " + ahead)
					break
				}
			}
			detail += detailSb5119.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// Clojure (lein test): "FAIL in (test-name) (file.clj:42)" or
		// "ERROR in (test-name) (file.clj:42)" followed by
		// "expected: (= X Y)" and "  actual: (not (= X Y))".
		if (strings.HasPrefix(trimmed, "FAIL in (") || strings.HasPrefix(trimmed, "ERROR in (")) &&
			(strings.Contains(trimmed, ".clj:") || strings.Contains(trimmed, ".cljc:") || strings.Contains(trimmed, ".cljs:")) {
			detail := trimmed
			var detailSb5140 strings.Builder
			for j := i + 1; j < min(i+4, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "expected:") || strings.HasPrefix(aheadLower, "actual:") {
					detailSb5140.WriteString(" / " + ahead)
				}
			}
			detail += detailSb5140.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// Erlang EUnit: "module_tests: test_name...*failed*" followed by
		// error detail lines containing assertEqual/assertMatch info.
		if strings.Contains(trimmed, "*failed*") && strings.Contains(trimmed, "...*") {
			detail := trimmed
			var detailSb5157 strings.Builder
			for j := i + 1; j < min(i+8, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.Contains(aheadLower, "expected") || strings.Contains(aheadLower, "value") ||
					strings.Contains(aheadLower, "assertequal") || strings.Contains(aheadLower, "assertmatch") {
					detailSb5157.WriteString(" / " + ahead)
					break
				}
			}
			detail += detailSb5157.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// Erlang Common Test: "%%% suite ==> test_case: FAILED" or
		// "=== Test failed ===" followed by reason on next lines.
		if (strings.Contains(trimmed, ": FAILED") && strings.HasPrefix(trimmed, "%%%")) ||
			strings.Contains(trimmed, "=== Test failed ===") {
			detail := trimmed
			var detailSb5177 strings.Builder
			for j := i + 1; j < min(i+5, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "reason") || strings.Contains(aheadLower, "assertEqual") ||
					strings.Contains(aheadLower, "error") {
					detailSb5177.WriteString(" / " + ahead)
					break
				}
			}
			detail += detailSb5177.String()
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			return "[first failure: " + detail + "]"
		}

		// HSpec (Haskell): "FAILED [N]" on its own line, details on subsequent lines.
		// Format:
		//   1) Module.function
		//        expected: X
		//         but got: Y
		if strings.HasPrefix(trimmed, "FAILED [") && strings.HasSuffix(trimmed, "]") {
			// Look ahead for assertion detail.
			for j := i + 1; j < min(i+6, len(lines)); j++ {
				ahead := strings.TrimSpace(lines[j])
				aheadLower := strings.ToLower(ahead)
				if strings.HasPrefix(aheadLower, "expected:") {
					detail := ahead
					if j+1 < len(lines) {
						nextAhead := strings.TrimSpace(lines[j+1])
						nextLower := strings.ToLower(nextAhead)
						if strings.HasPrefix(nextLower, "but got:") {
							detail += " / " + nextAhead
						}
					}
					if len(detail) > 200 {
						detail = detail[:200] + "..."
					}
					return "[first failure: " + detail + "]"
				}
			}
		}
	}

	return ""
}

// testFailureFingerprint creates a stable fingerprint of a test failure.
// Used to detect when the same test fails identically after edits (stale failure).
// Uses the first failure's assertion detail as the fingerprint — if the first
// failure assertion is identical, the agent's fix was ineffective.
func testFailureFingerprint(output string) string {
	return firstFailureDetail(output)
}

// extractTestCounts parses test output to extract pass/fail counts.
// Returns (passed, failed, ok) where ok indicates whether counts were found.
// Supports pytest, unittest, Go test, jest, cargo test, custom "X/Y" patterns.
func extractTestCounts(output string) (passed, failed int, ok bool) {
	lower := strings.ToLower(output)
	lines := strings.Split(output, "\n")

	// Catch2 (C++): "test cases: 5 | 4 passed | 1 failed"
	// or "All tests passed (X assertions in Y test cases)"
	// MUST run before pytest — Catch2's "assertions" line has "passed"/"failed"
	// which the pytest section would incorrectly parse as test counts.
	if strings.Contains(lower, "test case") && strings.Contains(lower, "assertion") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "test case") {
				continue
			}
			// "All tests passed (X assertions in Y test cases)"
			if strings.HasPrefix(line, "all tests passed") {
				var assertions, cases int
				_, _ = fmt.Sscanf(extractAfter(line, "("), "%d assertions in %d", &assertions, &cases)
				if cases > 0 {
					passed = cases
					ok = true
					return passed, failed, ok
				}
			}
			// "test cases: 5 | 4 passed | 1 failed"
			// Strip | separators and parse "N passed" / "N failed" directly.
			cleaned := strings.ReplaceAll(line, "|", " ")
			words := strings.Fields(cleaned)
			var p, f int
			foundPF := false
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if nextWord == "passed" {
						p = n
						foundPF = true
					} else if nextWord == "failed" || strings.HasPrefix(nextWord, "failure") {
						f = n
						foundPF = true
					}
				}
			}
			if foundPF {
				passed = p
				failed = f
				ok = true
				return passed, failed, ok
			}
		}
	}

	// pytest: "X passed, Y failed" or "X passed" (often in "==== 3 passed, 2 failed ====")
	if strings.Contains(lower, "passed") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "passed") {
				continue
			}
			// Extract "N passed" and "M failed" using word-boundary scanning.
			// Split into words and look for number + "passed"/"failed" pairs.
			// Strip trailing punctuation (commas) from keyword matching.
			words := strings.Fields(line)
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					switch {
					case nextWord == "passed":
						passed = n
						ok = true
					case nextWord == "failed":
						failed += n
						ok = true
					case strings.HasPrefix(nextWord, "error"):
						failed += n
						ok = true
					}
				}
			}
			if ok {
				return passed, failed, ok
			}
		}
	}

	// Go test: count "--- FAIL:" and "--- PASS:" lines.
	if strings.Contains(output, "--- FAIL:") || strings.Contains(output, "--- PASS:") {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "--- FAIL:") {
				failed++
				ok = true
			} else if strings.HasPrefix(trimmed, "--- PASS:") {
				passed++
				ok = true
			}
		}
		if ok {
			return passed, failed, ok
		}
	}

	// Python unittest: "FAILED (failures=N, errors=M)" or "Ran X tests... OK"
	// Pre-scan for "Ran X tests" line (may appear before the result line).
	if strings.Contains(output, "Ran ") && strings.Contains(lower, "tests") {
		var total int
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Ran ") {
				_, _ = fmt.Sscanf(trimmed, "Ran %d", &total)
			}
		}
		// Now scan from end for result line.
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "failures=") || strings.Contains(lineLower, "errors=") {
				var f, e int
				_, _ = fmt.Sscanf(extractAfter(lineLower, "failures="), "%d", &f)
				_, _ = fmt.Sscanf(extractAfter(lineLower, "errors="), "%d", &e)
				failed = f + e
				if total > 0 {
					passed = total - failed
				}
				ok = total > 0
				return passed, failed, ok
			}
			if line == "OK" && total > 0 {
				passed = total
				ok = true
				return passed, failed, ok
			}
		}
	}

	// Clojure (lein test): "Ran N tests containing M assertions." + "K failures, L errors."
	if strings.Contains(output, "Ran ") && strings.Contains(lower, "assertions") &&
		strings.Contains(lower, "failures") {
		var total int
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Ran ") {
				_, _ = fmt.Sscanf(trimmed, "Ran %d", &total)
			}
		}
		for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
			lineLower := strings.ToLower(strings.TrimSpace(lines[i]))
			if strings.Contains(lineLower, "failures") && strings.Contains(lineLower, "errors") &&
				!strings.Contains(lineLower, "failures=") {
				var f, e int
				words := strings.Fields(lineLower)
				for j := 0; j+1 < len(words); j++ {
					if isNumeric(words[j]) {
						var n int
						_, _ = fmt.Sscanf(words[j], "%d", &n)
						nextWord := strings.TrimRight(words[j+1], ",.;:")
						if strings.HasPrefix(nextWord, "failure") {
							f = n
						} else if strings.HasPrefix(nextWord, "error") {
							e = n
						}
					}
				}
				failed = f + e
				if total > 0 {
					passed = total - failed
					ok = true
					return passed, failed, ok
				}
			}
		}
	}

	// Jest/Vitest: "Tests:  X passed, Y failed, Z total" or "Test Suites:  X passed, Y failed, Z total"
	if strings.Contains(lower, "tests:") && (strings.Contains(lower, "total") || strings.Contains(lower, "passed")) {
		for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.HasPrefix(line, "tests:") {
				continue
			}
			words := strings.Fields(line)
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					switch nextWord {
					case "passed":
						passed = n
						ok = true
					case "failed":
						failed += n
						ok = true
					}
				}
			}
			if ok {
				return passed, failed, ok
			}
		}
	}

	// Cargo test: "test result: FAILED. X passed; Y failed; Z ignored; ..."
	// or "test result: ok. X passed; Y failed; ..."
	if strings.Contains(lower, "test result:") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "test result:") {
				continue
			}
			words := strings.Fields(line)
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					switch nextWord {
					case "passed":
						passed = n
						ok = true
					case "failed":
						failed += n
						ok = true
					}
				}
			}
			if ok {
				return passed, failed, ok
			}
		}
	}

	// Meson test: "Ok: N", "Fail: N" summary lines.
	if strings.Contains(output, "Ok:") &&
		(strings.Contains(output, "Timeout:") || strings.Contains(output, "Expected Fail:")) {
		var mesonOk, mesonFail int
		foundMeson := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Ok:") {
				_, _ = fmt.Sscanf(extractAfter(trimmed, "Ok:"), "%d", &mesonOk)
				foundMeson = true
			} else if strings.HasPrefix(trimmed, "Fail:") {
				_, _ = fmt.Sscanf(extractAfter(trimmed, "Fail:"), "%d", &mesonFail)
			}
		}
		if foundMeson {
			passed = mesonOk
			failed = mesonFail
			ok = true
			return passed, failed, ok
		}
	}

	// Bazel test: "Executed N out of N tests: X tests pass and Y fails locally."
	if strings.Contains(lower, "executed") && strings.Contains(lower, " out of ") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if !strings.HasPrefix(lineLower, "executed") || !strings.Contains(lineLower, "out of") ||
				!strings.Contains(lineLower, "test") {
				continue
			}
			words := strings.Fields(lineLower)
			var p, f int
			foundBazel := false
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if nextWord == "tests" && j+2 < len(words) {
						nextNextWord := strings.TrimRight(words[j+2], ",.;:")
						if nextNextWord == "pass" {
							p = n
							foundBazel = true
						}
					} else if nextWord == "fails" || nextWord == "fail" {
						f = n
						foundBazel = true
					}
				}
			}
			if foundBazel {
				passed = p
				failed = f
				ok = true
				return passed, failed, ok
			}
		}
	}

	// RSpec: "X examples, Y failures" or "X examples, 0 failures"
	if strings.Contains(lower, "examples") && strings.Contains(lower, "failure") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "example") {
				continue
			}
			words := strings.Fields(line)
			var examples, failures int
			foundExamples := false
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if strings.HasPrefix(nextWord, "example") {
						examples = n
						foundExamples = true
					} else if strings.HasPrefix(nextWord, "failure") {
						failures = n
					}
				}
			}
			if foundExamples {
				passed = examples - failures
				failed = failures
				ok = true
				return passed, failed, ok
			}
		}
	}

	// Mocha: "N passing" / "M failing" (separate lines near end of output)
	if strings.Contains(lower, "passing") && (strings.Contains(lower, "failing") || !strings.Contains(lower, "fail")) {
		var p, f int
		foundPassing := false
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			words := strings.Fields(lineLower)
			if len(words) >= 2 {
				if words[1] == "passing" && isNumeric(words[0]) {
					_, _ = fmt.Sscanf(words[0], "%d", &p)
					foundPassing = true
				} else if words[1] == "failing" && isNumeric(words[0]) {
					_, _ = fmt.Sscanf(words[0], "%d", &f)
				}
			}
		}
		if foundPassing {
			passed = p
			failed = f
			ok = true
			return passed, failed, ok
		}
	}

	// Bun test: "N pass" / "N fail" on separate lines.
	// Bun uses bare "pass"/"fail" (not "passed"/"failed" or "passing"/"failing").
	if strings.Contains(lower, "expect() calls") ||
		(strings.Contains(lower, "ran ") && strings.Contains(lower, " tests across ")) {
		var p, f int
		foundBun := false
		for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
			words := strings.Fields(strings.ToLower(strings.TrimSpace(lines[i])))
			if len(words) >= 2 && isNumeric(words[0]) {
				var n int
				_, _ = fmt.Sscanf(words[0], "%d", &n)
				switch words[1] {
				case "pass":
					p = n
					foundBun = true
				case "fail":
					f = n
					foundBun = true
				}
			}
		}
		if foundBun {
			passed = p
			failed = f
			ok = true
			return passed, failed, ok
		}
	}

	// PHPUnit: "Tests: N, Assertions: M, Failures: F, Errors: E"
	// or "OK (N tests, M assertions)"
	if strings.Contains(lower, "phpunit") || (strings.Contains(lower, "tests:") && strings.Contains(lower, "assertions:")) {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "tests:") && strings.Contains(lineLower, "assertions:") {
				var t, f, e int
				// Parse "Tests: N, Assertions: M, Failures: F, Errors: E"
				_, _ = fmt.Sscanf(extractAfter(lineLower, "tests:"), "%d", &t)
				_, _ = fmt.Sscanf(extractAfter(lineLower, "failures:"), "%d", &f)
				_, _ = fmt.Sscanf(extractAfter(lineLower, "errors:"), "%d", &e)
				if t > 0 {
					failed = f + e
					passed = t - failed
					ok = true
					return passed, failed, ok
				}
			}
			// PHPUnit OK format: "OK (N tests, M assertions)"
			if strings.HasPrefix(lineLower, "ok (") && strings.Contains(lineLower, "test") {
				var t int
				_, _ = fmt.Sscanf(strings.TrimPrefix(lineLower, "ok ("), "%d", &t)
				if t > 0 {
					passed = t
					ok = true
					return passed, failed, ok
				}
			}
		}
	}

	// Maven/JUnit: "Tests run: N, Failures: F, Errors: E, Skipped: S"
	if strings.Contains(lower, "tests run:") && strings.Contains(lower, "failures:") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "tests run:") {
				var t, f, e, s int
				_, _ = fmt.Sscanf(extractAfter(lineLower, "tests run:"), "%d", &t)
				_, _ = fmt.Sscanf(extractAfter(lineLower, "failures:"), "%d", &f)
				_, _ = fmt.Sscanf(extractAfter(lineLower, "errors:"), "%d", &e)
				_, _ = fmt.Sscanf(extractAfter(lineLower, "skipped:"), "%d", &s)
				if t > 0 {
					failed = f + e
					passed = t - failed - s
					ok = true
					return passed, failed, ok
				}
			}
		}
	}

	// Ruby minitest: "X runs, Y assertions, Z failures, W errors"
	if strings.Contains(lower, "runs") && strings.Contains(lower, "assertions") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "runs") || !strings.Contains(line, "assertions") {
				continue
			}
			words := strings.Fields(line)
			var runs, failures, errs int
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					switch {
					case strings.HasPrefix(nextWord, "run"):
						runs = n
					case strings.HasPrefix(nextWord, "failure"):
						failures = n
					case strings.HasPrefix(nextWord, "error"):
						errs = n
					}
				}
			}
			if runs > 0 {
				failed = failures + errs
				passed = runs - failed
				ok = true
				return passed, failed, ok
			}
		}
	}

	// .NET: "Total tests: X, Passed: Y, Failed: Z" (may be one line or separate lines)
	if strings.Contains(lower, "total tests:") && strings.Contains(lower, "passed:") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "total tests:") {
				continue
			}
			// .NET may put all on one line or spread across several.
			// Gather a window of lines around "Total tests:".
			window := line
			var windowSb5710 strings.Builder
			for j := i + 1; j < min(i+5, len(lines)); j++ {
				windowSb5710.WriteString(" " + strings.ToLower(strings.TrimSpace(lines[j])))
			}
			window += windowSb5710.String()
			_, _ = fmt.Sscanf(extractAfter(window, "passed:"), "%d", &passed)
			_, _ = fmt.Sscanf(extractAfter(window, "failed:"), "%d", &failed)
			if passed+failed > 0 {
				ok = true
				return passed, failed, ok
			}
		}
	}

	// CTest: "XX% tests passed, Y tests failed out of Z"
	if strings.Contains(lower, "tests passed") && strings.Contains(lower, "tests failed") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "tests passed") || !strings.Contains(line, "tests failed") {
				continue
			}
			// Parse "Y tests failed out of Z"
			words := strings.Fields(line)
			for j := 0; j+3 < len(words); j++ {
				if isNumeric(words[j]) && words[j+1] == "tests" && words[j+2] == "failed" {
					var f int
					_, _ = fmt.Sscanf(words[j], "%d", &f)
					failed = f
					// Look for "out of Z" after "failed"
					for k := j + 3; k+2 < len(words); k++ {
						if words[k] == "out" && words[k+1] == "of" && isNumeric(words[k+2]) {
							var total int
							_, _ = fmt.Sscanf(words[k+2], "%d", &total)
							passed = total - failed
							ok = true
							return passed, failed, ok
						}
					}
				}
			}
		}
	}

	// Haskell Tasty / HUnit: "N out of M tests failed (T seconds)"
	// Success: "All N tests passed (T seconds)" — handled by the Zig/generic section below.
	// Failure format: "2 out of 5 tests failed (0.01s)"
	// Must not conflict with Bazel ("Executed N out of N tests:") or CTest.
	if strings.Contains(lower, "out of") && strings.Contains(lower, "tests failed") &&
		!strings.Contains(lower, "executed") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "out of") || !strings.Contains(line, "tests failed") {
				continue
			}
			// Parse "N out of M tests failed"
			words := strings.Fields(line)
			for j := 0; j+4 < len(words); j++ {
				if isNumeric(words[j]) && words[j+1] == "out" && words[j+2] == "of" && isNumeric(words[j+3]) {
					var f, total int
					_, _ = fmt.Sscanf(words[j], "%d", &f)
					_, _ = fmt.Sscanf(words[j+3], "%d", &total)
					if total > 0 && f <= total {
						failed = f
						passed = total - f
						ok = true
						return passed, failed, ok
					}
				}
			}
		}
	}

	// Lua busted: "X successes / Y failures / Z errors / W pending : T seconds"
	// Note: busted uses "failure" (singular) or "failures" (plural).
	if strings.Contains(lower, "successes") && strings.Contains(lower, "failure") &&
		!strings.Contains(lower, "test result:") && !strings.Contains(lower, "test case") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "successes") || !strings.Contains(line, "failure") {
				continue
			}
			words := strings.Fields(line)
			var successes, failures, errs int
			foundBusted := false
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:/")
					switch {
					case strings.HasPrefix(nextWord, "success"):
						successes = n
						foundBusted = true
					case strings.HasPrefix(nextWord, "failure"):
						failures = n
					case strings.HasPrefix(nextWord, "error"):
						errs = n
					}
				}
			}
			if foundBusted {
				passed = successes
				failed = failures + errs
				ok = true
				return passed, failed, ok
			}
		}
	}

	// Gradle: "X tests completed, Y failed" or "X tests, Y failures"
	if strings.Contains(lower, "tests completed") || (strings.Contains(lower, "tests") && strings.Contains(lower, "gradle")) {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "test") {
				continue
			}
			words := strings.Fields(line)
			var total, failures int
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if strings.HasPrefix(nextWord, "test") && strings.Contains(nextWord, "completed") || (j+2 < len(words) && strings.TrimRight(words[j+2], ",.;:") == "completed") {
						total = n
					} else if nextWord == "failed" || strings.HasPrefix(nextWord, "failure") {
						failures = n
					}
				}
			}
			if total > 0 {
				failed = failures
				passed = total - failed
				ok = true
				return passed, failed, ok
			}
		}
	}

	// SBT (Scala): "[info] Tests: succeeded X, failed Y, canceled Z, ignored W, pending P"
	// Note: SBT uses "keyword N" format (e.g., "succeeded 8,"), not "N keyword".
	if strings.Contains(lower, "succeeded") && strings.Contains(lower, "failed") &&
		(strings.Contains(output, "[info]") || strings.Contains(lower, "sbt")) {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "succeeded") || !strings.Contains(line, "failed") {
				continue
			}
			words := strings.Fields(line)
			var s, f int
			foundSBT := false
			for j := 0; j+1 < len(words); j++ {
				keyword := strings.TrimRight(words[j], ",.;:")
				numStr := strings.TrimRight(words[j+1], ",.;:")
				if isNumeric(numStr) {
					var n int
					_, _ = fmt.Sscanf(numStr, "%d", &n)
					switch keyword {
					case "succeeded":
						s = n
						foundSBT = true
					case "failed":
						f = n
						foundSBT = true
					}
				}
			}
			if foundSBT {
				passed = s
				failed = f
				ok = true
				return passed, failed, ok
			}
		}
	}

	// Dart test: "+X -Y: Some tests failed." or "+X: All tests passed!"
	// Format: "+N" = passed, "-M" = failed (M is optional on success)
	if strings.Contains(output, "All tests passed") ||
		(strings.Contains(output, "+") && strings.Contains(lower, "tests")) {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			lineLower := strings.ToLower(line)
			if !strings.Contains(lineLower, "tests passed") && !strings.Contains(lineLower, "tests failed") {
				continue
			}
			// Extract +N and optionally -M from the line.
			var p, f int
			foundDart := false
			words := strings.Fields(line)
			for _, w := range words {
				if strings.HasPrefix(w, "+") && len(w) > 1 {
					numStr := strings.TrimRight(w[1:], ":")
					if isNumeric(numStr) {
						_, _ = fmt.Sscanf(numStr, "%d", &p)
						foundDart = true
					}
				} else if strings.HasPrefix(w, "-") && len(w) > 1 {
					numStr := strings.TrimRight(w[1:], ":")
					if isNumeric(numStr) {
						_, _ = fmt.Sscanf(numStr, "%d", &f)
					}
				}
			}
			if foundDart {
				passed = p
				failed = f
				ok = true
				return passed, failed, ok
			}
		}
	}

	// ExUnit (Elixir): "5 tests, 1 failure" or "3 doctests, 5 tests, 0 failures"
	if strings.Contains(lower, "failure") && !strings.Contains(lower, "examples") &&
		!strings.Contains(lower, "assertions") && !strings.Contains(lower, "tests run:") &&
		!strings.Contains(lower, "test result:") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "test") || !strings.Contains(line, "failure") {
				continue
			}
			words := strings.Fields(line)
			var tests, failures int
			foundTests := false
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if nextWord == "test" || nextWord == "tests" {
						tests = n
						foundTests = true
					} else if strings.HasPrefix(nextWord, "failure") {
						failures = n
					}
				}
			}
			if foundTests {
				passed = tests - failures
				failed = failures
				ok = true
				return passed, failed, ok
			}
		}
	}

	// XCTest (Swift): "Executed 5 tests, with 2 failures (0 unexpected) in 0.003 seconds"
	if strings.Contains(lower, "executed") && strings.Contains(lower, "test") &&
		strings.Contains(lower, "failure") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.ToLower(strings.TrimSpace(lines[i]))
			if !strings.Contains(line, "executed") || !strings.Contains(line, "test") {
				continue
			}
			words := strings.Fields(line)
			var tests, failures int
			foundTests := false
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if nextWord == "test" || nextWord == "tests" {
						tests = n
						foundTests = true
					} else if strings.HasPrefix(nextWord, "failure") {
						failures = n
					}
				}
			}
			if foundTests {
				passed = tests - failures
				failed = failures
				ok = true
				return passed, failed, ok
			}
		}
	}

	// Zig test: "All N tests passed." (success-only format).
	// Zig failure format "N passed; N skipped; N failed." is caught by pytest above.
	if strings.Contains(lower, "all") && strings.Contains(lower, "tests passed") &&
		!strings.Contains(lower, "assertion") && !strings.Contains(lower, "test case") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			lineLower := strings.ToLower(strings.TrimSpace(lines[i]))
			if strings.HasPrefix(lineLower, "all ") && strings.Contains(lineLower, "tests passed") {
				words := strings.Fields(lineLower)
				if len(words) >= 3 && isNumeric(words[1]) {
					var n int
					_, _ = fmt.Sscanf(words[1], "%d", &n)
					passed = n
					ok = true
					return passed, failed, ok
				}
			}
		}
	}

	// R testthat: "[ FAIL 1 | WARN 0 | SKIP 0 | PASS 2 ]"
	if strings.Contains(output, "[ FAIL") || strings.Contains(output, "[ PASS") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			line := strings.TrimSpace(lines[i])
			if !strings.HasPrefix(line, "[ FAIL") && !strings.HasPrefix(line, "[ PASS") {
				continue
			}
			if !strings.Contains(line, "|") || !strings.Contains(line, "]") {
				continue
			}
			// Parse "FAIL N" and "PASS N" from the pipe-separated format.
			segments := strings.Split(line, "|")
			var p, f int
			foundR := false
			for _, seg := range segments {
				seg = strings.TrimSpace(seg)
				seg = strings.Trim(seg, "[] ")
				words := strings.Fields(seg)
				if len(words) >= 2 && isNumeric(words[1]) {
					var n int
					_, _ = fmt.Sscanf(words[1], "%d", &n)
					switch words[0] {
					case "PASS":
						p = n
						foundR = true
					case "FAIL":
						f = n
						foundR = true
					}
				}
			}
			if foundR {
				passed = p
				failed = f
				ok = true
				return passed, failed, ok
			}
		}
	}

	// Erlang Common Test: "All N test cases passed." or "X of Y test cases failed"
	// Must run before generic section since CT uses "test cases" not "tests".
	if strings.Contains(lower, "test case") && !strings.Contains(lower, "assertion") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			lineLower := strings.ToLower(strings.TrimSpace(lines[i]))
			// "All N test cases passed."
			if strings.HasPrefix(lineLower, "all ") && strings.Contains(lineLower, "test case") &&
				strings.Contains(lineLower, "passed") {
				words := strings.Fields(lineLower)
				if len(words) >= 2 && isNumeric(words[1]) {
					var n int
					_, _ = fmt.Sscanf(words[1], "%d", &n)
					if n > 0 {
						passed = n
						ok = true
						return passed, failed, ok
					}
				}
			}
			// "X of Y test cases failed"
			if strings.Contains(lineLower, "of") && strings.Contains(lineLower, "test case") &&
				strings.Contains(lineLower, "failed") {
				words := strings.Fields(lineLower)
				for j := 0; j+3 < len(words); j++ {
					if isNumeric(words[j]) && words[j+1] == "of" && isNumeric(words[j+2]) {
						var f, total int
						_, _ = fmt.Sscanf(words[j], "%d", &f)
						_, _ = fmt.Sscanf(words[j+2], "%d", &total)
						if total > 0 && f <= total {
							failed = f
							passed = total - f
							ok = true
							return passed, failed, ok
						}
					}
				}
			}
		}
	}

	// Generic: "N tests, M failures" (Gleam, EUnit, common format).
	if strings.Contains(lower, " tests") && strings.Contains(lower, " failure") {
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			lineLower := strings.ToLower(strings.TrimSpace(lines[i]))
			words := strings.Fields(lineLower)
			var total, failures int
			foundTests := false
			for j := 0; j+1 < len(words); j++ {
				if isNumeric(words[j]) {
					var n int
					_, _ = fmt.Sscanf(words[j], "%d", &n)
					nextWord := strings.TrimRight(words[j+1], ",.;:")
					if nextWord == "tests" || nextWord == "test" {
						total = n
						foundTests = true
					} else if strings.HasPrefix(nextWord, "failure") {
						failures = n
					}
				}
			}
			if foundTests && total > 0 {
				passed = total - failures
				failed = failures
				ok = true
				return passed, failed, ok
			}
		}
	}

	// Generic: "X/Y tests passed" or "N out of M"
	for i := len(lines) - 1; i >= max(0, len(lines)-15); i-- {
		line := strings.TrimSpace(lines[i])
		lineLower := strings.ToLower(line)
		// "X/Y" pattern
		if strings.Contains(lineLower, "passed") || strings.Contains(lineLower, "tests") {
			var p, t int
			if n, _ := fmt.Sscanf(line, "%d/%d", &p, &t); n == 2 && t > 0 {
				passed = p
				failed = t - p
				ok = true
				return passed, failed, ok
			}
		}
	}

	// TAP (Test Anything Protocol): "ok 1 - desc" / "not ok 2 - desc"
	if strings.Contains(output, "ok ") && (strings.Contains(output, "1..") || strings.Contains(output, "not ok")) {
		if p, f, tapOK := extractTAPCounts(output); tapOK {
			passed = p
			failed = f
			ok = true
			return passed, failed, ok
		}
	}

	// GoogleTest (C++): "[  PASSED  ] N test(s)." and "[  FAILED  ] N test(s), listed below:"
	// Parse the bracketed summary lines that appear at the end of GoogleTest output.
	// Also handles standalone "N FAILED TEST(S)" final line.
	if strings.Contains(output, "[  PASSED  ]") || strings.Contains(output, "[  FAILED  ]") {
		var p, f int
		foundGTest := false
		for i := len(lines) - 1; i >= max(0, len(lines)-10); i-- {
			trimmed := strings.TrimSpace(lines[i])
			words := strings.Fields(trimmed)
			if strings.HasPrefix(trimmed, "[  PASSED  ]") {
				// "[  PASSED  ] N test(s)." — Fields: ["[", "PASSED", "]", "N", "test(s)."]
				if len(words) >= 4 && isNumeric(words[3]) {
					_, _ = fmt.Sscanf(words[3], "%d", &p)
					foundGTest = true
				}
			} else if strings.HasPrefix(trimmed, "[  FAILED  ]") && strings.Contains(trimmed, "listed below") {
				// "[  FAILED  ] N test(s), listed below:" — Fields: ["[", "FAILED", "]", "N", ...]
				if len(words) >= 4 && isNumeric(words[3]) {
					_, _ = fmt.Sscanf(words[3], "%d", &f)
					foundGTest = true
				}
			} else if len(words) >= 3 && isNumeric(words[0]) &&
				words[1] == "FAILED" && (words[2] == "TEST" || words[2] == "TESTS") {
				// Standalone "N FAILED TEST(S)" final line.
				_, _ = fmt.Sscanf(words[0], "%d", &f)
				foundGTest = true
			}
		}
		if foundGTest {
			passed = p
			failed = f
			ok = true
			return passed, failed, ok
		}
	}

	// Count bare PASS/FAIL lines (some test harnesses output "PASS test_name"
	// or "FAIL test_name" one per line). Only match lines where PASS/FAIL is
	// a standalone word at the start — not partial matches like PASSWORD,
	// PASSENGER, FAILURE, FAILOVER, etc.
	var passCount, failCount int
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		// Match "PASS", "PASS test_name", "PASS: ...", etc.
		if upper == "PASS" || strings.HasPrefix(upper, "PASS ") || strings.HasPrefix(upper, "PASS\t") || strings.HasPrefix(upper, "PASS:") {
			passCount++
		} else if upper == "FAIL" || strings.HasPrefix(upper, "FAIL ") || strings.HasPrefix(upper, "FAIL\t") || strings.HasPrefix(upper, "FAIL:") {
			failCount++
		}
	}
	if passCount+failCount >= 3 {
		passed = passCount
		failed = failCount
		ok = true
		return passed, failed, ok
	}

	return 0, 0, false
}

// extractTAPCounts parses TAP (Test Anything Protocol) output and returns
// pass/fail counts. TAP is used by Perl prove, Node tap/tape, pg_prove,
// and many other test tools across languages.
//
// TAP format:
//
//	1..5
//	ok 1 - test description
//	not ok 2 - failing test
//	ok 3 - another test
//
// Also handles TAP summary lines: "# tests 5" / "# pass 3" / "# fail 2".
func extractTAPCounts(output string) (passed, failed int, ok bool) {
	lines := strings.Split(output, "\n")

	// First check for TAP summary comments (Node tap/tape style):
	// "# tests 5", "# pass 3", "# fail 2"
	var summaryTests, summaryPass, summaryFail int
	hasSummary := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "# tests ") {
			_, _ = fmt.Sscanf(lower[len("# tests "):], "%d", &summaryTests)
			hasSummary = true
		} else if strings.HasPrefix(lower, "# pass ") {
			_, _ = fmt.Sscanf(lower[len("# pass "):], "%d", &summaryPass)
			hasSummary = true
		} else if strings.HasPrefix(lower, "# fail ") {
			_, _ = fmt.Sscanf(lower[len("# fail "):], "%d", &summaryFail)
			hasSummary = true
		}
	}
	if hasSummary && summaryTests > 0 {
		return summaryPass, summaryFail, true
	}

	// Count "ok N" and "not ok N" lines.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "not ok ") {
			// Verify it's followed by a number (TAP format).
			rest := strings.TrimSpace(trimmed[7:])
			if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
				failed++
				ok = true
			}
		} else if strings.HasPrefix(trimmed, "ok ") {
			rest := strings.TrimSpace(trimmed[3:])
			if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
				passed++
				ok = true
			}
		}
	}
	return passed, failed, ok
}

// extractAfter returns the substring after the given prefix in s.
func extractAfter(s, prefix string) string {
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	return s[idx+len(prefix):]
}

// compilationErrorSummary extracts key error lines from compiler output.
// Returns empty string if no compilation errors are detected.
func compilationErrorSummary(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	// Only process if it looks like compiler output
	hasCompilerError := false
	errorPatterns := []string{
		": error:", ": error[", "error:", "Error:",
		": fatal error:", "undefined reference",
		"SyntaxError:", "IndentationError:", "TypeError:",
		"NameError:", "ModuleNotFoundError:",
		"cannot find symbol", "not found in scope",
		"error[E", // Rust: error[E0425]: ... (no leading colon)
		"-- [E",   // Scala 3: -- [E007] Type Mismatch Error:
		// Go compilation errors (file.go:line:col: message — no ": error:" prefix).
		"undefined: ",           // Go: undefined identifier
		"imported and not used", // Go: unused import
		"declared and not used", // Go: unused variable
		// GHC (Haskell): error details on continuation lines, match distinctive messages.
		"Not in scope:",              // GHC: variable/function not found
		"Could not deduce",           // GHC: type class constraint failure
		"No instance for",            // GHC: missing type class instance
		"Couldn't match type",        // GHC: type mismatch
		"Couldn't match expected",    // GHC: expected vs actual type
		"Ambiguous type variable",    // GHC: type inference failure
		"Variable not in scope",      // GHC: newer format
		"Not a valid type signature", // GHC: invalid type signature
		"Parse error",                // GHC: syntax error
		// Clojure compilation errors.
		"Syntax error compiling", // Clojure: Syntax error compiling at (file.clj:line:col)
		"CompilerException",      // Clojure: older format
		"Execution error",        // Clojure: runtime error with file reference
		// Erlang compilation errors.
		"head mismatch",       // Erlang: function clause head mismatch
		"syntax error before", // Erlang: syntax error before: 'token'
		"illegal pattern",     // Erlang: illegal pattern in clause
	}
	for _, p := range errorPatterns {
		if strings.Contains(output, p) {
			hasCompilerError = true
			break
		}
	}
	if !hasCompilerError {
		return ""
	}

	// Extract error lines (lines containing ": error" or similar patterns)
	var errorLines []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 5 || len(trimmed) > 200 {
			continue
		}
		isError := false
		for _, p := range errorPatterns {
			if strings.Contains(trimmed, p) {
				isError = true
				break
			}
		}
		if isError && !seen[trimmed] {
			seen[trimmed] = true
			errorLines = append(errorLines, trimmed)
		}
	}

	if len(errorLines) == 0 {
		return ""
	}

	// Show up to 8 error lines
	shown := errorLines
	if len(shown) > 8 {
		shown = shown[:8]
	}

	summary := fmt.Sprintf("[compilation: %d error(s) found", len(errorLines))
	if len(errorLines) > 8 {
		summary += " (showing first 8)"
	}
	summary += ":\n"
	var summarySb6355 strings.Builder
	for _, line := range shown {
		summarySb6355.WriteString("  " + line + "\n")
	}
	summary += summarySb6355.String()
	summary += "]"

	// Cascade hint: when many errors exist, fixing the first one often
	// resolves many others (e.g., missing include → dozens of "undeclared").
	if len(errorLines) > 5 {
		summary += "\n[hint: fix the FIRST error and recompile — later errors often cascade from the first one]"
	}
	return summary
}

// compilationFingerprint extracts a fingerprint from compilation output for
// stale-error detection. Uses the first unique error line as fingerprint.
// When the same fingerprint appears twice in a row, the agent's fix didn't work.
func compilationFingerprint(output string) string {
	errorPatterns := []string{
		": error:", ": error[", ": fatal error:",
		"undefined reference", "cannot find symbol",
		"not found in scope",
		") Error:",     // Nim: "file.nim(42, 5) Error:"
		"): Error:",    // D: "file.d(42): Error:"
		"Fatal Error:", // Fortran gfortran fatal errors
		// Python runtime/compile errors (not caught by `: error:` patterns).
		"SyntaxError:",         // Python: SyntaxError: invalid syntax
		"IndentationError:",    // Python: IndentationError: unexpected indent
		"NameError:",           // Python: NameError: name 'foo' is not defined
		"ModuleNotFoundError:", // Python: ModuleNotFoundError: No module named 'x'
		// Rust: error[E0425]: cannot find value (no leading colon).
		"error[E",
		// Scala 3: "-- [E007] Type Mismatch Error:" (starts with --)
		"-- [E",
		// Go: "file.go:42:5: undefined: foo" — Go uses file:line:col: directly
		// without a ": error:" prefix, so these patterns catch Go-specific errors.
		"undefined: ",           // Go: undefined identifier
		"imported and not used", // Go: unused import
		"declared and not used", // Go: unused variable
		// GHC (Haskell): distinctive error messages on continuation lines.
		"Not in scope:",           // GHC: variable/function not found
		"Could not deduce",        // GHC: type class constraint failure
		"No instance for",         // GHC: missing type class instance
		"Couldn't match type",     // GHC: type mismatch
		"Couldn't match expected", // GHC: expected vs actual type
		"Variable not in scope",   // GHC: newer format
		"Parse error",             // GHC: syntax error
	}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 5 || len(trimmed) > 200 {
			continue
		}
		for _, p := range errorPatterns {
			if strings.Contains(trimmed, p) {
				return trimmed
			}
		}
	}
	return ""
}

// isBuildCommand detects commands that typically need longer timeouts.
// isLongRunningCommand returns true for commands that typically need more than
// short default timeouts: benchmarks, model training, data processing, etc.
func isLongRunningCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	longPatterns := []string{
		"benchmark", "bench.",
		"python3 train", "python train",
		"python3 benchmark", "python benchmark",
		"pytest",                  // any pytest invocation (bare, with args, etc.)
		"make test", "make check", // test targets through make
		"ctest", // CMake test runner
		"go test -bench", "go test -count", "go test -run", "go test ./...",
		"train.py", "training.py",
		"fasttext ", "qemu-system",
		"java -jar", "java -cp", // JVM programs
		"mvn ", "gradle ", // Build tools
		"npm run ", "npx ", // npm scripts
		"yarn ", "pnpm run ", // Package manager scripts
		"python3 -m ", "python -m ", // Module execution (e.g., python -m pytest)
		"docker run",              // Container execution
		"timeout ",                // Already has own timeout, don't cut short
		"lake build",              // Lean 4 proof checking
		"dune build", "dune test", // OCaml builds
		"stack build", "cabal build", // Haskell builds
		"cargo test",                    // Rust tests
		"python3 /app/", "python /app/", // app scripts
		"python3 solve", "python solve", // solver scripts
		"python3 process", "python process", // data processing
		"python3 run", "python run", // generic runner scripts
		"python3 main", "python main", // main entry points
		"python3 solution", "python solution", // solution scripts
		"node main", "node solution", // Node.js entry points
		"bash /app/", "sh /app/", // shell scripts in /app/
		"bash /tests/", "sh /tests/", // test scripts
		"julia ",                    // Julia JIT compilation is slow on first run
		"coqc ",                     // Coq proof checking
		"opam ",                     // OCaml package manager
		"stack setup", "stack exec", // Haskell GHC download / execution
		"rscript ", "r -e ", // R scripts
		"dotnet test", "dotnet run", // .NET execution
		"sbt test", "sbt run", // Scala/SBT
		"dart test", "dart run", // Dart
		"flutter test", "flutter run", // Flutter
		"mix test",                // Elixir tests
		"bundle exec",             // Ruby with bundler
		"cabal test", "cabal run", // Haskell
		"busted",                // Lua tests
		"fpc ",                  // Free Pascal compilation
		"crystal spec",          // Crystal tests
		"kotlinc ",              // Kotlin compilation
		"crystal build",         // Crystal compilation
		"zig test",              // Zig tests
		"nim c ", "nim compile", // Nim compilation
		"v test",         // V language tests
		"gleam test",     // Gleam tests
		"bun test",       // Bun tests
		"bun run ",       // Bun scripts
		"poetry run ",    // Poetry scripts
		"pdm run ",       // PDM scripts
		"uv run ",        // uv scripts
		"nox ", "nox -s", // Nox test sessions
		"tox ", "tox -e", // Tox test environments
		"hatch run ",            // Hatch scripts
		"deno test",             // Deno tests
		"deno run ",             // Deno scripts
		"swift test",            // Swift package tests
		"gleam run",             // Gleam execution
		"mix phx.",              // Phoenix (Elixir) tasks
		"ghci ",                 // GHCi interactive
		"scala ",                // Scala execution
		"lein test", "lein run", // Clojure Leiningen
		"clj -M", "clj -X", // Clojure deps.edn
		"rebar3 ct", "rebar3 eunit", // Erlang tests
		"conda install", "mamba install", // Conda/Mamba (downloads + solves)
		"uv pip install",                // uv pip install (network + compile)
		"swift build",                   // Swift compilation
		"phpunit", "vendor/bin/phpunit", // PHP tests
		"nimble test",           // Nim tests
		"dub test", "dub build", // D language
		"meson test", "meson compile", // Meson build system
		// Terraform/OpenTofu (provider downloads + API calls).
		"terraform plan", "terraform apply", "terraform init",
		"tofu plan", "tofu apply", "tofu init",
		// Nix (derivation builds can be very slow).
		"nix build", "nix develop", "nix flake check", "nix-build",
		// Elm (full project compilation).
		"elm make", "elm-test",
		// Solidity/Foundry (contract compilation + tests).
		"forge build", "forge test",
		"hardhat compile", "hardhat test",
		"truffle compile", "truffle test",
		// BATS (Bash testing).
		"bats ",
	}
	for _, p := range longPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// timeoutContextHint provides a context-aware hint when a command times out.
// Server/daemon commands need background execution, not optimization. Build
// commands need more time or parallelization. This saves a turn of confusion.
func timeoutContextHint(cmd string) string {
	lower := strings.ToLower(strings.TrimSpace(cmd))

	// Server/daemon commands: suggest running in background.
	serverPatterns := []string{
		"flask run", "django", "runserver", "uvicorn", "gunicorn",
		"node server", "npm start", "npm run dev", "npm run serve",
		"python3 -m http.server", "python -m http.server",
		"rails server", "rails s ", "puma ",
		"nginx", "apache", "httpd",
		"redis-server", "mongod", "postgres",
		"java -jar", "spring-boot:run",
		"cargo run", "go run",
		"deno run --allow-net", "deno serve",
		"bun run dev", "bun run serve",
		"mix phx.server",           // Phoenix (Elixir)
		"iex -s mix phx.server",    // Phoenix interactive
		"caddy run", "caddy start", // Caddy web server
		"hypercorn", "daphne", // ASGI servers
		"php -s ", "php artisan serve", // PHP built-in server
		// Modern JS/TS dev servers — extremely common to accidentally run blocking.
		"vite", "next dev", "nuxt dev",
		"ng serve",                            // Angular CLI
		"gatsby develop",                      // Gatsby
		"astro dev",                           // Astro
		"remix dev",                           // Remix
		"turbo dev",                           // Turborepo
		"webpack serve", "webpack-dev-server", // Webpack
		// Python application servers and workers.
		"streamlit run",              // Streamlit
		"celery worker", "celery -a", // Celery task worker
		"jupyter notebook", "jupyter lab", // Jupyter (runs indefinitely)
		// Background services.
		"consul agent", "vault server", "nomad agent", // HashiCorp
	}
	for _, p := range serverPatterns {
		if strings.Contains(lower, p) {
			return "[hint: this looks like a server/daemon command that runs indefinitely. " +
				"Run it in the background: nohup <command> > /tmp/server.log 2>&1 & " +
				"Then verify with: curl localhost:<port> or ss -tlnp]"
		}
	}

	// Interactive/blocking commands that should not be run directly.
	blockingPatterns := []string{
		"tail -f", "watch ",
		"top", "htop", "btop", "nmon", // System monitors
		"less ", "more ", // Pagers
		"journalctl -f", "journalctl --follow", // Log following
		"docker logs -f", "docker attach", // Docker live streams
		"kubectl logs -f", // Kubernetes log following
	}
	for _, p := range blockingPatterns {
		if strings.Contains(lower, p) {
			return "[hint: this is a blocking/interactive command. Use a non-blocking alternative " +
				"(e.g., tail -n 50 instead of tail -f, cat instead of less, " +
				"journalctl -n 50 instead of journalctl -f)]"
		}
	}

	return ""
}

// testTimeoutOptimizationHint provides language-specific optimization advice
// when a test or build command times out. The generic timeout message says
// "optimize YOUR code" but language-specific hints are more actionable.
func testTimeoutOptimizationHint(cmd string) string {
	lower := strings.ToLower(strings.TrimSpace(cmd))

	// Detect test commands and provide language-specific optimization hints.
	switch {
	case strings.Contains(lower, "pytest") || strings.Contains(lower, "python3 -m pytest") ||
		strings.Contains(lower, "python3 test") || strings.Contains(lower, "python test"):
		return "[optimization hints: (1) use numpy/vectorized ops instead of Python loops, " +
			"(2) use dict/set for O(1) lookups instead of list scans, " +
			"(3) use generators for large data, (4) profile with: python3 -m cProfile -s cumulative your_script.py]"
	case strings.Contains(lower, "go test") || strings.Contains(lower, "go run"):
		return "[optimization hints: (1) use map for lookups, (2) pre-allocate slices with make([]T, 0, n), " +
			"(3) avoid string concatenation in loops (use strings.Builder), (4) profile with: go test -cpuprofile=cpu.prof -run TestName]"
	case strings.Contains(lower, "cargo test") || strings.Contains(lower, "cargo run"):
		return "[optimization hints: (1) use HashMap for lookups, (2) avoid unnecessary cloning, " +
			"(3) use iterators instead of collect+loop, (4) compile with --release for benchmarks]"
	case strings.Contains(lower, "gcc") || strings.Contains(lower, "g++") || strings.Contains(lower, "clang") || strings.Contains(lower, "make"):
		return "[optimization hints: (1) add -O2 flag for optimization, " +
			"(2) use efficient data structures, (3) minimize memory allocations in hot loops]"
	case strings.Contains(lower, "npm test") || strings.Contains(lower, "jest") || strings.Contains(lower, "mocha") || strings.Contains(lower, "node "):
		return "[optimization hints: (1) use Map/Set for lookups instead of array.find/includes, " +
			"(2) avoid unnecessary object spread/creation in loops, (3) use streams for large data]"
	case strings.Contains(lower, "mvn test") || strings.Contains(lower, "gradle test") ||
		strings.Contains(lower, "gradlew test") || strings.Contains(lower, "java ") || strings.Contains(lower, "javac "):
		return "[optimization hints: (1) use HashMap/HashSet for O(1) lookups, (2) use StringBuilder instead of string concatenation in loops, " +
			"(3) prefer primitive arrays over ArrayList for numeric data, (4) use parallelStream() for independent computations]"
	case strings.Contains(lower, "dotnet test") || strings.Contains(lower, "dotnet run"):
		return "[optimization hints: (1) use Dictionary/HashSet for O(1) lookups, (2) use StringBuilder for string concatenation in loops, " +
			"(3) use Span<T> to avoid allocations, (4) prefer LINQ with early termination (.Any(), .First()) over full iteration]"
	case strings.Contains(lower, "rspec") || strings.Contains(lower, "rake test") ||
		strings.Contains(lower, "ruby ") || strings.Contains(lower, "bundle exec"):
		return "[optimization hints: (1) use Hash/Set for O(1) lookups instead of Array#include?, " +
			"(2) use each_with_object instead of map+flatten, (3) avoid excessive object allocation in loops, " +
			"(4) use lazy enumerators for large collections]"
	case strings.Contains(lower, "mix test"):
		return "[optimization hints: (1) use MapSet/Map for lookups instead of Enum.member?, " +
			"(2) use Stream for lazy evaluation of large collections, (3) avoid repeated list traversals, " +
			"(4) use ETS tables for shared in-memory lookups]"
	case strings.Contains(lower, "sbt test") || strings.Contains(lower, "sbt run"):
		return "[optimization hints: (1) use HashMap for O(1) lookups, (2) use view for lazy collection operations, " +
			"(3) avoid implicit conversions in hot paths, (4) use mutable collections in performance-critical sections]"
	case strings.Contains(lower, "stack test") || strings.Contains(lower, "cabal test"):
		return "[optimization hints: (1) use Data.Map or Data.HashMap for lookups, (2) use strict fields and BangPatterns to avoid thunk buildup, " +
			"(3) use Data.ByteString instead of String for large text, (4) use Data.Vector instead of lists for random access]"
	case strings.Contains(lower, "dart test") || strings.Contains(lower, "flutter test"):
		return "[optimization hints: (1) use Map/Set for O(1) lookups instead of List.contains, " +
			"(2) use StringBuffer for string concatenation in loops, (3) avoid unnecessary widget rebuilds (use const constructors), " +
			"(4) pre-compute expensive values outside loops]"
	case strings.Contains(lower, "phpunit") || strings.Contains(lower, "php "):
		return "[optimization hints: (1) use array keys for O(1) lookups (isset > in_array), " +
			"(2) avoid array_merge in loops (use array_push or []), (3) use generators for large datasets, " +
			"(4) pre-compute values instead of recalculating in loops]"
	case strings.Contains(lower, "zig test") || strings.Contains(lower, "zig build"):
		return "[optimization hints: (1) use std.HashMap for O(1) lookups, (2) use SIMD builtins for numeric operations, " +
			"(3) avoid unnecessary allocations — prefer stack and comptime, (4) use @prefetch for memory-bound loops]"
	case strings.Contains(lower, "nim c") || strings.Contains(lower, "nimble test"):
		return "[optimization hints: (1) compile with -d:release for optimizations, (2) use Table for O(1) lookups, " +
			"(3) avoid seq copies in loops — use openArray, (4) use --gc:arc for deterministic memory management]"
	case strings.Contains(lower, "swift test") || strings.Contains(lower, "swift build"):
		return "[optimization hints: (1) use Dictionary/Set for O(1) lookups, (2) use value types (struct) over reference types (class) for small data, " +
			"(3) use ContiguousArray instead of Array for non-bridged types, (4) avoid ARC overhead — use unowned/weak refs carefully]"
	case strings.Contains(lower, "gleam test") || strings.Contains(lower, "gleam run"):
		return "[optimization hints: (1) use dict for O(1) lookups instead of list.contains, " +
			"(2) use iterators/streams for lazy evaluation, (3) avoid repeated list traversals, " +
			"(4) if targeting Erlang, leverage OTP concurrency for parallelism]"
	case strings.Contains(lower, "deno test") || strings.Contains(lower, "deno run"):
		return "[optimization hints: (1) use Map/Set for O(1) lookups, (2) use TypedArrays for numeric data, " +
			"(3) avoid unnecessary object spread/creation in loops, (4) use Web Streams for large data processing]"
	case strings.Contains(lower, "lein test") || strings.Contains(lower, "clj -M:test") || strings.Contains(lower, "clj -X:test"):
		return "[optimization hints: (1) use hash-map/hash-set for O(1) lookups, (2) use transients for bulk mutations, " +
			"(3) use reducers/transducers instead of chained seq operations, (4) use type hints to avoid reflection]"
	case strings.Contains(lower, "rebar3 ct") || strings.Contains(lower, "rebar3 eunit"):
		return "[optimization hints: (1) use maps for O(1) lookups instead of lists:keyfind, " +
			"(2) use binary matching instead of string operations, (3) avoid list comprehensions over large datasets — use ets tables, " +
			"(4) use binary:compile_pattern for repeated pattern matching]"
	case strings.Contains(lower, "crystal spec") || strings.Contains(lower, "crystal build"):
		return "[optimization hints: (1) use Hash/Set for O(1) lookups instead of Array#includes?, " +
			"(2) use IO::Memory and String.build for string construction, (3) avoid unnecessary object allocations in hot loops, " +
			"(4) compile with --release for optimized builds]"
	case strings.Contains(lower, "busted") || strings.Contains(lower, "lua ") || strings.Contains(lower, "luajit "):
		return "[optimization hints: (1) use table keys for O(1) lookups instead of linear search, " +
			"(2) localize frequently used functions (local insert = table.insert), (3) pre-size tables with table.new if using LuaJIT, " +
			"(4) avoid creating closures or tables inside tight loops]"
	case strings.Contains(lower, "julia ") || strings.Contains(lower, "julia -e"):
		return "[optimization hints: (1) avoid global variables in hot paths (pass as function args), " +
			"(2) use type annotations for function arguments, (3) pre-allocate arrays with similar() or zeros(), " +
			"(4) use @views for array slices to avoid copies, (5) Julia JIT compiles on first call — subsequent runs are faster]"
	case strings.Contains(lower, "rscript ") || strings.Contains(lower, "r -e ") || strings.Contains(lower, "r --vanilla"):
		return "[optimization hints: (1) vectorize operations instead of using for loops, " +
			"(2) use data.table instead of data.frame for large datasets, (3) pre-allocate vectors instead of growing in loops, " +
			"(4) use Rcpp for computation-heavy inner loops]"
	case strings.Contains(lower, "fpc ") || strings.Contains(lower, "fpc -"):
		return "[optimization hints: (1) compile with -O2 for optimization, (2) use arrays instead of linked lists for sequential data, " +
			"(3) use SetLength to pre-allocate dynamic arrays, (4) avoid string concatenation in loops — use TStringBuilder]"
	case strings.Contains(lower, "v test") || strings.Contains(lower, "v run"):
		return "[optimization hints: (1) use maps for O(1) lookups instead of array linear search, " +
			"(2) use -prod flag for optimized compilation, (3) avoid unnecessary allocations in tight loops, " +
			"(4) use @[cap:n]T{} to pre-allocate arrays]"
	case strings.Contains(lower, "forge test") || strings.Contains(lower, "forge build") ||
		strings.Contains(lower, "hardhat test") || strings.Contains(lower, "hardhat compile"):
		return "[optimization hints: (1) minimize storage reads/writes (SLOAD/SSTORE are expensive), " +
			"(2) use calldata instead of memory for read-only function params, (3) pack storage variables (uint128+uint128 in one slot), " +
			"(4) use --match-test to run specific tests, (5) use unchecked{} for math that can't overflow]"
	// Catch-all for generic python/python3 invocations (scripts, not test runners).
	case strings.Contains(lower, "python3 ") || strings.Contains(lower, "python "):
		return "[optimization hints: (1) use numpy/vectorized ops instead of Python loops for numeric work, " +
			"(2) use dict/set for O(1) lookups instead of list scans, " +
			"(3) use generators for large data, (4) profile with: python3 -m cProfile -s cumulative your_script.py]"
	}

	return ""
}

// isPipCommand returns true if the command involves pip installing packages.
// Used to auto-set PIP_BREAK_SYSTEM_PACKAGES=1 for container environments.
func isPipCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "pip install") ||
		strings.Contains(lower, "pip3 install") ||
		strings.Contains(lower, "python -m pip") ||
		strings.Contains(lower, "python3 -m pip")
}

func isBuildCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	buildPatterns := []string{
		"make", "cmake", "cargo build", "cargo install",
		"go build", "go install",
		"gcc ", "g++ ", "clang ", "cc ",
		"javac ", "mvn ", "gradle ",
		"npm install", "npm ci", "yarn install", "pnpm install",
		"pip install", "pip3 install", "python3 -m pip install", "python -m pip install",
		"uv pip install", "uv sync", "uv add", // uv (modern Python)
		"poetry install", "poetry add", // Poetry
		"conda install", "mamba install", // Conda/Mamba
		"apt-get install", "apt install", "apk add", "yum install", "dnf install",
		"docker build",
		"lake build",  // Lean 4
		"dune build",  // OCaml
		"stack build", // Haskell
		"cabal build", // Haskell
		"zig build",   // Zig
		"mix compile", // Elixir
		"./configure",
		"rustup",
		"gem install", "bundle install",
		"composer install",            // PHP
		"sbt compile", "sbt assembly", // Scala
		"dart compile",                   // Dart
		"flutter build",                  // Flutter
		"pub get",                        // Dart package manager
		"dotnet restore", "dotnet build", // .NET
		"gfortran ",      // Fortran
		"nasm ", "yasm ", // Assembly
		"fpc ",                  // Free Pascal
		"coqc ", "coq_makefile", // Coq proof checker
		"nim c ", "nim compile", // Nim
		"opam install",  // OCaml
		"stack setup",   // Haskell GHC download
		"swiftc ",       // Swift
		"ldc2 ", "gdc ", // D language
		"julia -e \"using Pkg", // Julia package operations
		"kotlinc ",             // Kotlin
		"crystal build",        // Crystal
		"dmd ",                 // D language (reference compiler)
		"v build", "v run",     // V language
		"bun install",                       // Bun package manager
		"bun build",                         // Bun bundler
		"bun add",                           // Bun add dependency
		"gleam build",                       // Gleam
		"deno install",                      // Deno dependencies
		"deno cache",                        // Deno dependency caching
		"mix deps.get",                      // Elixir dependencies
		"stack install",                     // Haskell
		"cargo check",                       // Rust quick check
		"elixirc ",                          // Elixir compilation
		"scalac ",                           // Scala compilation
		"ghc ",                              // GHC direct compilation
		"rebar3 compile", "rebar3 get-deps", // Erlang
		"nimble install", "nimble build", // Nim packages
		"dub build",                    // D language
		"meson setup", "meson compile", // Meson build system
		"pub add",                                    // Dart add dependency
		"just build", "just compile", "just install", // Just task runner
	}
	for _, p := range buildPatterns {
		if strings.HasPrefix(lower, p) || strings.Contains(lower, " && "+p) || strings.Contains(lower, "; "+p) {
			return true
		}
	}
	return false
}

// isRiskyProcessKillCommand blocks broad process-kill patterns that can
// terminate unrelated processes in shared benchmark containers.
// Prefer PID-file-based lifecycle management for services.
func isRiskyProcessKillCommand(cmd string) bool {
	lower := strings.ToLower(cmd)

	if strings.Contains(lower, "pkill -f") || strings.Contains(lower, "pkill --full") {
		return true
	}
	if strings.Contains(lower, "killall ") {
		return true
	}
	return false
}

// isDestructiveTestCommand checks whether a bash command would destructively
// modify files in /tests/ (the verifier test directory). This blocks:
//   - Redirects to /tests/ files (>, >>)
//   - rm, mv, cp targeting /tests/
//   - sed -i (in-place edit) on /tests/ files
//   - chmod, chown on /tests/ files
//   - truncate on /tests/ files
//
// It does NOT block running tests (bash /tests/test.sh, python3 /tests/test.py)
// or reading tests (cat /tests/test.sh, head /tests/test.py).
//
// Path matching is scoped to the root verifier directory (/tests/...), not
// similarly named paths like /app/tests or /tmp/tests-data.
func isDestructiveTestCommand(cmd string) bool {
	// Analyze the full command as one unit. This is intentionally conservative:
	// it prevents bypasses where /tests appears in one segment and the mutating
	// operation appears in another (pipelines, variable indirection, etc.).
	return isDestructiveTestCommandSegment(cmd)
}

func isDestructiveTestCommandSegment(cmd string) bool {
	// Quick check: if the command doesn't reference root /tests, skip.
	if findVerifierTestsPath(cmd, 0) < 0 {
		return false
	}

	// Destructive patterns that target /tests/ files.
	lower := strings.ToLower(cmd)

	// Creating or linking paths under /tests mutates verifier state.
	if strings.Contains(lower, "mkdir ") || strings.Contains(lower, "ln ") || strings.Contains(lower, "touch ") {
		return true
	}

	// Redirects to /tests/ files.
	if strings.Contains(cmd, "> /tests") || strings.Contains(cmd, ">/tests") ||
		strings.Contains(cmd, ">> /tests") || strings.Contains(cmd, ">>/tests") {
		return true
	}

	// tee writing to /tests/ files.
	if strings.Contains(lower, "tee ") {
		// Check if /tests comes after tee (output target).
		teeIdx := strings.Index(lower, "tee ")
		testsIdx := findVerifierTestsPath(cmd, teeIdx+len("tee "))
		if testsIdx > teeIdx {
			return true
		}
	}

	// rm targeting /tests/ files.
	if strings.Contains(lower, "rm ") || strings.Contains(lower, "rm -") {
		return true
	}

	// sed -i (in-place edit) on /tests/ files.
	if strings.Contains(lower, "sed ") && strings.Contains(lower, "-i") {
		return true
	}

	// chmod, chown on /tests/ files (prevents making tests non-executable, etc.).
	if strings.Contains(lower, "chmod ") || strings.Contains(lower, "chown ") {
		return true
	}

	// truncate on /tests/ files.
	if strings.Contains(lower, "truncate ") {
		return true
	}

	// perl -i / perl -pi (in-place edit) on /tests/ files.
	if (strings.Contains(lower, "perl ") || strings.Contains(lower, "perl\t")) &&
		(strings.Contains(lower, " -i") || strings.Contains(lower, " -pi")) {
		return true
	}

	// dd writing to /tests/ files (dd of=/tests/...).
	if strings.Contains(lower, "dd ") && strings.Contains(cmd, "of=/tests") {
		return true
	}

	// patch applying to /tests/ files.
	if strings.Contains(lower, "patch ") {
		return true
	}

	// install (coreutils) targeting /tests/ files.
	if strings.Contains(lower, "install ") &&
		!strings.Contains(lower, "pip install") && !strings.Contains(lower, "npm install") &&
		!strings.Contains(lower, "apt install") && !strings.Contains(lower, "apt-get install") {
		return true
	}

	// mv/cp with /tests/ as destination (overwriting test files).
	// Only block if /tests/ is in the latter part (destination).
	if strings.Contains(lower, "cp ") || strings.Contains(lower, "mv ") {
		// Split on /tests and check if it appears after the first argument.
		testsIdx := findVerifierTestsPath(cmd, 0)
		if testsIdx >= 0 {
			before := cmd[:testsIdx]
			// If /tests/ follows the source argument (i.e., it's the destination), block.
			if strings.Contains(before, "cp ") || strings.Contains(before, "mv ") {
				// But NOT if /tests/ is the source (first arg after cp/mv).
				lastSpace := strings.LastIndex(strings.TrimSpace(before), " ")
				if lastSpace > 0 {
					return true
				}
			}
		}
	}

	return false
}

// findVerifierTestsPath returns the index of a root /tests path occurrence in s,
// starting from start, or -1 if not found.
//
// Valid matches must be rooted at /tests and path-bounded:
// - accepted: "/tests", "/tests/foo", "cmd '/tests/foo'", "of=/tests/file"
// - rejected: "/app/tests/foo", "/tests_backup", "/tmp/tests-data".
func findVerifierTestsPath(s string, start int) int {
	const needle = "/tests"
	if start < 0 {
		start = 0
	}
	for start <= len(s)-len(needle) {
		rel := strings.Index(s[start:], needle)
		if rel < 0 {
			return -1
		}
		idx := start + rel
		if isVerifierTestsPathAt(s, idx) {
			return idx
		}
		start = idx + len(needle)
	}
	return -1
}

func isVerifierTestsPathAt(s string, idx int) bool {
	const needle = "/tests"
	if idx < 0 || idx+len(needle) > len(s) || s[idx:idx+len(needle)] != needle {
		return false
	}
	prevOK := idx == 0 || isShellPathDelimiter(s[idx-1])
	nextIdx := idx + len(needle)
	nextOK := nextIdx == len(s) || s[nextIdx] == '/' || isShellPathDelimiter(s[nextIdx])
	return prevOK && nextOK
}

func isShellPathDelimiter(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', '=', ':', ',', ';', '|', '&',
		'(', ')', '[', ']', '{', '}', '<', '>', '\\':
		return true
	default:
		return false
	}
}

// envVarHint detects missing environment variable errors.
// Common in eval containers and local dev: commands fail because JAVA_HOME,
// GOPATH, ANDROID_HOME, or custom env vars aren't set. The hint saves 1-2
// turns of the agent trying to figure out what's missing.
func envVarHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// Bash "unbound variable" — set -u or set -e with unset var.
	if strings.Contains(output, ": unbound variable") {
		return "[hint: unbound variable — a required environment variable is not set. " +
			"Check the script for ${VAR} references and export the variable: export VAR=value]"
	}

	// Common env var not-set patterns from various languages and tools.
	envPatterns := []string{
		"java_home", "gopath", "goroot", "android_home", "android_sdk",
		"ndk_home", "flutter_home", "dart_home",
		"node_path", "npm_config_prefix",
		"cargo_home", "rustup_home",
		"virtual_env", "conda_prefix",
		"database_url", "redis_url", "mongodb_uri",
	}
	for _, env := range envPatterns {
		if strings.Contains(lower, env) && (strings.Contains(lower, "not set") ||
			strings.Contains(lower, "not defined") ||
			strings.Contains(lower, "is not configured") ||
			strings.Contains(lower, "must be set") ||
			strings.Contains(lower, "environment variable")) {
			envUpper := strings.ToUpper(strings.ReplaceAll(env, "_", "_"))
			return fmt.Sprintf("[hint: %s environment variable is not set. "+
				"Find the install path and set it: export %s=/path/to/installation]",
				envUpper, envUpper)
		}
	}

	// Generic "environment variable not set/defined" pattern.
	if (strings.Contains(lower, "environment variable") || strings.Contains(lower, "env var")) &&
		(strings.Contains(lower, "not set") || strings.Contains(lower, "not defined") ||
			strings.Contains(lower, "not found") || strings.Contains(lower, "is required") ||
			strings.Contains(lower, "must be set") || strings.Contains(lower, "is missing")) {
		return "[hint: a required environment variable is not set. Check the error message for the variable name " +
			"and set it with: export VAR_NAME=value]"
	}

	// Python KeyError on env var access (os.environ['FOO']).
	if strings.Contains(output, "KeyError:") && strings.Contains(lower, "os.environ") {
		return "[hint: Python KeyError on os.environ — a required environment variable is missing. " +
			"Set it with: export VAR_NAME=value, or use os.environ.get('VAR', 'default') in the code]"
	}

	return ""
}

// downloadHint detects curl, wget, and other download tool failures.
// Common in eval containers with limited network, or when downloading assets.
func downloadHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// curl-specific errors (curl exit codes are well-defined).
	if strings.Contains(lower, "curl:") || strings.Contains(lower, "curl error") {
		if strings.Contains(lower, "could not resolve host") {
			return "[hint: curl DNS resolution failed — the hostname can't be resolved. " +
				"Check internet connectivity, or try a different mirror/URL. " +
				"If offline, look for locally cached files instead.]"
		}
		if strings.Contains(lower, "failed to connect") || strings.Contains(lower, "connection refused") {
			return "[hint: curl connection failed — the server refused the connection. " +
				"Check if the URL is correct and the service is running. " +
				"For local services, verify the port with: ss -tlnp | grep <port>]"
		}
		if strings.Contains(lower, "operation timed out") || strings.Contains(lower, "connection timed out") {
			return "[hint: curl timed out — try increasing timeout with: curl --connect-timeout 30 --max-time 120 <url>, " +
				"or try a different mirror/URL]"
		}
		if strings.Contains(lower, "ssl") || strings.Contains(lower, "certificate") {
			return "[hint: curl SSL/certificate error — try: curl -k <url> (skip verification), " +
				"or update CA certificates: apt-get install -y ca-certificates]"
		}
		if strings.Contains(lower, "404") || strings.Contains(lower, "not found") {
			return "[hint: curl got 404 Not Found — the URL doesn't exist. " +
				"Check the URL for typos, or find the correct download URL from the project's releases page]"
		}
	}

	// wget-specific errors.
	if strings.Contains(lower, "wget:") || strings.Contains(lower, "wget error") {
		if strings.Contains(lower, "unable to resolve") {
			return "[hint: wget DNS resolution failed — check internet connectivity or try a different URL]"
		}
		if strings.Contains(lower, "failed") && strings.Contains(lower, "retrying") {
			return "[hint: wget download failed — try with --no-check-certificate if it's an SSL issue, " +
				"or use curl as an alternative]"
		}
	}

	// Generic download failure patterns (Python requests, Node fetch, etc.).
	if strings.Contains(lower, "connectionerror") && (strings.Contains(lower, "requests") || strings.Contains(lower, "urllib")) {
		return "[hint: Python HTTP request failed — check network connectivity. " +
			"If offline, look for cached data or local alternatives]"
	}
	if strings.Contains(lower, "fetch failed") || (strings.Contains(lower, "enotfound") && strings.Contains(lower, "getaddrinfo")) {
		return "[hint: network fetch failed — DNS resolution or connection error. " +
			"Check if the URL is correct and network is available]"
	}

	return ""
}

// sedHint detects common sed syntax and usage errors.
// Agents frequently misuse sed (wrong delimiters, missing flags, escaping issues).
// This saves 1-2 turns of trial-and-error.
func sedHint(output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lower := strings.ToLower(output)

	// sed: unterminated / unmatched / incomplete expression.
	if strings.Contains(lower, "sed:") || strings.Contains(lower, "sed -") {
		if strings.Contains(lower, "unterminated") {
			return "[hint: sed syntax error — unterminated expression. Check that all s/old/new/ delimiters are balanced. " +
				"If your pattern contains '/', use a different delimiter: s|old|new|g. " +
				"IMPORTANT: prefer the edit tool over sed for file modifications — it's safer and more reliable.]"
		}
		if strings.Contains(lower, "unknown option") || strings.Contains(lower, "invalid option") {
			return "[hint: sed option error — on macOS, use sed -i '' (empty string arg) instead of sed -i. " +
				"On Linux, sed -i works without the extra argument. " +
				"IMPORTANT: prefer the edit tool over sed for file modifications.]"
		}
		if strings.Contains(lower, "no such file") {
			return "[hint: sed can't find the target file. Check the file path. " +
				"IMPORTANT: prefer the edit tool for modifying files — it has better error handling.]"
		}
		// Generic sed error.
		if strings.Contains(lower, "expression") || strings.Contains(lower, "command") {
			return "[hint: sed syntax error — check delimiter escaping and expression format. " +
				"IMPORTANT: prefer the edit tool over sed for file modifications — it handles whitespace and multi-line edits safely.]"
		}
	}

	return ""
}

// isTransientBashFailure returns true if the bash output suggests a transient
// failure that's likely to succeed on retry (network errors, lock contention).
// The command parameter enables context-aware decisions: "connection refused"
// is transient during package installs but NOT when testing a local service
// (where it means the service isn't running — retrying won't help).
func isTransientBashFailure(exitCode int, output string, command string) bool {
	if exitCode == 0 {
		return false
	}
	lower := strings.ToLower(output)
	cmdLower := strings.ToLower(command)

	// "Connection refused" is only transient for package managers and remote fetches.
	// For local service tests (curl localhost, wget localhost), the service is simply
	// not running — retrying wastes 2 seconds and the result is always the same.
	if strings.Contains(lower, "connection refused") {
		isInstallCmd := strings.Contains(cmdLower, "apt") ||
			strings.Contains(cmdLower, "pip") ||
			strings.Contains(cmdLower, "npm") ||
			strings.Contains(cmdLower, "gem") ||
			strings.Contains(cmdLower, "cargo") ||
			strings.Contains(cmdLower, "go get") ||
			strings.Contains(cmdLower, "go mod") ||
			strings.Contains(cmdLower, "wget") && !strings.Contains(cmdLower, "localhost")
		if !isInstallCmd {
			return false // not transient for service tests
		}
	}

	transientPatterns := []string{
		"could not resolve host",
		"connection timed out",
		"connection refused", // now only reached for install commands (above guard)
		"temporary failure in name resolution",
		"network is unreachable",
		"unable to fetch",
		"failed to download",
		"dpkg was interrupted",
		"unable to acquire the dpkg",
		"is another process using it",
		"hash sum mismatch", // apt mirror inconsistency
		"failed to fetch",   // apt download failure
		"connection reset by peer",
		"ssl_error_syscall",
		"read: connection reset",
		"429 too many requests", // HTTP rate limiting
		"rate limit exceeded",   // generic rate limiting
		"service unavailable",   // HTTP 503
		"502 bad gateway",       // reverse proxy transient
		"connectionerror",       // Python requests ConnectionError
		"econnreset",            // Node.js connection reset
		"etimedout",             // Node.js timeout
		"esockettimedout",       // Node.js socket timeout
		"socket hang up",        // Node.js socket drop
		"enotfound",             // Node.js DNS resolution failure
		"getaddrinfo enotfound", // Node.js DNS lookup failure
		"getaddrinfo failed",    // Python/curl DNS lookup failure
		"err_name_not_resolved", // Chromium DNS failure
	}
	for _, p := range transientPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
