package nlsearch

type Plan struct {
	Queries      []string `json:"queries"`
	InputTokens  int64    `json:"input_tokens,omitempty"`
	OutputTokens int64    `json:"output_tokens,omitempty"`
}
