package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JobRuntime is shared by the CLI and MyGo. Jobs are run only by explicit
// method calls: there is no invisible worker or startup API request.
type JobRuntime struct {
	workspace *CreativeWorkspace
}

func NewJobRuntime(workspace *CreativeWorkspace) *JobRuntime {
	return &JobRuntime{workspace: workspace}
}

// ProviderReceipt is the durable record needed to resume an asynchronous API
// task. No expiring media URL or API key is stored in this table.
type ProviderReceipt struct {
	JobID            string `json:"jobId"`
	ProviderTaskID   string `json:"providerTaskId"`
	ProviderState    string `json:"providerState"`
	LastCheckedAt    int64  `json:"lastCheckedAt"`
	LastError        string `json:"lastError,omitempty"`
	ActualCostMicros *int64 `json:"actualCostMicros,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
}

type JobExecution struct {
	Job          CreativeJob        `json:"job"`
	Receipt      *ProviderReceipt   `json:"receipt,omitempty"`
	Artifacts    []CreativeArtifact `json:"artifacts"`
	CostVerified bool               `json:"costVerified"`
}

func (r *JobRuntime) receipt(ctx context.Context, id string) (*ProviderReceipt, error) {
	var result ProviderReceipt
	err := r.workspace.projects.db.QueryRowContext(ctx, `SELECT job_id, provider_task_id,
		provider_state, last_checked_at, last_error, actual_cost_micros,
		created_at, updated_at FROM creative_job_receipts WHERE job_id=?`, id).
		Scan(&result.JobID, &result.ProviderTaskID, &result.ProviderState,
			&result.LastCheckedAt, &result.LastError, &result.ActualCostMicros,
			&result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *JobRuntime) Status(ctx context.Context, id string) (JobExecution, error) {
	job, err := r.workspace.GetJob(ctx, id)
	if err != nil {
		return JobExecution{}, err
	}
	if job == nil {
		return JobExecution{}, errors.New("job not found")
	}
	receipt, err := r.receipt(ctx, id)
	if err != nil {
		return JobExecution{}, err
	}
	all, err := r.workspace.ListArtifacts(ctx, job.ProjectID)
	if err != nil {
		return JobExecution{}, err
	}
	artifacts := []CreativeArtifact{}
	for _, artifact := range all {
		if artifact.JobID == id {
			artifacts = append(artifacts, artifact)
		}
	}
	// Billing is unknown until there is a provider-confirmed cost record.
	return JobExecution{Job: *job, Receipt: receipt, Artifacts: artifacts,
		CostVerified: receipt != nil && receipt.ActualCostMicros != nil}, nil
}

func (r *JobRuntime) setJobStatus(ctx context.Context, id, status, message string) error {
	_, err := r.workspace.projects.db.ExecContext(ctx,
		"UPDATE creative_jobs SET status=?,error=?,updated_at=? WHERE id=?",
		status, truncateJobError(message), time.Now().UnixMilli(), id)
	return err
}

func truncateJobError(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > 400 {
		return string(runes[:400])
	}
	return string(runes)
}

func (r *JobRuntime) updateReceipt(ctx context.Context, id, state, message string) error {
	now := time.Now().UnixMilli()
	_, err := r.workspace.projects.db.ExecContext(ctx,
		"UPDATE creative_job_receipts SET provider_state=?,last_error=?,last_checked_at=?,updated_at=? WHERE job_id=?",
		state, truncateJobError(message), now, now, id)
	return err
}

// Submit makes exactly one attempt at a paid provider request. Approval is a
// durable human decision; allowPaid is a separate command-time authorization.
// A state transition to submitting is committed BEFORE any network request.
// If the response is lost, this method never resends it automatically.
func (r *JobRuntime) Submit(ctx context.Context, id string, allowPaid bool) (JobExecution, error) {
	if !allowPaid {
		return JobExecution{}, errors.New("paid submission requires --allow-paid at execution time")
	}
	job, err := r.workspace.GetJob(ctx, id)
	if err != nil {
		return JobExecution{}, err
	}
	if job == nil {
		return JobExecution{}, errors.New("job not found")
	}
	if job.Status != "draft" {
		return JobExecution{}, fmt.Errorf("job is %s; paid submission is allowed only once from draft", job.Status)
	}
	if job.ApprovalStatus != "approved" {
		return JobExecution{}, errors.New("job must be approved before submission")
	}
	// These values are supplied by the user; the platform does not report a
	// reliable quote or charged amount. Check the declared estimate, but
	// never present it as a guaranteed provider spending limit.
	if job.EstimatedCostMicros == nil || job.MaxCostMicros == nil ||
		*job.EstimatedCostMicros <= 0 || *job.MaxCostMicros <= 0 ||
		*job.EstimatedCostMicros > *job.MaxCostMicros {
		return JobExecution{}, errors.New("positive estimated cost and maximum budget are required; estimate must be within the budget")
	}
	if job.Currency != "USD" {
		return JobExecution{}, errors.New("paid music providers currently require a user-entered USD budget estimate")
	}
	provider, err := providerForJob(*job)
	if err != nil {
		return JobExecution{}, err
	}
	if err := provider.Validate(*job); err != nil {
		return JobExecution{}, err
	}
	if err := provider.Ready(); err != nil {
		return JobExecution{}, err
	}

	now := time.Now().UnixMilli()
	claimed, err := r.workspace.projects.db.ExecContext(ctx,
		"UPDATE creative_jobs SET status='submitting',updated_at=?,error='' WHERE id=? AND status='draft' AND approval_status='approved'",
		now, id)
	if err != nil {
		return JobExecution{}, err
	}
	affected, err := claimed.RowsAffected()
	if err != nil {
		return JobExecution{}, err
	}
	if affected != 1 {
		return JobExecution{}, errors.New("another process already claimed this job; inspect job status before retrying")
	}

	if direct, ok := provider.(DirectMusicProvider); ok {
		return r.submitDirect(ctx, *job, direct)
	}
	async, ok := provider.(AsyncJobProvider)
	if !ok {
		_ = r.setJobStatus(context.Background(), id, "needs_reconciliation", "provider runtime is not supported")
		return JobExecution{}, errors.New("unsupported provider execution mode")
	}

	taskID, err := async.Submit(ctx, *job)
	if err != nil {
		// We cannot know whether a failed HTTP call was accepted remotely.
		// A person must locate and attach the real task ID, if one exists.
		_ = r.setJobStatus(context.Background(), id, "needs_reconciliation",
			"submission response was not confirmed; check the provider dashboard before attaching a task ID")
		return JobExecution{}, fmt.Errorf("submission outcome uncertain; do not retry the paid POST: %w", err)
	}
	if err := validateProviderTaskID(taskID); err != nil {
		_ = r.setJobStatus(context.Background(), id, "needs_reconciliation",
			"provider returned an invalid task ID; check dashboard before recovery")
		return JobExecution{}, errors.New("provider task ID was invalid; manual reconciliation required")
	}

	tx, err := r.workspace.projects.db.BeginTx(ctx, nil)
	if err != nil {
		return JobExecution{}, fmt.Errorf("remote job submitted but receipt transaction failed; attach task ID %s manually: %w", taskID, err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO creative_job_receipts
		(job_id,provider_task_id,provider_state,last_checked_at,last_error,created_at,updated_at)
		VALUES(?,?,?,0,'',?,?)`, id, taskID, "submitted", now, now)
	if err != nil {
		return JobExecution{}, fmt.Errorf("remote job submitted but receipt saving failed; attach task ID %s manually: %w", taskID, err)
	}
	_, err = tx.ExecContext(ctx,
		"UPDATE creative_jobs SET status='submitted',updated_at=? WHERE id=? AND status='submitting'", now, id)
	if err != nil {
		return JobExecution{}, fmt.Errorf("remote job submitted but job saving failed; attach task ID %s manually: %w", taskID, err)
	}
	if err = tx.Commit(); err != nil {
		return JobExecution{}, fmt.Errorf("remote job submitted but database commit failed; attach task ID %s manually: %w", taskID, err)
	}
	return r.Status(ctx, id)
}

func validateProviderTaskID(id string) error {
	if len(id) == 0 || len(id) > 128 {
		return errors.New("invalid provider task ID")
	}
	for _, ch := range id {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') || ch == '-' || ch == '_') {
			return errors.New("invalid provider task ID")
		}
	}
	return nil
}

// Attach recovers a submission whose HTTP response or receipt commit was lost.
// It records an existing provider ID; it does NOT create another paid task.
func (r *JobRuntime) Attach(ctx context.Context, id, providerTaskID string) (JobExecution, error) {
	if err := validateProviderTaskID(providerTaskID); err != nil {
		return JobExecution{}, err
	}
	job, err := r.workspace.GetJob(ctx, id)
	if err != nil {
		return JobExecution{}, err
	}
	if job == nil {
		return JobExecution{}, errors.New("job not found")
	}
	if job.Status != "submitting" && job.Status != "needs_reconciliation" {
		return JobExecution{}, errors.New("only uncertain submissions may attach a provider task ID")
	}
	if job.ApprovalStatus != "approved" {
		return JobExecution{}, errors.New("job was not approved")
	}
	provider, err := providerForJob(*job)
	if err != nil {
		return JobExecution{}, err
	}
	if _, ok := provider.(AsyncJobProvider); !ok {
		return JobExecution{}, errors.New("this provider returns music directly; attach is not supported; inspect local artifact staging")
	}
	now := time.Now().UnixMilli()
	tx, err := r.workspace.projects.db.BeginTx(ctx, nil)
	if err != nil {
		return JobExecution{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO creative_job_receipts
		(job_id,provider_task_id,provider_state,last_checked_at,last_error,created_at,updated_at)
		VALUES(?,?,?,0,'',?,?)`, id, providerTaskID, "attached", now, now)
	if err != nil {
		return JobExecution{}, fmt.Errorf("provider task already recorded or storage failed: %w", err)
	}
	changed, err := tx.ExecContext(ctx,
		"UPDATE creative_jobs SET status='submitted',error='',updated_at=? WHERE id=? AND status IN ('submitting','needs_reconciliation')", now, id)
	if err != nil {
		return JobExecution{}, err
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return JobExecution{}, errors.New("job status changed during recovery")
	}
	if err := tx.Commit(); err != nil {
		return JobExecution{}, err
	}
	return r.Status(ctx, id)
}

// Sync never submits a paid POST. It polls an already-recorded task, and on
// success imports the audio as an ordinary artifact using the same storage
// code MyGo and the CLI use.
func (r *JobRuntime) Sync(ctx context.Context, id string) (JobExecution, error) {
	current, err := r.Status(ctx, id)
	if err != nil {
		return JobExecution{}, err
	}
	job := current.Job
	provider, err := providerForJob(job)
	if err != nil {
		return current, err
	}
	if _, ok := provider.(DirectMusicProvider); ok {
		switch job.Status {
		case "succeeded", "failed", "cancelled":
			return current, nil
		case "submitting", "needs_reconciliation", "awaiting_archive", "archiving":
			// Only archive locally staged audio; NEVER repeat the paid POST.
			return r.finishDirect(ctx, job)
		default:
			return current, errors.New("direct music job has not been submitted")
		}
	}
	async, ok := provider.(AsyncJobProvider)
	if !ok {
		return current, errors.New("unsupported provider query mode")
	}
	switch job.Status {
	case "succeeded", "failed", "cancelled":
		return current, nil
	case "submitted", "processing", "awaiting_archive", "archiving":
		// Resume is allowed; successful paid submission is never repeated.
	case "submitting", "needs_reconciliation":
		return current, errors.New("submission outcome is unknown; inspect provider dashboard and use job attach, never resubmit")
	default:
		return current, errors.New("job has not been submitted")
	}
	if current.Receipt == nil || current.Receipt.ProviderTaskID == "" {
		return current, errors.New("provider receipt is missing; manual reconciliation required")
	}
	// If the process crashed after file import but before changing job
	// status, finish from the already-durable artifact instead of downloading
	// the same remote audio again.
	for _, artifact := range current.Artifacts {
		// A user might link an unrelated reference file to the job.
		// Only our generated-audio import can establish completion.
		expectedPrefix := "generated-" + job.ID[:min(12, len(job.ID))]
		if artifact.Provider != job.Provider || artifact.Kind != "audio" ||
			!strings.HasPrefix(artifact.Name, expectedPrefix+".") {
			continue
		}
		if err := r.setJobStatus(ctx, id, "succeeded", ""); err != nil {
			return JobExecution{}, err
		}
		return r.Status(ctx, id)
	}

	if err := provider.Ready(); err != nil {
		return current, err
	}
	progress, err := async.Poll(ctx, job, current.Receipt.ProviderTaskID)
	if err != nil {
		// A temporary query failure should not turn the paid task into a
		// failed or re-submittable job.
		_ = r.updateReceipt(context.Background(), id, "query_error", "provider status request failed; retry sync later")
		return current, err
	}
	switch progress.State {
	case "failed":
		if err := r.updateReceipt(ctx, id, "failed", progress.Message); err != nil {
			return JobExecution{}, err
		}
		if err := r.setJobStatus(ctx, id, "failed", "provider task failed; check provider dashboard"); err != nil {
			return JobExecution{}, err
		}
		return r.Status(ctx, id)
	case "succeeded":
		if progress.AudioURL == "" {
			_ = r.updateReceipt(ctx, id, "succeeded", "provider returned no download URL")
			_ = r.setJobStatus(ctx, id, "awaiting_archive", "provider succeeded, but media URL was not available")
			return r.Status(ctx, id)
		}
	default:
		// The Mureka adapter maps preparing/running/streaming to submitted,
		// and records the more detailed remote status when available.
		if err := r.updateReceipt(ctx, id, progress.State, ""); err != nil {
			return JobExecution{}, err
		}
		if err := r.setJobStatus(ctx, id, "processing", ""); err != nil {
			return JobExecution{}, err
		}
		return r.Status(ctx, id)
	}

	// At most one process archives a given successful task at once. A stale
	// claim can be recovered after 5 minutes without another provider POST.
	now := time.Now().UnixMilli()
	claimed, err := r.workspace.projects.db.ExecContext(ctx, `UPDATE creative_jobs
		SET status='archiving',updated_at=?,error=''
		WHERE id=? AND (
		  status IN ('submitted','processing','awaiting_archive') OR
		  (status='archiving' AND updated_at<?)
		)`, now, id, now-5*60*1000)
	if err != nil {
		return JobExecution{}, err
	}
	n, err := claimed.RowsAffected()
	if err != nil {
		return JobExecution{}, err
	}
	if n != 1 {
		return r.Status(ctx, id)
	}
	artifact, err := r.archiveAudio(ctx, job, progress.AudioURL)
	if err != nil {
		_ = r.updateReceipt(context.Background(), id, "succeeded", "local archive failed; retry sync later")
		_ = r.setJobStatus(context.Background(), id, "awaiting_archive", "generated audio not yet archived")
		return r.Status(ctx, id)
	}
	_ = artifact
	if err := r.updateReceipt(ctx, id, "succeeded", ""); err != nil {
		return JobExecution{}, err
	}
	if err := r.setJobStatus(ctx, id, "succeeded", ""); err != nil {
		return JobExecution{}, err
	}
	return r.Status(ctx, id)
}

func allowedMurekaMediaURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return nil, errors.New("provider media URL must use HTTPS without credentials or custom ports")
	}
	host := strings.ToLower(parsed.Hostname())
	// Limit remote downloads to domains controlled by the provider. Reject
	// arbitrary media URLs so a model response cannot request internal URLs.
	if host != "cdn.mureka.ai" && host != "cdn.mureka.cn" &&
		!strings.HasSuffix(host, ".mureka.ai") &&
		!strings.HasSuffix(host, ".mureka.cn") {
		return nil, errors.New("media host is not a trusted Mureka CDN domain")
	}
	return parsed, nil
}

func (r *JobRuntime) archiveAudio(ctx context.Context, job CreativeJob, audioURL string) (CreativeArtifact, error) {
	parsed, err := allowedMurekaMediaURL(audioURL)
	if err != nil {
		return CreativeArtifact{}, err
	}
	extension := strings.ToLower(filepath.Ext(parsed.Path))
	switch extension {
	case ".mp3", ".wav", ".flac", ".ogg", ".m4a":
	default:
		return CreativeArtifact{}, errors.New("provider media URL has unsupported audio file type")
	}
	dir, err := r.workspace.projectFolder(job.ProjectID)
	if err != nil {
		return CreativeArtifact{}, err
	}
	// This temp is app-owned; the existing AddArtifact path handles copies,
	// SHA-256, metadata and durable SQLite records.
	temp, err := os.CreateTemp(dir, ".audio-download-*"+extension)
	if err != nil {
		return CreativeArtifact{}, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	defer temp.Close()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return CreativeArtifact{}, err
	}
	client := &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 4 {
				return errors.New("too many provider media redirects")
			}
			_, err := allowedMurekaMediaURL(req.URL.String())
			return err
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return CreativeArtifact{}, fmt.Errorf("provider audio download failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CreativeArtifact{}, fmt.Errorf("provider audio download HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxArtifactBytes {
		return CreativeArtifact{}, errors.New("provider audio exceeds the maximum asset size")
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "" && mediaType != "application/octet-stream" &&
		!strings.HasPrefix(mediaType, "audio/") {
		return CreativeArtifact{}, errors.New("provider media response is not audio")
	}
	bytesWritten, err := io.Copy(temp, io.LimitReader(response.Body, maxArtifactBytes+1))
	if err != nil {
		return CreativeArtifact{}, err
	}
	if bytesWritten > maxArtifactBytes || bytesWritten < 44 {
		return CreativeArtifact{}, errors.New("provider audio is empty, too small, or too large")
	}
	if err := temp.Close(); err != nil {
		return CreativeArtifact{}, err
	}
	name := "generated-" + job.ID[:min(12, len(job.ID))] + extension
	return r.workspace.AddArtifact(ctx, NewCreativeArtifact{
		ProjectID: job.ProjectID, JobID: job.ID, Name: name,
		SourcePath: tempPath, Kind: "audio", Provider: job.Provider,
		LicenseNote: "Provider license and permitted commercial uses not verified",
	})
}
