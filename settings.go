package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const defaultOpenAIBaseURL = "https://openrouter.ai/api/v1"

type userSettings struct {
	OpenAIAPIKey  string `json:"openai_api_key,omitempty"`
	OpenAIBaseURL string `json:"openai_base_url,omitempty"`
	OpenAIModel   string `json:"openai_model,omitempty"`
	KokoroVoice   string `json:"kokoro_voice,omitempty"`
}

type settingsStore struct {
	mu    sync.RWMutex
	path  string
	value userSettings
}

func openSettings() (*settingsStore, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find user config directory: %w", err)
	}
	path := filepath.Join(configDir, "trends", "settings.json")

	value := userSettings{
		OpenAIAPIKey:  os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL: envOrDefault("OPENAI_BASE_URL", defaultOpenAIBaseURL),
		OpenAIModel:   os.Getenv("OPENAI_MODEL"),
		KokoroVoice:   envOrDefault("KOKORO_VOICE", "af_heart"),
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var stored userSettings
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("read settings %s: %w", path, err)
		}
		value = stored
		if value.OpenAIBaseURL == "" {
			value.OpenAIBaseURL = defaultOpenAIBaseURL
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	if strings.TrimSpace(value.OpenAIBaseURL) == "" {
		value.OpenAIBaseURL = defaultOpenAIBaseURL
	}
	if value.KokoroVoice == "" {
		value.KokoroVoice = "af_heart"
	}
	if !isKokoroVoice(value.KokoroVoice) {
		return nil, fmt.Errorf("settings %s contains unsupported Kokoro voice %q", path, value.KokoroVoice)
	}
	return &settingsStore{path: path, value: value}, nil
}

func newSettingsStore(path string, value userSettings) *settingsStore {
	return &settingsStore{path: path, value: value}
}

func (s *settingsStore) snapshot() userSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

func (s *settingsStore) save(value userSettings) error {
	value.OpenAIAPIKey = strings.TrimSpace(value.OpenAIAPIKey)
	value.OpenAIBaseURL = strings.TrimSpace(value.OpenAIBaseURL)
	value.OpenAIModel = strings.TrimSpace(value.OpenAIModel)
	value.KokoroVoice = strings.TrimSpace(value.KokoroVoice)
	if value.OpenAIBaseURL == "" {
		value.OpenAIBaseURL = defaultOpenAIBaseURL
	}
	endpoint, err := url.Parse(value.OpenAIBaseURL)
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Host == "" || endpoint.User != nil {
		return fmt.Errorf("invalid API base URL %q: use an http or https URL", value.OpenAIBaseURL)
	}
	if !isKokoroVoice(value.KokoroVoice) {
		return fmt.Errorf("unsupported Kokoro voice %q", value.KokoroVoice)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary settings file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write settings: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close settings: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("install settings: %w", err)
	}

	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
