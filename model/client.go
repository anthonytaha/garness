package model

func Chat(
	provider Provider,
	messages []map[string]any,
	options *CompleteOptions,
) (*LLMResponse, error) {
	return completeOpenai(provider, messages, options)
}
