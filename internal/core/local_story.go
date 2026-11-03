package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// StartLocalStory claims a non-billable story.build job. Unlike a paid POST,
// a local build can be retried after failure without spending money.
// It returns false when this exact job already has a durable StoryPack.
func (r *JobRuntime) StartLocalStory(ctx context.Context, id string) (bool, JobExecution, error) {
	result, err := r.Status(ctx, id)
	if err != nil {
		return false, JobExecution{}, err
	}
	job := result.Job
	if job.Kind != "story.build" || job.Provider != "local" ||
		job.ApprovalStatus != "not_required" {
		return false, result, errors.New("job is not a non-billable local story build")
	}
	outputName := localStoryOutputName(job.ID)
	for _, artifact := range result.Artifacts {
		if artifact.Name == outputName && artifact.Provider == "local" &&
			artifact.Kind == "story" {
			// Recover when output was imported but process stopped before
			// marking the job succeeded. Do not build a duplicate.
			if job.Status != "succeeded" {
				if err := r.setJobStatus(ctx, id, "succeeded", ""); err != nil {
					return false, JobExecution{}, err
				}
				result, err = r.Status(ctx, id)
			}
			return false, result, err
		}
	}
	if job.Status == "succeeded" {
		return false, result, errors.New("completed local job has no archived StoryPack")
	}
	now := time.Now().UnixMilli()
	allowed := job.Status == "draft" || job.Status == "failed"
	if job.Status == "processing" && job.UpdatedAt < now-5*60*1000 {
		allowed = true // a previous local build stopped without a result
	}
	if !allowed {
		return false, result, fmt.Errorf("local job is %s; try again after it finishes", job.Status)
	}

	changed, err := r.workspace.projects.db.ExecContext(ctx,
		`UPDATE creative_jobs SET status='processing',error='',updated_at=?
		 WHERE id=? AND (status IN ('draft','failed') OR
		                (status='processing' AND updated_at<?))`,
		now, id, now-5*60*1000)
	if err != nil {
		return false, JobExecution{}, err
	}
	affected, err := changed.RowsAffected()
	if err != nil {
		return false, JobExecution{}, err
	}
	if affected != 1 {
		return false, JobExecution{}, errors.New("another process started this local job")
	}
	result, err = r.Status(ctx, id)
	return true, result, err
}

func localStoryOutputName(id string) string {
	return "storypack-" + id[:min(len(id), 12)] + ".json"
}

func localStorySourceName(id string) string {
	return "source-" + id[:min(len(id), 12)] + ".json"
}

// StoreLocalStorySource preserves the exact input snapshot so the original
// file can be moved or changed without losing how the StoryPack was built.
func (r *JobRuntime) StoreLocalStorySource(ctx context.Context, id, sourcePath string) error {
	result, err := r.Status(ctx, id)
	if err != nil {
		return err
	}
	if result.Job.Kind != "story.build" || result.Job.Provider != "local" ||
		result.Job.Status != "processing" {
		return errors.New("local story input requires a processing story.build job")
	}
	name := localStorySourceName(id)
	for _, artifact := range result.Artifacts {
		if artifact.Name == name && artifact.Provider == "local" {
			return nil
		}
	}
	_, err = r.workspace.AddArtifact(ctx, NewCreativeArtifact{
		ProjectID:   result.Job.ProjectID,
		JobID:       id,
		SourcePath:  sourcePath,
		Name:        name,
		Kind:        "document",
		Provider:    "local",
		LicenseNote: "User-supplied story source; rights not verified",
	})
	return err
}

// CompleteLocalStory copies the built StoryPack through the existing
// Artifact Store and updates the same job status used by MyGo and the CLI.
func (r *JobRuntime) CompleteLocalStory(ctx context.Context, id, outputPath string) (JobExecution, error) {
	result, err := r.Status(ctx, id)
	if err != nil {
		return JobExecution{}, err
	}
	if result.Job.Kind != "story.build" || result.Job.Provider != "local" ||
		result.Job.Status != "processing" {
		return JobExecution{}, errors.New("local StoryPack output requires a processing story.build job")
	}
	_, err = r.workspace.AddArtifact(ctx, NewCreativeArtifact{
		ProjectID:   result.Job.ProjectID,
		JobID:       id,
		SourcePath:  outputPath,
		Name:        localStoryOutputName(id),
		Kind:        "story",
		Provider:    "local",
		LicenseNote: "User-supplied content; commercial rights not verified",
	})
	if err != nil {
		return JobExecution{}, err
	}
	if err := r.setJobStatus(ctx, id, "succeeded", ""); err != nil {
		// A subsequent call to StartLocalStory will detect the durable
		// output artifact and finalize instead of rebuilding.
		return JobExecution{}, err
	}
	return r.Status(ctx, id)
}

// FailLocalStory records a failed local build without discarding any
// previously imported source. It does not expose story text in Job.error.
func (r *JobRuntime) FailLocalStory(ctx context.Context, id string) error {
	_, err := r.workspace.projects.db.ExecContext(ctx,
		`UPDATE creative_jobs SET status='failed',error=?,updated_at=?
		 WHERE id=? AND status='processing'`,
		strings.TrimSpace("StoryPack build failed; inspect the CLI error and retry locally"),
		time.Now().UnixMilli(), id)
	return err
}
