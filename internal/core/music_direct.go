package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// DirectMusicProvider returns an MP3 in the same paid request. Unlike Mureka,
// it has no asynchronous task to poll. JobRuntime writes the result to a
// durable local stage before adding it to the existing Artifact Store.
type DirectMusicProvider interface {
	JobProvider
	GenerateAudio(context.Context, CreativeJob, io.Writer) error
}

const maxDirectMusicBytes int64 = 64 << 20

var directMusicHTTP = &http.Client{
	Timeout: 10 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("music provider HTTP redirects are not allowed")
	},
}

type directMusicInput struct {
	Prompt          string `json:"prompt"`
	Lyrics          string `json:"lyrics"`
	Model           string `json:"model"`
	Instrumental    bool   `json:"instrumental"`
	DurationSeconds int    `json:"durationSeconds"`
}

func decodeDirectMusic(job CreativeJob) (directMusicInput, error) {
	var input directMusicInput
	dec := json.NewDecoder(bytes.NewReader(job.Input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, errors.New("invalid direct music input")
	}
	if strings.TrimSpace(input.Prompt) == "" || utf8.RuneCountInString(input.Prompt) > 3600 ||
		utf8.RuneCountInString(input.Lyrics) > 3500 {
		return input, errors.New("music prompt must be 1–3600 characters, lyrics at most 3500")
	}
	if input.DurationSeconds < 0 || input.DurationSeconds > 600 {
		return input, errors.New("music duration must be 0–600 seconds")
	}
	return input, nil
}

func inputWithLyrics(input directMusicInput) string {
	prompt := strings.TrimSpace(input.Prompt)
	if input.Instrumental {
		return prompt + "\nInstrumental only, no vocals or lyrics."
	}
	if strings.TrimSpace(input.Lyrics) != "" {
		prompt += "\n\nLyrics:\n" + strings.TrimSpace(input.Lyrics)
	}
	return prompt
}

func writeMusicAudio(out io.Writer, data io.Reader) error {
	n, err := io.Copy(out, io.LimitReader(data, maxDirectMusicBytes+1))
	if err != nil {
		return fmt.Errorf("music audio streaming failed: %w", err)
	}
	if n < 1024 || n > maxDirectMusicBytes {
		return errors.New("music audio is empty, too small or over 64 MiB")
	}
	return nil
}

type elevenMusicProvider struct{}

func (elevenMusicProvider) Ready() error {
	if strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY")) == "" {
		return errors.New("ELEVENLABS_API_KEY is not configured")
	}
	return nil
}

func (elevenMusicProvider) Validate(job CreativeJob) error {
	input, err := decodeDirectMusic(job)
	if err != nil {
		return err
	}
	switch input.Model {
	case "", "music_v1", "music_v2", "music_v2_5":
	default:
		return errors.New("ElevenLabs model must be music_v1, music_v2 or music_v2_5")
	}
	if len(inputWithLyrics(input)) > 4100 {
		return errors.New("ElevenLabs combined music prompt and lyrics exceed 4100 characters")
	}
	if input.DurationSeconds != 0 && input.DurationSeconds < 3 {
		return errors.New("ElevenLabs duration must be between 3 and 600 seconds")
	}
	if input.Instrumental && strings.TrimSpace(input.Lyrics) != "" {
		return errors.New("instrumental mode cannot include lyrics")
	}
	return nil
}

func (p elevenMusicProvider) GenerateAudio(ctx context.Context, job CreativeJob, out io.Writer) error {
	if err := p.Validate(job); err != nil {
		return err
	}
	key := strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
	if key == "" {
		return errors.New("ELEVENLABS_API_KEY is not configured")
	}
	input, _ := decodeDirectMusic(job)
	model := input.Model
	if model == "" {
		model = "music_v2_5"
	}
	payload := map[string]any{
		"prompt": inputWithLyrics(input), "model_id": model,
		"force_instrumental": input.Instrumental,
	}
	if input.DurationSeconds > 0 {
		payload["music_length_ms"] = input.DurationSeconds * 1000
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.elevenlabs.io/v1/music?output_format=auto", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	res, err := directMusicHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ElevenLabs request outcome uncertain: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("ElevenLabs returned HTTP %d; verify billing before attempting another generation", res.StatusCode)
	}
	typ := strings.ToLower(strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0]))
	if typ != "" && typ != "application/octet-stream" && !strings.HasPrefix(typ, "audio/") {
		return errors.New("ElevenLabs did not return audio")
	}
	return writeMusicAudio(out, res.Body)
}

type lyriaMusicProvider struct{}

func (lyriaMusicProvider) Ready() error {
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) == "" {
		return errors.New("GEMINI_API_KEY is not configured")
	}
	return nil
}

func (lyriaMusicProvider) Validate(job CreativeJob) error {
	input, err := decodeDirectMusic(job)
	if err != nil {
		return err
	}
	if input.Instrumental && strings.TrimSpace(input.Lyrics) != "" {
		return errors.New("instrumental mode cannot include lyrics")
	}
	switch input.Model {
	case "", "lyria-3-clip-preview", "lyria-3.5":
	default:
		return errors.New("Lyria model must be lyria-3-clip-preview or lyria-3.5")
	}
	if input.Model != "lyria-3.5" && input.DurationSeconds != 0 && input.DurationSeconds != 30 {
		return errors.New("Lyria Clip produces 30 seconds; select lyria-3.5 for full-length tracks")
	}
	return nil
}

func (p lyriaMusicProvider) GenerateAudio(ctx context.Context, job CreativeJob, out io.Writer) error {
	if err := p.Validate(job); err != nil {
		return err
	}
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		return errors.New("GEMINI_API_KEY is not configured")
	}
	input, _ := decodeDirectMusic(job)
	model := input.Model
	if model == "" {
		model = "lyria-3-clip-preview"
	}
	prompt := inputWithLyrics(input)
	if model == "lyria-3.5" && input.DurationSeconds > 0 {
		prompt += fmt.Sprintf("\nTarget duration: approximately %d seconds.", input.DurationSeconds)
	}
	body, err := json.Marshal(map[string]any{"model": model, "input": prompt})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://generativelanguage.googleapis.com/v1beta/interactions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	res, err := directMusicHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Google Lyria request outcome uncertain: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Google Lyria returned HTTP %d; verify billing before attempting another generation", res.StatusCode)
	}
	// The Interactions REST API returns model_output content blocks. Music
	// audio is base64 data, not a download URL or an async Mureka task ID.
	var interaction struct {
		Status string `json:"status"`
		Steps  []struct {
			Type    string `json:"type"`
			Content []struct {
				Type     string `json:"type"`
				Data     string `json:"data"`
				MimeType string `json:"mime_type"`
			} `json:"content"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2*maxDirectMusicBytes+1)).
		Decode(&interaction); err != nil {
		return errors.New("Google Lyria returned invalid or oversized JSON")
	}
	if interaction.Status != "completed" {
		return fmt.Errorf("Google Lyria interaction is %s, no audio archived; check the provider before retrying", interaction.Status)
	}
	for _, step := range interaction.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, content := range step.Content {
			if content.Type != "audio" || content.Data == "" {
				continue
			}
			if content.MimeType != "" && content.MimeType != "audio/mp3" && content.MimeType != "audio/mpeg" {
				return errors.New("Lyria output is not an MP3")
			}
			if len(content.Data) > int(2*maxDirectMusicBytes) {
				return errors.New("Lyria audio exceeds size limit")
			}
			return writeMusicAudio(out, base64.NewDecoder(base64.StdEncoding, strings.NewReader(content.Data)))
		}
	}
	return errors.New("Google Lyria completed without an audio content block")
}

// submitDirect keeps a complete audio response on disk before archiving.
// A lost response never causes another paid request to be sent.
func (r *JobRuntime) submitDirect(ctx context.Context, job CreativeJob, provider DirectMusicProvider) (JobExecution, error) {
	projectDir, err := r.workspace.projectFolder(job.ProjectID)
	if err != nil {
		return JobExecution{}, err
	}
	partialPath := filepath.Join(projectDir, ".generated-"+job.ID+".mp3.partial")
	finalPath := filepath.Join(projectDir, ".generated-"+job.ID+".mp3")
	// A former attempt may have charged the user. Do not overwrite or repeat.
	if _, err := os.Lstat(finalPath); err == nil {
		_ = r.setJobStatus(context.Background(), job.ID, "awaiting_archive", "existing generated audio must be archived")
		return r.finishDirect(ctx, job)
	}
	file, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = r.setJobStatus(context.Background(), job.ID, "needs_reconciliation", "previous direct music request may exist; check provider before retrying")
		return JobExecution{}, fmt.Errorf("direct music staging unavailable; check previous request before retrying: %w", err)
	}
	callErr := provider.GenerateAudio(ctx, job, file)
	closeErr := file.Close()
	if callErr != nil || closeErr != nil {
		// The remote service might have billed even if the response was lost.
		// Incomplete audio is never treated as a finished result.
		_ = r.setJobStatus(context.Background(), job.ID, "needs_reconciliation", "direct music request outcome uncertain; inspect provider account before retrying")
		if callErr != nil {
			return JobExecution{}, fmt.Errorf("paid music response is not confirmed; do not resubmit automatically: %w", callErr)
		}
		return JobExecution{}, fmt.Errorf("paid music received but local file write failed; inspect staging before retrying: %w", closeErr)
	}
	if err := os.Rename(partialPath, finalPath); err != nil {
		_ = r.setJobStatus(context.Background(), job.ID, "needs_reconciliation", "completed audio could not be staged; check local files before retrying")
		return JobExecution{}, err
	}
	if err := r.setJobStatus(ctx, job.ID, "awaiting_archive", ""); err != nil {
		return JobExecution{}, fmt.Errorf("audio is stored locally but job status update failed; run job sync, never submit again: %w", err)
	}
	return r.finishDirect(ctx, job)
}

// finishDirect only imports previously completed local MP3 data; it does
// not contact ElevenLabs or Google. Safe for interrupted local archiving.
func (r *JobRuntime) finishDirect(ctx context.Context, job CreativeJob) (JobExecution, error) {
	current, err := r.Status(ctx, job.ID)
	if err != nil {
		return JobExecution{}, err
	}
	name := "generated-" + job.ID[:min(12, len(job.ID))] + ".mp3"
	for _, artifact := range current.Artifacts {
		if artifact.Provider == job.Provider && artifact.Kind == "audio" &&
			artifact.Name == name {
			if err := r.setJobStatus(ctx, job.ID, "succeeded", ""); err != nil {
				return JobExecution{}, err
			}
			return r.Status(ctx, job.ID)
		}
	}
	projectDir, err := r.workspace.projectFolder(job.ProjectID)
	if err != nil {
		return JobExecution{}, err
	}
	stage := filepath.Join(projectDir, ".generated-"+job.ID+".mp3")
	info, err := os.Lstat(stage)
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() < 1024 || info.Size() > maxDirectMusicBytes {
		return current, errors.New("no completed local audio available; inspect provider billing and staging before any retry")
	}
	_, err = r.workspace.AddArtifact(ctx, NewCreativeArtifact{
		ProjectID: job.ProjectID, JobID: job.ID, SourcePath: stage, Name: name,
		Kind: "audio", Provider: job.Provider,
		LicenseNote: "Provider license and commercial-use rights not verified",
	})
	if err != nil {
		_ = r.setJobStatus(context.Background(), job.ID, "awaiting_archive", "finished audio still staged locally; use job sync to retry import")
		return current, fmt.Errorf("audio received, but local archive failed; run job sync: %w", err)
	}
	if err := r.setJobStatus(ctx, job.ID, "succeeded", ""); err != nil {
		return JobExecution{}, err
	}
	_ = os.Remove(stage)
	return r.Status(ctx, job.ID)
}
