package tools

// FileEditConfig holds configuration for file editing tools.
type FileEditConfig struct {
	// MaxFileSize is the maximum file size in bytes that can be edited.
	MaxFileSize int64 `yaml:"max_file_size" json:"max_file_size"`
}
