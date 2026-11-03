package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sogud/Muse/internal/core"
)

// storyPackArtifactID returns the stored output rather than a temporary
// Bun build path, which is deleted as soon as the command completes.
func storyPackArtifactID(artifacts []core.CreativeArtifact) string {
	for _, artifact := range artifacts {
		if artifact.Kind == "story" && artifact.Provider == "local" {
			return artifact.ID
		}
	}
	return ""
}

// buildProjectStoryPack takes an existing StoryPack input, builds it locally,
// and saves both the input snapshot and final StoryPack under one creative
// project. The idempotency key is derived from the exact input bytes, so an
// Agent retry cannot duplicate the same generated artifact.
func buildProjectStoryPack(ctx context.Context, projectID, inputPath string) (any, error) {
	info, err := os.Lstat(inputPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 10<<20 {
		return nil, fail("INVALID_ARGUMENT", "story input must be a regular JSON file at most 10 MiB")
	}
	source, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	if len(source) == 0 || len(source) > 10<<20 {
		return nil, fail("INVALID_ARGUMENT", "story input must contain 1 byte to 10 MiB")
	}
	hash := sha256.Sum256(source)
	sourceHash := hex.EncodeToString(hash[:])

	// The Bun subprocess reads this private snapshot, not a path that
	// another process might modify while this command is running.
	stagedInput, err := os.CreateTemp("", "muse-story-input-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(stagedInput.Name())
	if _, err := stagedInput.Write(source); err != nil {
		stagedInput.Close()
		return nil, err
	}
	if err := stagedInput.Close(); err != nil {
		return nil, err
	}

	validation, err := runStoryBridge(ctx, "validate", stagedInput.Name(), "")
	if err != nil {
		return nil, err
	}
	diagnostics, ok := validation.(map[string]any)
	if !ok || diagnostics["valid"] != true {
		return nil, fail("STORY_ERROR", "story input failed validation; run story validate to inspect diagnostics")
	}

	return withStore(ctx, func(store *core.ProjectStore) (any, error) {
		workspace := core.NewCreativeWorkspace(store)
		// The project and the saved source are never rewritten. A repeated
		// request with the same content retrieves the same local job.
		requiresApproval := false
		jobInput, err := json.Marshal(map[string]string{
			"sourceSha256": sourceHash,
			"engine":       "storypack/v1",
		})
		if err != nil {
			return nil, err
		}
		job, err := workspace.CreateJob(ctx, core.NewCreativeJob{
			ProjectID:        projectID,
			Kind:             "story.build",
			Provider:         "local",
			Input:            jobInput,
			RequiresApproval: &requiresApproval,
			IdempotencyKey:   "storypack:v1:" + sourceHash,
		})
		if err != nil {
			return nil, err
		}
		runtime := core.NewJobRuntime(workspace)
		shouldBuild, execution, err := runtime.StartLocalStory(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		if !shouldBuild {
			manifestPath, err := workspace.ExportManifest(ctx, projectID)
			if err != nil {
				return nil, fmt.Errorf("existing StoryPack is archived but manifest export failed: %w", err)
			}
			return map[string]any{
				"cached":              true,
				"networkCalled":       false,
				"job":                 execution.Job,
				"artifacts":           execution.Artifacts,
				"storyPackArtifactId": storyPackArtifactID(execution.Artifacts),
				"manifestPath":        manifestPath,
			}, nil
		}

		if err := runtime.StoreLocalStorySource(ctx, job.ID, stagedInput.Name()); err != nil {
			_ = runtime.FailLocalStory(context.Background(), job.ID)
			return nil, err
		}
		buildDir, err := os.MkdirTemp("", "muse-story-output-*")
		if err != nil {
			_ = runtime.FailLocalStory(context.Background(), job.ID)
			return nil, err
		}
		defer os.RemoveAll(buildDir)
		outputPath := filepath.Join(buildDir, "storypack.json")
		built, err := runStoryBridge(ctx, "build", stagedInput.Name(), outputPath)
		if err != nil {
			_ = runtime.FailLocalStory(context.Background(), job.ID)
			return nil, err
		}
		execution, err = runtime.CompleteLocalStory(ctx, job.ID, outputPath)
		if err != nil {
			_ = runtime.FailLocalStory(context.Background(), job.ID)
			return nil, errors.New("StoryPack was built, but local archiving did not complete; check job status before retrying")
		}
		manifestPath, err := workspace.ExportManifest(ctx, projectID)
		if err != nil {
			return nil, fmt.Errorf("StoryPack archived, but manifest export failed; run workspace export: %w", err)
		}
		if summary, ok := built.(map[string]any); ok {
			delete(summary, "outputFile") // the Bun staging path no longer exists
		}
		return map[string]any{
			"cached":              false,
			"networkCalled":       false,
			"job":                 execution.Job,
			"artifacts":           execution.Artifacts,
			"storyPackArtifactId": storyPackArtifactID(execution.Artifacts),
			"build":               built,
			"manifestPath":        manifestPath,
		}, nil
	})
}
