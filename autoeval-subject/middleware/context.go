package middleware

// ContextConfig configures context window management.
type ContextConfig struct {
	// MaxTokens is the token budget before summarization kicks in.
	MaxTokens int `yaml:"max_tokens" json:"max_tokens"`
	// KeepLastN is the number of recent messages to always preserve.
	KeepLastN int `yaml:"keep_last_n" json:"keep_last_n"`
}
