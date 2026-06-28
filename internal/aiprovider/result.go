package aiprovider

// GenerateResult holds the output of an AI generation call.
type GenerateResult struct {
	Content      string
	Raw          string
	FinishReason string // "stop", "length", "max_tokens", or "" for unknown
}
