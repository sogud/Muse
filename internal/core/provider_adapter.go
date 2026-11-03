package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Every provider shares validation and readiness, but the result transport
// differs: Mureka is asynchronous while ElevenLabs and Lyria return audio
// directly. The CLI and MyGo continue using the same JobRuntime.
type JobProvider interface {
	Validate(CreativeJob) error
	Ready() error
}

type AsyncJobProvider interface {
	JobProvider
	Submit(context.Context, CreativeJob) (string, error)
	Poll(context.Context, CreativeJob, string) (ProviderProgress, error)
}

type ProviderProgress struct {
	State    string
	AudioURL string
	Message  string
}

// ValidateMusicProviderRequest applies provider-specific model, duration and
// prompt checks without credentials or any network call. Used by CLI dry-run.
func ValidateMusicProviderRequest(provider string, input json.RawMessage) error {
	job := CreativeJob{Kind: "music.generate", Provider: provider, Input: input}
	adapter, err := providerForJob(job)
	if err != nil {
		return err
	}
	return adapter.Validate(job)
}

// providerForJob is deliberately explicit: unsupported API families must
// fail instead of silently falling back to a different paid service.
func providerForJob(job CreativeJob) (JobProvider, error) {
	switch {
	case job.Kind == "music.generate" && job.Provider == "mureka":
		return murekaJobProvider{}, nil
	case job.Kind == "music.generate" && job.Provider == "elevenlabs":
		return elevenMusicProvider{}, nil
	case job.Kind == "music.generate" && job.Provider == "lyria":
		return lyriaMusicProvider{}, nil
	default:
		return nil, fmt.Errorf("job kind %q with provider %q is not executable yet", job.Kind, job.Provider)
	}
}

type murekaJobProvider struct{}

func (murekaJobProvider) Ready() error {
	if murekaKey() == "" {
		return errors.New("MUREKA_API_KEY is not configured")
	}
	_, err := murekaURL()
	return err
}

func decodeMurekaJob(job CreativeJob) (MusicRequest, error) {
	var input struct {
		Prompt          string `json:"prompt"`
		Lyrics          string `json:"lyrics"`
		Model           string `json:"model"`
		Instrumental    bool   `json:"instrumental"`
		DurationSeconds int    `json:"durationSeconds"`
	}
	decoder := json.NewDecoder(bytes.NewReader(job.Input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return MusicRequest{}, fmt.Errorf("invalid Mureka input: %w", err)
	}
	if input.Prompt == "" {
		return MusicRequest{}, errors.New("music.generate requires a prompt")
	}
	if input.DurationSeconds < 0 {
		return MusicRequest{}, errors.New("durationSeconds must not be negative")
	}
	return MusicRequest{
		Provider:        "mureka",
		Prompt:          input.Prompt,
		Lyrics:          input.Lyrics,
		Model:           input.Model,
		Instrumental:    input.Instrumental,
		DurationSeconds: input.DurationSeconds,
	}, nil
}

func (murekaJobProvider) Validate(job CreativeJob) error {
	_, err := decodeMurekaJob(job)
	return err
}

func (murekaJobProvider) Submit(ctx context.Context, job CreativeJob) (string, error) {
	input, err := decodeMurekaJob(job)
	if err != nil {
		return "", err
	}
	result, err := (MusicGeneration{}).Generate(ctx, input)
	if err != nil {
		return "", err
	}
	if result.ProviderTaskID == "" {
		return "", errors.New("Mureka did not return a provider task ID; manual reconciliation is required")
	}
	return result.ProviderTaskID, nil
}

func (murekaJobProvider) Poll(ctx context.Context, job CreativeJob, taskID string) (ProviderProgress, error) {
	input, err := decodeMurekaJob(job)
	if err != nil {
		return ProviderProgress{}, err
	}
	result, err := (MusicGeneration{}).GetMurekaTask(ctx, MusicTaskInput{
		TaskID: taskID, Instrumental: input.Instrumental,
	})
	if err != nil {
		return ProviderProgress{}, err
	}
	return ProviderProgress{
		State: result.Status, AudioURL: result.AudioURL, Message: result.Message,
	}, nil
}
