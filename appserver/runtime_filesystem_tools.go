package appserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	toolfs "github.com/fugue-labs/gollem/appserver/tools/fs"
	"github.com/fugue-labs/gollem/core"
	"github.com/pmezard/go-difflib/difflib"
)

const (
	runtimeFilesystemToolNamespace   = "workspace"
	runtimeFilesystemContentMaxBytes = 64 * 1024
)

type runtimeFilesystemPathParams struct {
	Path string `json:"path" jsonschema:"description=Workspace-relative or workspace-contained absolute path"`
}

type runtimeFilesystemWriteParams struct {
	Path    string `json:"path" jsonschema:"description=Workspace-relative or workspace-contained absolute file path"`
	Content string `json:"content" jsonschema:"description=Complete UTF-8 file content"`
}

type runtimeFilesystemCopyParams struct {
	Source      string `json:"source" jsonschema:"description=Workspace-relative or workspace-contained source path"`
	Destination string `json:"destination" jsonschema:"description=Workspace-relative or workspace-contained destination path"`
}

type runtimeFilesystemReadResult struct {
	Path             string    `json:"path"`
	Content          string    `json:"content,omitempty"`
	ContentEncoding  string    `json:"contentEncoding,omitempty"`
	ContentTruncated bool      `json:"contentTruncated,omitempty"`
	OmittedReason    string    `json:"omittedReason,omitempty"`
	SHA256           string    `json:"sha256"`
	Size             int64     `json:"size"`
	Mode             string    `json:"mode"`
	ModifiedAt       time.Time `json:"modifiedAt"`
}

type runtimeFilesystemEntryResult struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	IsDir      bool      `json:"isDir"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type runtimeFilesystemListResult struct {
	Path    string                         `json:"path"`
	Entries []runtimeFilesystemEntryResult `json:"entries"`
}

type runtimeFilesystemMetadataResult struct {
	Path       string    `json:"path"`
	IsDir      bool      `json:"isDir"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type runtimeFilesystemMutationResult struct {
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Operation   string `json:"operation"`
	Changed     bool   `json:"changed"`
}

// FilesystemRuntimeTools adapts the scoped app-server filesystem service into
// provider-neutral model tools. Mutations retain the service's approval and
// audit hooks and publish bounded artifact evidence on the active run bus.
func FilesystemRuntimeTools(service *toolfs.Service) []core.Tool {
	if service == nil {
		return nil
	}
	tools := []core.Tool{
		core.FuncTool[runtimeFilesystemPathParams](
			"workspace_read_file",
			"Read a workspace file. Text is bounded; binary or oversized content is represented by metadata and a digest.",
			func(ctx context.Context, params runtimeFilesystemPathParams) (runtimeFilesystemReadResult, error) {
				if strings.TrimSpace(params.Path) == "" {
					return runtimeFilesystemReadResult{}, errors.New("path is required")
				}
				content, err := service.ReadFile(ctx, params.Path)
				if err != nil {
					return runtimeFilesystemReadResult{}, err
				}
				return newRuntimeFilesystemReadResult(content), nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
		core.FuncTool[runtimeFilesystemPathParams](
			"workspace_list_directory",
			"List one workspace directory with path, type, size, mode, and modification metadata.",
			func(ctx context.Context, params runtimeFilesystemPathParams) (runtimeFilesystemListResult, error) {
				if strings.TrimSpace(params.Path) == "" {
					params.Path = "."
				}
				entries, err := service.ReadDirectory(ctx, params.Path)
				if err != nil {
					return runtimeFilesystemListResult{}, err
				}
				out := make([]runtimeFilesystemEntryResult, 0, len(entries))
				for _, entry := range entries {
					out = append(out, runtimeFilesystemEntryResult{
						Path:       filepath.ToSlash(entry.Path),
						Name:       entry.Name,
						IsDir:      entry.IsDir,
						Size:       entry.Size,
						Mode:       entry.Mode.String(),
						ModifiedAt: entry.ModTime,
					})
				}
				return runtimeFilesystemListResult{Path: filepath.ToSlash(filepath.Clean(params.Path)), Entries: out}, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
		core.FuncTool[runtimeFilesystemPathParams](
			"workspace_file_metadata",
			"Inspect workspace path metadata without reading file content.",
			func(ctx context.Context, params runtimeFilesystemPathParams) (runtimeFilesystemMetadataResult, error) {
				if strings.TrimSpace(params.Path) == "" {
					return runtimeFilesystemMetadataResult{}, errors.New("path is required")
				}
				metadata, err := service.Metadata(ctx, params.Path)
				if err != nil {
					return runtimeFilesystemMetadataResult{}, err
				}
				return runtimeFilesystemMetadataResult{
					Path:       filepath.ToSlash(metadata.Path),
					IsDir:      metadata.IsDir,
					Size:       metadata.Size,
					Mode:       metadata.Mode.String(),
					ModifiedAt: metadata.ModTime,
				}, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
		core.FuncTool[runtimeFilesystemWriteParams](
			"workspace_write_file",
			"Create or replace a UTF-8 workspace file through the configured mutation approval policy.",
			func(ctx context.Context, rc *core.RunContext, params runtimeFilesystemWriteParams) (runtimeFilesystemMutationResult, error) {
				if strings.TrimSpace(params.Path) == "" {
					return runtimeFilesystemMutationResult{}, errors.New("path is required")
				}
				before, err := captureRuntimeArtifact(ctx, service, params.Path)
				if err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				if err := service.WriteFile(ctx, params.Path, []byte(params.Content), 0o644); err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				after, err := captureRuntimeArtifact(ctx, service, params.Path)
				if err != nil {
					return runtimeFilesystemMutationResult{}, fmt.Errorf("capture written artifact: %w", err)
				}
				operation := "update"
				if !before.Exists {
					operation = "create"
				}
				changed := publishRuntimeArtifactChange(ctx, rc, before, after, operation)
				return runtimeFilesystemMutationResult{Path: after.Path, Operation: operation, Changed: changed}, nil
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(2*time.Minute),
		),
		core.FuncTool[runtimeFilesystemPathParams](
			"workspace_create_directory",
			"Create a workspace directory through the configured mutation approval policy.",
			func(ctx context.Context, rc *core.RunContext, params runtimeFilesystemPathParams) (runtimeFilesystemMutationResult, error) {
				if strings.TrimSpace(params.Path) == "" {
					return runtimeFilesystemMutationResult{}, errors.New("path is required")
				}
				before, err := captureRuntimeArtifact(ctx, service, params.Path)
				if err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				if err := service.CreateDirectory(ctx, params.Path); err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				after, err := captureRuntimeArtifact(ctx, service, params.Path)
				if err != nil {
					return runtimeFilesystemMutationResult{}, fmt.Errorf("capture created directory: %w", err)
				}
				changed := publishRuntimeArtifactChange(ctx, rc, before, after, "create")
				return runtimeFilesystemMutationResult{Path: after.Path, Operation: "createDirectory", Changed: changed}, nil
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(2*time.Minute),
		),
		core.FuncTool[runtimeFilesystemPathParams](
			"workspace_remove_path",
			"Remove a workspace file or directory through the configured destructive-mutation approval policy.",
			func(ctx context.Context, rc *core.RunContext, params runtimeFilesystemPathParams) (runtimeFilesystemMutationResult, error) {
				if strings.TrimSpace(params.Path) == "" {
					return runtimeFilesystemMutationResult{}, errors.New("path is required")
				}
				before, err := captureRuntimeArtifact(ctx, service, params.Path)
				if err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				if err := service.Remove(ctx, params.Path); err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				after, err := captureRuntimeArtifact(ctx, service, params.Path)
				if err != nil {
					return runtimeFilesystemMutationResult{}, fmt.Errorf("capture removed artifact: %w", err)
				}
				changed := publishRuntimeArtifactChange(ctx, rc, before, after, "delete")
				return runtimeFilesystemMutationResult{Path: before.Path, Operation: "remove", Changed: changed}, nil
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(2*time.Minute),
		),
		core.FuncTool[runtimeFilesystemCopyParams](
			"workspace_copy_path",
			"Copy a workspace file or directory through the configured mutation approval policy.",
			func(ctx context.Context, rc *core.RunContext, params runtimeFilesystemCopyParams) (runtimeFilesystemMutationResult, error) {
				if strings.TrimSpace(params.Source) == "" || strings.TrimSpace(params.Destination) == "" {
					return runtimeFilesystemMutationResult{}, errors.New("source and destination are required")
				}
				before, err := captureRuntimeArtifact(ctx, service, params.Destination)
				if err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				if err := service.Copy(ctx, params.Source, params.Destination); err != nil {
					return runtimeFilesystemMutationResult{}, err
				}
				after, err := captureRuntimeArtifact(ctx, service, params.Destination)
				if err != nil {
					return runtimeFilesystemMutationResult{}, fmt.Errorf("capture copied artifact: %w", err)
				}
				operation := "update"
				if !before.Exists {
					operation = "create"
				}
				changed := publishRuntimeArtifactChange(ctx, rc, before, after, operation)
				return runtimeFilesystemMutationResult{
					Path:        filepath.ToSlash(filepath.Clean(params.Source)),
					Destination: after.Path,
					Operation:   "copy",
					Changed:     changed,
				}, nil
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(2*time.Minute),
		),
	}
	for i := range tools {
		tools[i].Definition.Namespace = runtimeFilesystemToolNamespace
	}
	return tools
}

func newRuntimeFilesystemReadResult(content *toolfs.FileContent) runtimeFilesystemReadResult {
	result := runtimeFilesystemReadResult{
		Path:       filepath.ToSlash(content.Path),
		SHA256:     runtimeSHA256(content.Content),
		Size:       content.Size,
		Mode:       content.Mode.String(),
		ModifiedAt: content.ModTime,
	}
	if !runtimeArtifactText(content.Content) {
		result.OmittedReason = "binary content omitted"
		return result
	}
	result.ContentEncoding = "utf-8"
	result.Content, result.ContentTruncated = boundedRuntimeArtifactText(content.Content)
	return result
}

type runtimeArtifactCapture struct {
	Path    string
	Exists  bool
	IsDir   bool
	Size    int64
	Content []byte
	SHA256  string
}

func captureRuntimeArtifact(ctx context.Context, service *toolfs.Service, path string) (runtimeArtifactCapture, error) {
	metadata, err := service.Metadata(ctx, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtimeArtifactCapture{Path: runtimeRelativeFilesystemPath(service.Root(), path)}, nil
		}
		return runtimeArtifactCapture{}, err
	}
	capture := runtimeArtifactCapture{
		Path:   filepath.ToSlash(metadata.Path),
		Exists: true,
		IsDir:  metadata.IsDir,
		Size:   metadata.Size,
	}
	if metadata.IsDir {
		return capture, nil
	}
	content, err := service.ReadFile(ctx, path)
	if err != nil {
		return runtimeArtifactCapture{}, err
	}
	capture.Content = append([]byte(nil), content.Content...)
	capture.SHA256 = runtimeSHA256(content.Content)
	return capture, nil
}

func publishRuntimeArtifactChange(ctx context.Context, rc *core.RunContext, before, after runtimeArtifactCapture, operation string) bool {
	if rc == nil || rc.EventBus == nil || runtimeArtifactCapturesEqual(before, after) {
		return false
	}
	path := after.Path
	if path == "" {
		path = before.Path
	}
	diff, diffTruncated, diffOmitted := runtimeArtifactDiff(path, before, after)
	beforeContent, afterContent, contentTruncated, contentOmitted := runtimeArtifactContents(before, after)
	bytesChanged := after.Size
	if !after.Exists {
		bytesChanged = before.Size
	}
	core.Publish(rc.EventBus, core.ArtifactChangedEvent{
		RunID:                rc.RunID,
		ParentRunID:          rc.ParentRunID,
		ToolCallID:           firstRuntimeNonEmpty(core.ToolCallIDFromContext(ctx), rc.ToolCallID),
		ToolName:             rc.ToolName,
		Path:                 filepath.ToSlash(path),
		Operation:            operation,
		Bytes:                bytesChanged,
		BeforeSHA256:         before.SHA256,
		AfterSHA256:          after.SHA256,
		Diff:                 diff,
		DiffTruncated:        diffTruncated,
		DiffOmittedReason:    diffOmitted,
		BeforeContent:        beforeContent,
		AfterContent:         afterContent,
		ContentEncoding:      runtimeArtifactContentEncoding(before, after, contentOmitted),
		ContentTruncated:     contentTruncated,
		ContentOmittedReason: contentOmitted,
		ChangedAt:            time.Now().UTC(),
	})
	return true
}

func runtimeArtifactCapturesEqual(before, after runtimeArtifactCapture) bool {
	if before.Exists != after.Exists || before.IsDir != after.IsDir || before.Size != after.Size {
		return false
	}
	if !before.Exists {
		return true
	}
	if before.IsDir {
		return true
	}
	return before.SHA256 == after.SHA256
}

func runtimeArtifactDiff(path string, before, after runtimeArtifactCapture) (string, bool, string) {
	if before.IsDir || after.IsDir {
		return "", false, "directory content omitted"
	}
	if len(before.Content) > runtimeFilesystemContentMaxBytes || len(after.Content) > runtimeFilesystemContentMaxBytes {
		return "", false, fmt.Sprintf("content exceeds %d byte diff limit", runtimeFilesystemContentMaxBytes)
	}
	if !runtimeArtifactText(before.Content) || !runtimeArtifactText(after.Content) {
		return "", false, "binary content omitted"
	}
	fromFile := "a/" + runtimeDiffPath(path)
	toFile := "b/" + runtimeDiffPath(path)
	if !before.Exists {
		fromFile = "/dev/null"
	}
	if !after.Exists {
		toFile = "/dev/null"
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(before.Content)),
		B:        difflib.SplitLines(string(after.Content)),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	})
	if err != nil {
		return "", false, "build unified diff: " + err.Error()
	}
	if len(diff) <= runtimeFilesystemContentMaxBytes {
		return diff, false, ""
	}
	bounded, _ := boundedRuntimeArtifactText([]byte(diff))
	return bounded + "\n... diff truncated ...\n", true, ""
}

func runtimeArtifactContents(before, after runtimeArtifactCapture) (string, string, bool, string) {
	if before.IsDir || after.IsDir {
		return "", "", false, "directory content omitted"
	}
	if !runtimeArtifactText(before.Content) || !runtimeArtifactText(after.Content) {
		return "", "", false, "binary content omitted"
	}
	beforeText, beforeTruncated := boundedRuntimeArtifactText(before.Content)
	afterText, afterTruncated := boundedRuntimeArtifactText(after.Content)
	return beforeText, afterText, beforeTruncated || afterTruncated, ""
}

func runtimeArtifactContentEncoding(before, after runtimeArtifactCapture, omitted string) string {
	if omitted != "" || before.IsDir || after.IsDir {
		return ""
	}
	return "utf-8"
}

func boundedRuntimeArtifactText(data []byte) (string, bool) {
	if len(data) <= runtimeFilesystemContentMaxBytes {
		return string(data), false
	}
	end := runtimeFilesystemContentMaxBytes
	for end > 0 && !utf8.RuneStart(data[end]) {
		end--
	}
	return string(data[:end]), true
}

func runtimeArtifactText(data []byte) bool {
	return !bytes.Contains(data, []byte{0}) && utf8.Valid(data)
}

func runtimeSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func runtimeRelativeFilesystemPath(root, path string) string {
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(rel)
}

func runtimeDiffPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimPrefix(path, "/")
	if path == "." || path == "" {
		return "artifact"
	}
	return path
}
