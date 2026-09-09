package model

// NewParameters returns chat-completion defaults for a catalog provider.
// Mutate the returned value before passing to Complete or storing on Agent.
func NewParameters(p Provider) (Parameters, error) {
	cfg, err := lookup(p)
	if err != nil {
		return Parameters{}, err
	}
	temp := 0.7
	maxCompletion := 4096
	out := Parameters{
		Model:               cfg.DefaultModel,
		Temperature:         &temp,
		MaxCompletionTokens: &maxCompletion,
	}
	if p == OpenRouter {
		out.Provider = map[string]any{
			"allow_fallbacks": true,
		}
	}
	return out, nil
}
