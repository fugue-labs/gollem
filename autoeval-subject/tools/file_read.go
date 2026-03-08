package tools

// FileReadConfig holds configuration for file reading tools.
type FileReadConfig struct {
	// MaxLines limits the number of lines returned from a file read.
	MaxLines int `yaml:"max_lines" json:"max_lines"`
}
