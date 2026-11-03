package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// MusicGeneration implements official Mureka generation/query for the
// shared JobRuntime and the headless CLI; no UI-specific adapter is required.
type MusicGeneration struct{}

type MusicProviderInfo struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Status     string   `json:"status"`
	Requires   []string `json:"requires"`
	Configured bool     `json:"configured"`
	Notes      string   `json:"notes"`
}

type MusicRequest struct {
	Provider        string `json:"provider"`
	Prompt          string `json:"prompt"`
	Lyrics          string `json:"lyrics"`
	Instrumental    bool   `json:"instrumental"`
	Model           string `json:"model"`
	DurationSeconds int    `json:"durationSeconds"`
}

type MusicTaskInput struct {
	TaskID       string `json:"taskId"`
	Instrumental bool   `json:"instrumental"`
}

type MusicTaskResult struct {
	Provider       string `json:"provider"`
	OK             bool   `json:"ok"`
	Status         string `json:"status"`
	ProviderTaskID string `json:"providerTaskId,omitempty"`
	AudioURL       string `json:"audioUrl,omitempty"`
	Message        string `json:"message"`
}

type murekaTask struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	FailedReason string `json:"failed_reason"`
	Choices      []struct {
		URL      string `json:"url"`
		AudioURL string `json:"audio_url"`
		WavURL   string `json:"wav_url"`
	} `json:"choices"`
}

var murekaClient = &http.Client{Timeout: 60 * time.Second}

func murekaKey() string {
	return strings.TrimSpace(os.Getenv("MUREKA_API_KEY"))
}

func murekaURL() (string, error) {
	raw := strings.TrimSuffix(strings.TrimSpace(os.Getenv("MUREKA_BASE_URL")), "/")
	if raw == "" {
		return "https://api.mureka.ai", nil
	}
	// Credentials must never be sent to an arbitrary configured host.
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Host != "api.mureka.ai" && parsed.Host != "api.mureka.cn") {
		return "", errors.New("MUREKA_BASE_URL must be https://api.mureka.ai or https://api.mureka.cn")
	}
	return raw, nil
}

// ListProviders reports available official music adapters and their local
// configuration. No network request or paid generation is performed.
func (MusicGeneration) ListProviders() []MusicProviderInfo {
	_, endpointErr := murekaURL()
	configured := murekaKey() != "" && endpointErr == nil
	return []MusicProviderInfo{
		{
			ID: "mureka", Label: "Mureka", Status: "official",
			Requires: []string{"MUREKA_API_KEY"}, Configured: configured,
			Notes: "Asynchronous song/instrumental API; use job sync to poll and archive.",
		},
		{
			ID: "elevenlabs", Label: "ElevenLabs Music", Status: "official",
			Requires:   []string{"ELEVENLABS_API_KEY"},
			Configured: strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY")) != "",
			Notes:      "Direct MP3 output; no provider task to poll; music_v2_5 default.",
		},
		{
			ID: "lyria", Label: "Google Lyria", Status: "official",
			Requires:   []string{"GEMINI_API_KEY"},
			Configured: strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "",
			Notes:      "Gemini Interactions API, direct MP3; lyria-3-clip-preview default.",
		},
	}
}

// Generate creates exactly one paid Mureka task after an explicit user action.
func (MusicGeneration) Generate(ctx context.Context, input MusicRequest) (MusicTaskResult, error) {
	if input.Provider != "mureka" {
		return MusicTaskResult{}, errors.New("the MyGo backend currently supports Mureka only")
	}
	prompt := strings.TrimSpace(input.Prompt)
	lyrics := strings.TrimSpace(input.Lyrics)
	if prompt == "" || utf8.RuneCountInString(prompt) > 1024 {
		return MusicTaskResult{}, errors.New("music description must contain 1–1024 characters")
	}
	if utf8.RuneCountInString(lyrics) > 5000 {
		return MusicTaskResult{}, errors.New("lyrics exceed 5000 characters")
	}

	model := strings.TrimSpace(input.Model)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("MUREKA_MODEL"))
	}
	if model == "" {
		model = "auto"
	}

	endpoint := "/v1/instrumental/generate"
	payload := map[string]any{"model": model, "prompt": prompt, "n": 1}
	if !input.Instrumental {
		if lyrics == "" {
			endpoint = "/v1/song/easy-generate"
		} else {
			endpoint = "/v1/song/generate"
			payload["lyrics"] = lyrics
			if input.DurationSeconds != 0 && model == "mureka-9.5" {
				if input.DurationSeconds < 120 || input.DurationSeconds > 300 {
					return MusicTaskResult{}, errors.New("Mureka 9.5 duration must be 120–300 seconds")
				}
				payload["duration"] = input.DurationSeconds
			}
		}
	}

	var task murekaTask
	if err := murekaRequest(ctx, http.MethodPost, endpoint, payload, &task); err != nil {
		return MusicTaskResult{}, err
	}
	if task.ID == "" {
		return MusicTaskResult{}, errors.New("Mureka returned no task ID")
	}
	return MusicTaskResult{
		Provider: "mureka", OK: true, Status: "submitted",
		ProviderTaskID: task.ID, Message: "Mureka task submitted.",
	}, nil
}

// GetMurekaTask fetches status and an audio URL without creating a new paid task.
func (MusicGeneration) GetMurekaTask(ctx context.Context, input MusicTaskInput) (MusicTaskResult, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if len(taskID) == 0 || len(taskID) > 128 {
		return MusicTaskResult{}, errors.New("invalid task ID")
	}
	for _, ch := range taskID {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' ||
			ch >= 'A' && ch <= 'Z' || ch == '-' || ch == '_') {
			return MusicTaskResult{}, errors.New("invalid task ID")
		}
	}
	kind := "song"
	if input.Instrumental {
		kind = "instrumental"
	}
	var task murekaTask
	endpoint := "/v1/" + kind + "/query/" + url.PathEscape(taskID)
	if err := murekaRequest(ctx, http.MethodGet, endpoint, nil, &task); err != nil {
		return MusicTaskResult{}, err
	}

	status := "submitted"
	ok := true
	message := task.Status
	switch task.Status {
	case "succeeded":
		status = "succeeded"
	case "failed", "timeouted", "cancelled":
		status, ok = "failed", false
		if task.FailedReason != "" {
			message = task.FailedReason
		}
	}
	audioURL := ""
	if len(task.Choices) > 0 {
		choice := task.Choices[0]
		audioURL = choice.URL
		if audioURL == "" {
			audioURL = choice.AudioURL
		}
		if audioURL == "" {
			audioURL = choice.WavURL
		}
	}
	return MusicTaskResult{
		Provider: "mureka", OK: ok, Status: status,
		ProviderTaskID: taskID, AudioURL: audioURL, Message: message,
	}, nil
}

func murekaRequest(ctx context.Context, method, endpoint string, payload any, result any) error {
	key := murekaKey()
	if key == "" {
		return errors.New("MUREKA_API_KEY is not configured")
	}
	baseURL, err := murekaURL()
	if err != nil {
		return err
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := murekaClient.Do(req)
	if err != nil {
		return fmt.Errorf("Mureka connection failed: %w", err)
	}
	defer res.Body.Close()

	limited := io.LimitReader(res.Body, 2<<20)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		io.Copy(io.Discard, limited)
		return fmt.Errorf("Mureka returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(limited).Decode(result); err != nil {
		return fmt.Errorf("invalid Mureka response: %w", err)
	}
	return nil
}
