package model

import (
	"cmp"
	"fmt"
	"os"
)

// Provider is a typed catalog name (OpenRouter, OpenAI, …).
// The zero value is invalid. Parameters.Provider is unrelated OpenRouter routing JSON.
type Provider string

const (
	OpenRouter Provider = "openrouter"
	OpenAI     Provider = "openai"
)

// Providers is the allow-list of typed names.
var Providers = []Provider{OpenRouter, OpenAI}

type providerConfig struct {
	URL          string
	APIKeyEnv    string
	DefaultModel string
}

func lookup(p Provider) (providerConfig, error) {
	switch p {
	case OpenRouter:
		return providerConfig{
			URL:          "https://openrouter.ai/api/v1/chat/completions",
			APIKeyEnv:    "OPENROUTER_API_KEY",
			DefaultModel: "openrouter/free",
		}, nil
	case OpenAI:
		return providerConfig{
			URL:       "https://api.openai.com/v1/chat/completions",
			APIKeyEnv: "OPENAI_API_KEY",
		}, nil
	default:
		return providerConfig{}, fmt.Errorf("unknown provider %q", p)
	}
}

// resolve applies Parameters.URL / Parameters.APIKey overlays on the catalog row.
func resolve(p Provider, params Parameters) (url, key string, err error) {
	cfg, err := lookup(p)
	if err != nil {
		return "", "", err
	}
	url = cmp.Or(params.URL, cfg.URL)
	key = cmp.Or(params.APIKey, os.Getenv(cfg.APIKeyEnv))
	if key == "" {
		return "", "", fmt.Errorf("missing API key: set %s or Parameters.APIKey", cfg.APIKeyEnv)
	}
	return url, key, nil
}
