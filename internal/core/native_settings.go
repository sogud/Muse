package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type SettingChange struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type AIConfiguration struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseUrl"`
}

type AIStatus struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ProviderStatus is a local configuration check, not a live network test.
// Neither keys nor raw credentials leave this process through the status API.
type ProviderStatus struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Capabilities     []string `json:"capabilities"`
	Configured       bool     `json:"configured"`
	CredentialSource string   `json:"credentialSource"`
	Detail           string   `json:"detail"`
}

// NativeSettings exposes the existing SQLite settings without leaking AI keys.
type NativeSettings struct{ projects *ProjectStore }

func NewNativeSettings(projects *ProjectStore) *NativeSettings {
	return &NativeSettings{projects: projects}
}

func (s *NativeSettings) read(key string) string {
	if s.projects.db == nil {
		return ""
	}
	var value string
	if err := s.projects.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&value); err != nil {
		return ""
	}
	return value
}

func (s *NativeSettings) write(ctx context.Context, key, value string) error {
	if s.projects.db == nil {
		return errors.New("database is not initialized")
	}
	_, err := s.projects.db.ExecContext(ctx,
		"INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at",
		key, value, time.Now().UnixMilli())
	return err
}

func (s *NativeSettings) Get(_ context.Context, key string) *string {
	if key == "ai.apiKey" {
		return nil
	}
	switch key {
	case "ai.provider", "ai.model", "ai.baseUrl", "appearance.theme", "fluidsynth.path", "soundfont.path":
		if s.projects.db == nil {
			return nil
		}
		var value string
		if err := s.projects.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&value); err == nil {
			return &value
		}
	}
	return nil
}

func (s *NativeSettings) GetAll(ctx context.Context) (map[string]string, error) {
	if s.projects.db == nil {
		return nil, errors.New("database is not initialized")
	}
	rows, err := s.projects.db.QueryContext(ctx, "SELECT key,value FROM settings ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		if key != "ai.apiKey" {
			values[key] = value
		}
	}
	return values, rows.Err()
}

func (s *NativeSettings) Set(ctx context.Context, input SettingChange) error {
	switch input.Key {
	case "appearance.theme", "fluidsynth.path", "soundfont.path":
		return s.write(ctx, input.Key, input.Value)
	default:
		return errors.New("setting must be changed through its dedicated configuration API")
	}
}

func (s *NativeSettings) ConfigureAi(ctx context.Context, input AIConfiguration) error {
	if len(input.Model) == 0 || len(input.Model) > 160 || len(input.BaseURL) > 500 || len(input.APIKey) > 4096 {
		return errors.New("invalid AI configuration length")
	}
	switch input.Provider {
	case "openai", "local", "deepseek", "google", "anthropic":
	default:
		return errors.New("unsupported AI provider")
	}
	if input.BaseURL != "" {
		if _, err := validatedAIURL(input.BaseURL); err != nil {
			return err
		}
	}
	previousProvider := s.read("ai.provider")
	previousEndpoint := s.read("ai.baseUrl")
	changed := previousProvider != input.Provider || previousEndpoint != strings.TrimSpace(input.BaseURL)
	if changed && input.APIKey == "" && input.Provider != "local" && s.read("ai.apiKey") != "" {
		return errors.New("Provider or Endpoint changed: enter a new API Key before saving")
	}
	tx, err := s.projects.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := map[string]string{
		"ai.provider": input.Provider,
		"ai.model":    strings.TrimSpace(input.Model),
		"ai.baseUrl":  strings.TrimSpace(input.BaseURL),
	}
	if input.APIKey != "" {
		values["ai.apiKey"] = input.APIKey
	} else if changed {
		// Do not forward a previous provider's key to the new endpoint.
		values["ai.apiKey"] = ""
	}
	for key, value := range values {
		if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at", key, value, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListProviderStatuses is the shared source for the MyGo settings screen and
// the JSON CLI. It checks configuration only, never contacts a paid provider.
func (s *NativeSettings) ListProviderStatuses() []ProviderStatus {
	availableMusic := MusicGeneration{}.ListProviders()
	mureka := availableMusic[0]
	music := ProviderStatus{
		ID: "mureka", Label: mureka.Label,
		Capabilities:     []string{"music.generate"},
		CredentialSource: "environment",
	}
	switch {
	case murekaKey() == "":
		music.Detail = "未找到当前进程的 MUREKA_API_KEY；需为 CLI 或 Muse.app 分别提供运行环境。"
	case !mureka.Configured:
		music.Detail = "MUREKA_BASE_URL 无效；只允许 Mureka 官方 HTTPS API 域名。"
	default:
		music.Configured = true
		music.Detail = "进程环境变量已设置，尚未验证 API 连接和付费额度。"
	}
	statuses := []ProviderStatus{music}
	for _, provider := range availableMusic[1:] {
		item := ProviderStatus{
			ID: provider.ID, Label: provider.Label,
			Capabilities:     []string{"music.generate"},
			CredentialSource: "environment",
			Configured:       provider.Configured,
		}
		if item.Configured {
			item.Detail = "当前进程已有 API Key；此 API 直接返回音频，尚未验证网络或收费。"
		} else {
			item.Detail = "未找到 " + provider.Requires[0] + "；CLI 和 Muse.app 需要各自的运行环境。"
		}
		statuses = append(statuses, item)
	}

	cfg := s.aiConfiguration()
	provider := cfg.Provider
	if provider == "" {
		provider = "openai" // same default used by AiPattern
	}
	text := ProviderStatus{
		ID: "text-pattern", Label: "文本模型 · Pattern DSL",
		Capabilities: []string{"pattern.generate"},
	}
	if s.read("ai.apiKey") != "" {
		text.CredentialSource = "legacy_sqlite"
	} else if strings.TrimSpace(os.Getenv("OTODESK_AI_API_KEY")) != "" {
		text.CredentialSource = "environment"
	} else {
		text.CredentialSource = "none"
	}

	switch provider {
	case "openai", "deepseek", "anthropic", "google", "local":
		// These are the text providers handled by the existing AiPattern.
	default:
		text.Detail = "不支持的文本 Provider，请在设置中重新配置。"
		return append(statuses, text)
	}

	if cfg.BaseURL != "" {
		if _, err := validatedAIURL(cfg.BaseURL); err != nil {
			text.Detail = "文本模型 Endpoint 无效：必须为 HTTPS，或本机 localhost HTTP。"
			return append(statuses, text)
		}
	}
	if provider != "local" && cfg.APIKey == "" {
		text.Detail = "缺少文本模型 API Key；该配置不用于 Mureka 音乐生成。"
		return append(statuses, text)
	}
	if provider == "local" && cfg.Model == "" {
		text.Detail = "请填写本地模型 ID 和 Endpoint；本地服务是否运行尚未检测。"
		return append(statuses, text)
	}

	text.Configured = true
	if text.CredentialSource == "legacy_sqlite" {
		text.Detail = "配置存在，但密钥仍存于旧 SQLite；尚未验证连接或迁移 Keychain。"
	} else if provider == "local" {
		text.Detail = "本地模型参数已填写；尚未验证服务可用性。"
	} else {
		text.Detail = "文本 Provider 配置已填写，尚未验证 API 连接和额度。"
	}
	return append(statuses, text)
}

// apiJSONRequest bounds network requests and intentionally does not include
// secrets in status messages or errors.
func aiJSONRequest(ctx context.Context, method, urlString, apiKey string, payload any, result any) error {
	var reader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(data))
	}
	contextWithTimeout, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(contextWithTimeout, method, urlString, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := http.Client{Timeout: 45 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("AI request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return fmt.Errorf("AI provider returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(result); err != nil {
		return fmt.Errorf("invalid provider JSON: %w", err)
	}
	return nil
}

func (s *NativeSettings) aiConfiguration() AIConfiguration {
	field := func(key, env string) string {
		if v := s.read(key); v != "" {
			return v
		}
		return strings.TrimSpace(os.Getenv(env))
	}
	return AIConfiguration{
		Provider: field("ai.provider", "OTODESK_AI_PROVIDER"),
		Model:    field("ai.model", "OTODESK_AI_MODEL"),
		APIKey:   field("ai.apiKey", "OTODESK_AI_API_KEY"),
		BaseURL:  field("ai.baseUrl", "OTODESK_AI_BASE_URL"),
	}
}
