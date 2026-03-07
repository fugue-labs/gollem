package tools

// SearchConfig holds configuration for code search tools.
type SearchConfig struct {
	// MaxResults limits the number of search results returned.
	MaxResults int `yaml:"max_results" json:"max_results"`
	// ContextLines is the number of context lines around matches.
	ContextLines int `yaml:"context_lines" json:"context_lines"`
}
