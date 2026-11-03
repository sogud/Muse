package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const creativeSchemaVersion = "creative-manifest/v1"
const maxArtifactBytes = 512 << 20

// This is intentionally separate from the legacy MIDI "projects" table.
// The original user library and its composer records are never reinterpreted.
const creativeSchema = `
CREATE TABLE IF NOT EXISTS creative_projects (
 id TEXT PRIMARY KEY,
 title TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL DEFAULT 'mixed',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS creative_jobs (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES creative_projects(id),
 kind TEXT NOT NULL,
 provider TEXT NOT NULL DEFAULT '',
 input_json TEXT NOT NULL DEFAULT '{}',
 status TEXT NOT NULL DEFAULT 'draft',
 approval_status TEXT NOT NULL DEFAULT 'pending',
 estimated_cost_micros INTEGER,
 max_cost_micros INTEGER,
 currency TEXT NOT NULL DEFAULT 'USD',
 idempotency_key TEXT,
 error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(project_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS creative_jobs_project_idx
 ON creative_jobs(project_id, created_at DESC);
-- Separate receipts preserve the existing creative_jobs schema and leave
-- projects created before the runner intact.
CREATE TABLE IF NOT EXISTS creative_job_receipts (
 job_id TEXT PRIMARY KEY REFERENCES creative_jobs(id),
 provider_task_id TEXT NOT NULL,
 provider_state TEXT NOT NULL,
 last_checked_at INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 actual_cost_micros INTEGER,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS creative_artifacts (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES creative_projects(id),
 job_id TEXT,
 name TEXT NOT NULL,
 kind TEXT NOT NULL,
 relative_path TEXT NOT NULL,
 mime_type TEXT NOT NULL,
 byte_size INTEGER NOT NULL,
 sha256 TEXT NOT NULL,
 provider TEXT NOT NULL DEFAULT '',
 license_note TEXT NOT NULL DEFAULT '',
 review_status TEXT NOT NULL DEFAULT 'pending',
 review_note TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 reviewed_at INTEGER
);
CREATE INDEX IF NOT EXISTS creative_artifacts_project_idx
 ON creative_artifacts(project_id, created_at DESC);
`

func (s *ProjectStore) initCreativeSchema() error {
	if s.db == nil {
		return errors.New("database is not initialized")
	}
	_, err := s.db.Exec(creativeSchema)
	return err
}

// CreativeWorkspace owns general-purpose projects, durable job intentions,
// locally copied artifacts and reviews. MyGo and CLI bind this same object.
type CreativeWorkspace struct{ projects *ProjectStore }

func NewCreativeWorkspace(projects *ProjectStore) *CreativeWorkspace {
	return &CreativeWorkspace{projects: projects}
}

type CreativeProject struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

type NewCreativeProject struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

func validCreativeKind(kind string) bool {
	switch kind {
	case "mixed", "music", "story", "visual", "audio":
		return true
	}
	return false
}

func (w *CreativeWorkspace) projectFolder(id string) (string, error) {
	if !validProjectID(id) {
		return "", errors.New("invalid creative project ID")
	}
	return filepath.Join(w.projects.root, "creative", "projects", id), nil
}

func (w *CreativeWorkspace) CreateProject(ctx context.Context, in NewCreativeProject) (CreativeProject, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" || len([]rune(title)) > 140 {
		return CreativeProject{}, errors.New("project title must contain 1-140 characters")
	}
	if len([]rune(in.Description)) > 4000 {
		return CreativeProject{}, errors.New("project description too long")
	}
	kind := in.Kind
	if kind == "" {
		kind = "mixed"
	}
	if !validCreativeKind(kind) {
		return CreativeProject{}, errors.New("invalid project kind")
	}
	id, err := newID()
	if err != nil {
		return CreativeProject{}, err
	}
	dir, err := w.projectFolder(id)
	if err != nil {
		return CreativeProject{}, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "artifacts"), 0700); err != nil {
		return CreativeProject{}, err
	}
	now := time.Now().UnixMilli()
	project := CreativeProject{id, title, strings.TrimSpace(in.Description), kind, now, now}
	_, err = w.projects.db.ExecContext(ctx,
		"INSERT INTO creative_projects(id,title,description,kind,created_at,updated_at) VALUES(?,?,?,?,?,?)",
		id, project.Title, project.Description, kind, now, now)
	if err != nil {
		return CreativeProject{}, fmt.Errorf("create creative project: %w", err)
	}
	return project, nil
}

func scanCreativeProject(s interface{ Scan(dest ...any) error }) (CreativeProject, error) {
	var p CreativeProject
	err := s.Scan(&p.ID, &p.Title, &p.Description, &p.Kind, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (w *CreativeWorkspace) ListProjects(ctx context.Context) ([]CreativeProject, error) {
	rows, err := w.projects.db.QueryContext(ctx,
		"SELECT id,title,description,kind,created_at,updated_at FROM creative_projects ORDER BY updated_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CreativeProject{}
	for rows.Next() {
		p, err := scanCreativeProject(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (w *CreativeWorkspace) GetProject(ctx context.Context, id string) (*CreativeProject, error) {
	if !validProjectID(id) {
		return nil, errors.New("invalid project ID")
	}
	p, err := scanCreativeProject(w.projects.db.QueryRowContext(ctx,
		"SELECT id,title,description,kind,created_at,updated_at FROM creative_projects WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (w *CreativeWorkspace) requireProject(ctx context.Context, id string) error {
	project, err := w.GetProject(ctx, id)
	if err != nil {
		return err
	}
	if project == nil {
		return errors.New("creative project not found")
	}
	return nil
}

type CreativeJob struct {
	ID                  string          `json:"id"`
	ProjectID           string          `json:"projectId"`
	Kind                string          `json:"kind"`
	Provider            string          `json:"provider"`
	Input               json.RawMessage `json:"input"`
	Status              string          `json:"status"`
	ApprovalStatus      string          `json:"approvalStatus"`
	EstimatedCostMicros *int64          `json:"estimatedCostMicros,omitempty"`
	MaxCostMicros       *int64          `json:"maxCostMicros,omitempty"`
	Currency            string          `json:"currency"`
	IdempotencyKey      string          `json:"idempotencyKey,omitempty"`
	Error               string          `json:"error,omitempty"`
	CreatedAt           int64           `json:"createdAt"`
	UpdatedAt           int64           `json:"updatedAt"`
}

type NewCreativeJob struct {
	ProjectID           string          `json:"projectId"`
	Kind                string          `json:"kind"`
	Provider            string          `json:"provider"`
	Input               json.RawMessage `json:"input"`
	RequiresApproval    *bool           `json:"requiresApproval,omitempty"`
	EstimatedCostMicros *int64          `json:"estimatedCostMicros,omitempty"`
	MaxCostMicros       *int64          `json:"maxCostMicros,omitempty"`
	Currency            string          `json:"currency"`
	IdempotencyKey      string          `json:"idempotencyKey,omitempty"`
}

func scanCreativeJob(s interface{ Scan(dest ...any) error }) (CreativeJob, error) {
	var j CreativeJob
	var raw string
	var idem, errtext string
	err := s.Scan(&j.ID, &j.ProjectID, &j.Kind, &j.Provider, &raw, &j.Status, &j.ApprovalStatus,
		&j.EstimatedCostMicros, &j.MaxCostMicros, &j.Currency, &idem, &errtext, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return j, err
	}
	j.Input = json.RawMessage(raw)
	j.IdempotencyKey = idem
	j.Error = errtext
	return j, nil
}

const jobColumns = "id,project_id,kind,provider,input_json,status,approval_status,estimated_cost_micros,max_cost_micros,currency,coalesce(idempotency_key,''),error,created_at,updated_at"

func (w *CreativeWorkspace) GetJob(ctx context.Context, id string) (*CreativeJob, error) {
	if !validProjectID(id) {
		return nil, errors.New("invalid job ID")
	}
	j, err := scanCreativeJob(w.projects.db.QueryRowContext(ctx,
		"SELECT "+jobColumns+" FROM creative_jobs WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (w *CreativeWorkspace) ListJobs(ctx context.Context, projectID string) ([]CreativeJob, error) {
	if err := w.requireProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := w.projects.db.QueryContext(ctx,
		"SELECT "+jobColumns+" FROM creative_jobs WHERE project_id=? ORDER BY created_at DESC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CreativeJob{}
	for rows.Next() {
		j, err := scanCreativeJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}

func normalizeJobInput(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	if len(raw) > 128<<10 {
		return nil, errors.New("job input exceeds 128 KiB")
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return nil, errors.New("job input must be a JSON object")
	}
	if containsCredentialKey(v) {
		return nil, errors.New("job input contains a credential field; configure provider credentials outside the job manifest")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(data) > 128<<10 {
		return nil, errors.New("job input exceeds 128 KiB")
	}
	return data, nil
}

// An Agent's persisted input is public project metadata, not a vault.
// Reject known credential key names at any nesting level rather than risking
// accidental disclosure in manifests and CLI output.
func containsCredentialKey(value any) bool {
	switch fields := value.(type) {
	case map[string]any:
		for key, item := range fields {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
			switch normalized {
			case "apikey", "secret", "clientsecret", "accesstoken", "refreshtoken", "authorization", "password", "cookie", "bearertoken", "credentials":
				return true
			}
			if containsCredentialKey(item) {
				return true
			}
		}
	case []any:
		for _, item := range fields {
			if containsCredentialKey(item) {
				return true
			}
		}
	}
	return false
}

func (w *CreativeWorkspace) CreateJob(ctx context.Context, in NewCreativeJob) (CreativeJob, error) {
	if err := w.requireProject(ctx, in.ProjectID); err != nil {
		return CreativeJob{}, err
	}
	if in.Kind == "" || len(in.Kind) > 80 || strings.ContainsAny(in.Kind, " /\\") {
		return CreativeJob{}, errors.New("invalid job kind")
	}
	if len(in.Provider) > 120 {
		return CreativeJob{}, errors.New("provider name too long")
	}
	if len(in.IdempotencyKey) > 150 {
		return CreativeJob{}, errors.New("idempotency key too long")
	}
	for _, n := range []*int64{in.EstimatedCostMicros, in.MaxCostMicros} {
		if n != nil && *n < 0 {
			return CreativeJob{}, errors.New("cost must not be negative")
		}
	}
	currency := in.Currency
	if currency == "" {
		currency = "USD"
	}
	if currency != "USD" && currency != "CNY" {
		return CreativeJob{}, errors.New("currency must be USD or CNY")
	}
	raw, err := normalizeJobInput(in.Input)
	if err != nil {
		return CreativeJob{}, err
	}
	requireApproval := true
	if in.RequiresApproval != nil {
		requireApproval = *in.RequiresApproval
	}
	approval := "pending"
	if !requireApproval {
		approval = "not_required"
	}

	if in.IdempotencyKey != "" {
		var id string
		err := w.projects.db.QueryRowContext(ctx,
			"SELECT id FROM creative_jobs WHERE project_id=? AND idempotency_key=?",
			in.ProjectID, in.IdempotencyKey).Scan(&id)
		if err == nil {
			existing, getErr := w.GetJob(ctx, id)
			if getErr != nil {
				return CreativeJob{}, getErr
			}
			if existing != nil && existing.Kind == in.Kind && existing.Provider == in.Provider &&
				string(existing.Input) == string(raw) && existing.Currency == currency &&
				equalNullableInt(existing.EstimatedCostMicros, in.EstimatedCostMicros) &&
				equalNullableInt(existing.MaxCostMicros, in.MaxCostMicros) &&
				(existing.ApprovalStatus == "not_required") == (!requireApproval) {
				return *existing, nil
			}
			return CreativeJob{}, errors.New("idempotency key already exists for different job input")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return CreativeJob{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return CreativeJob{}, err
	}
	now := time.Now().UnixMilli()
	_, err = w.projects.db.ExecContext(ctx, `INSERT INTO creative_jobs
      (id,project_id,kind,provider,input_json,status,approval_status,estimated_cost_micros,max_cost_micros,currency,idempotency_key,error,created_at,updated_at)
      VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, in.ProjectID, in.Kind, in.Provider, string(raw), "draft", approval,
		in.EstimatedCostMicros, in.MaxCostMicros, currency, nullIfEmpty(in.IdempotencyKey), "", now, now)
	if err != nil {
		return CreativeJob{}, fmt.Errorf("create job (possible duplicate idempotency key): %w", err)
	}
	result, err := w.GetJob(ctx, id)
	if err != nil {
		return CreativeJob{}, err
	}
	return *result, nil
}

func equalNullableInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type CreativeJobDecision struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

// Approving a *draft* only records a human decision. It does not enqueue,
// execute, charge, or call a provider; execution is intentionally not implemented.
func (w *CreativeWorkspace) DecideJob(ctx context.Context, in CreativeJobDecision) (CreativeJob, error) {
	j, err := w.GetJob(ctx, in.ID)
	if err != nil {
		return CreativeJob{}, err
	}
	if j == nil {
		return CreativeJob{}, errors.New("job not found")
	}
	if j.Status != "draft" {
		return CreativeJob{}, errors.New("job is no longer a draft")
	}
	var approval string
	switch in.Decision {
	case "approve":
		if j.ApprovalStatus == "rejected" {
			return CreativeJob{}, errors.New("rejected job cannot be approved")
		}
		if j.ApprovalStatus == "pending" {
			if j.EstimatedCostMicros == nil || j.MaxCostMicros == nil {
				return CreativeJob{}, errors.New("approval requires explicit estimated and maximum costs")
			}
			if *j.EstimatedCostMicros > *j.MaxCostMicros {
				return CreativeJob{}, errors.New("estimated cost exceeds maximum budget")
			}
		}
		approval = "approved"
	case "reject":
		approval = "rejected"
	default:
		return CreativeJob{}, errors.New("decision must be approve or reject")
	}
	now := time.Now().UnixMilli()
	_, err = w.projects.db.ExecContext(ctx,
		"UPDATE creative_jobs SET approval_status=?,updated_at=? WHERE id=? AND status='draft'",
		approval, now, j.ID)
	if err != nil {
		return CreativeJob{}, err
	}
	result, err := w.GetJob(ctx, j.ID)
	if err != nil {
		return CreativeJob{}, err
	}
	return *result, nil
}

func (w *CreativeWorkspace) CancelJob(ctx context.Context, id string) (CreativeJob, error) {
	j, err := w.GetJob(ctx, id)
	if err != nil {
		return CreativeJob{}, err
	}
	if j == nil {
		return CreativeJob{}, errors.New("job not found")
	}
	if j.Status != "draft" {
		return CreativeJob{}, errors.New("only draft jobs can be cancelled in this release")
	}
	_, err = w.projects.db.ExecContext(ctx,
		"UPDATE creative_jobs SET status='cancelled',updated_at=? WHERE id=? AND status='draft'",
		time.Now().UnixMilli(), id)
	if err != nil {
		return CreativeJob{}, err
	}
	result, err := w.GetJob(ctx, id)
	if err != nil {
		return CreativeJob{}, err
	}
	return *result, nil
}

type CreativeArtifact struct {
	ID           string `json:"id"`
	ProjectID    string `json:"projectId"`
	JobID        string `json:"jobId,omitempty"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	MimeType     string `json:"mimeType"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
	Provider     string `json:"provider,omitempty"`
	LicenseNote  string `json:"licenseNote,omitempty"`
	ReviewStatus string `json:"reviewStatus"`
	ReviewNote   string `json:"reviewNote,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	ReviewedAt   *int64 `json:"reviewedAt,omitempty"`
}

type NewCreativeArtifact struct {
	ProjectID   string `json:"projectId"`
	JobID       string `json:"jobId"`
	SourcePath  string `json:"sourcePath"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Provider    string `json:"provider"`
	LicenseNote string `json:"licenseNote"`
}

func validArtifactKind(kind string) bool {
	switch kind {
	case "audio", "image", "video", "story", "document", "other":
		return true
	}
	return false
}

const artifactColumns = "id,project_id,coalesce(job_id,''),name,kind,mime_type,byte_size,sha256,provider,license_note,review_status,review_note,created_at,reviewed_at"

func scanCreativeArtifact(s interface{ Scan(dest ...any) error }) (CreativeArtifact, error) {
	var a CreativeArtifact
	err := s.Scan(&a.ID, &a.ProjectID, &a.JobID, &a.Name, &a.Kind, &a.MimeType, &a.SizeBytes, &a.SHA256,
		&a.Provider, &a.LicenseNote, &a.ReviewStatus, &a.ReviewNote, &a.CreatedAt, &a.ReviewedAt)
	return a, err
}

func (w *CreativeWorkspace) GetArtifact(ctx context.Context, id string) (*CreativeArtifact, error) {
	if !validProjectID(id) {
		return nil, errors.New("invalid artifact ID")
	}
	artifact, err := scanCreativeArtifact(w.projects.db.QueryRowContext(ctx,
		"SELECT "+artifactColumns+" FROM creative_artifacts WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (w *CreativeWorkspace) ListArtifacts(ctx context.Context, projectID string) ([]CreativeArtifact, error) {
	if err := w.requireProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := w.projects.db.QueryContext(ctx,
		"SELECT "+artifactColumns+" FROM creative_artifacts WHERE project_id=? ORDER BY created_at DESC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []CreativeArtifact{}
	for rows.Next() {
		a, err := scanCreativeArtifact(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, rows.Err()
}

// AddArtifact copies bytes into the app-owned project directory, so an Agent
// can delete the original temporary file without invalidating project state.
func (w *CreativeWorkspace) AddArtifact(ctx context.Context, in NewCreativeArtifact) (CreativeArtifact, error) {
	if err := w.requireProject(ctx, in.ProjectID); err != nil {
		return CreativeArtifact{}, err
	}
	if in.Kind == "" {
		in.Kind = "other"
	}
	if !validArtifactKind(in.Kind) {
		return CreativeArtifact{}, errors.New("invalid artifact kind")
	}
	if strings.TrimSpace(in.SourcePath) == "" {
		return CreativeArtifact{}, errors.New("sourcePath is required")
	}
	if len(in.Provider) > 120 || len(in.LicenseNote) > 3000 {
		return CreativeArtifact{}, errors.New("artifact metadata too long")
	}
	if in.JobID != "" {
		job, err := w.GetJob(ctx, in.JobID)
		if err != nil {
			return CreativeArtifact{}, err
		}
		if job == nil || job.ProjectID != in.ProjectID {
			return CreativeArtifact{}, errors.New("job does not belong to project")
		}
	}
	path, err := filepath.Abs(in.SourcePath)
	if err != nil {
		return CreativeArtifact{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return CreativeArtifact{}, err
	}
	if !info.Mode().IsRegular() {
		return CreativeArtifact{}, errors.New("source must be a regular file, not a symlink or directory")
	}
	if info.Size() > maxArtifactBytes {
		return CreativeArtifact{}, errors.New("artifact exceeds maximum size")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	if len([]rune(name)) > 200 || name == "" || strings.ContainsAny(name, "/\\\x00") {
		return CreativeArtifact{}, errors.New("invalid artifact name")
	}
	id, err := newID()
	if err != nil {
		return CreativeArtifact{}, err
	}
	projectDir, err := w.projectFolder(in.ProjectID)
	if err != nil {
		return CreativeArtifact{}, err
	}
	dir := filepath.Join(projectDir, "artifacts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return CreativeArtifact{}, err
	}
	extension := strings.ToLower(filepath.Ext(path))
	if len(extension) > 12 {
		extension = ""
	}
	rel := filepath.Join("artifacts", id+extension)
	dest := filepath.Join(projectDir, rel)
	src, err := os.Open(path)
	if err != nil {
		return CreativeArtifact{}, err
	}
	defer src.Close()
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return CreativeArtifact{}, err
	}
	defer func() { file.Close() }()
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(src, maxArtifactBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || n > maxArtifactBytes {
		os.Remove(dest)
		if copyErr != nil {
			return CreativeArtifact{}, copyErr
		}
		if closeErr != nil {
			return CreativeArtifact{}, closeErr
		}
		return CreativeArtifact{}, errors.New("artifact exceeds maximum size")
	}
	contentType := mime.TypeByExtension(extension)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	now := time.Now().UnixMilli()
	_, err = w.projects.db.ExecContext(ctx, `INSERT INTO creative_artifacts
      (id,project_id,job_id,name,kind,relative_path,mime_type,byte_size,sha256,provider,license_note,review_status,review_note,created_at)
      VALUES(?,?,?,?,?,?,?,?,?,?,?,'pending','',?)`,
		id, in.ProjectID, nullIfEmpty(in.JobID), name, in.Kind, rel, contentType, n, hex.EncodeToString(hasher.Sum(nil)),
		in.Provider, in.LicenseNote, now)
	if err != nil {
		os.Remove(dest)
		return CreativeArtifact{}, err
	}
	artifact, err := w.GetArtifact(ctx, id)
	if err != nil {
		return CreativeArtifact{}, err
	}
	return *artifact, nil
}

type CreativeArtifactReview struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

func (w *CreativeWorkspace) ReviewArtifact(ctx context.Context, in CreativeArtifactReview) (CreativeArtifact, error) {
	if in.Decision != "approved" && in.Decision != "rejected" && in.Decision != "pending" {
		return CreativeArtifact{}, errors.New("review must be approved, rejected or pending")
	}
	if len(in.Note) > 2000 {
		return CreativeArtifact{}, errors.New("review note too long")
	}
	a, err := w.GetArtifact(ctx, in.ID)
	if err != nil {
		return CreativeArtifact{}, err
	}
	if a == nil {
		return CreativeArtifact{}, errors.New("artifact not found")
	}
	now := time.Now().UnixMilli()
	var reviewedAt any = now
	if in.Decision == "pending" {
		reviewedAt = nil
	}
	_, err = w.projects.db.ExecContext(ctx,
		"UPDATE creative_artifacts SET review_status=?,review_note=?,reviewed_at=? WHERE id=?",
		in.Decision, strings.TrimSpace(in.Note), reviewedAt, in.ID)
	if err != nil {
		return CreativeArtifact{}, err
	}
	reviewed, err := w.GetArtifact(ctx, in.ID)
	if err != nil {
		return CreativeArtifact{}, err
	}
	return *reviewed, nil
}

type CreativeManifest struct {
	SchemaVersion string             `json:"schemaVersion"`
	Project       CreativeProject    `json:"project"`
	Jobs          []CreativeJob      `json:"jobs"`
	Artifacts     []CreativeArtifact `json:"artifacts"`
}

func (w *CreativeWorkspace) GetManifest(ctx context.Context, projectID string) (CreativeManifest, error) {
	project, err := w.GetProject(ctx, projectID)
	if err != nil {
		return CreativeManifest{}, err
	}
	if project == nil {
		return CreativeManifest{}, errors.New("project not found")
	}
	jobs, err := w.ListJobs(ctx, projectID)
	if err != nil {
		return CreativeManifest{}, err
	}
	artifacts, err := w.ListArtifacts(ctx, projectID)
	if err != nil {
		return CreativeManifest{}, err
	}
	return CreativeManifest{creativeSchemaVersion, *project, jobs, artifacts}, nil
}

// ExportManifest is a snapshot, never a second writable source of truth.
func (w *CreativeWorkspace) ExportManifest(ctx context.Context, projectID string) (string, error) {
	result, err := w.GetManifest(ctx, projectID)
	if err != nil {
		return "", err
	}
	dir, err := w.projectFolder(projectID)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "manifest.json")
	if err = os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}

func (w *CreativeWorkspace) artifactFilePath(ctx context.Context, id string) (string, string, error) {
	a, err := w.GetArtifact(ctx, id)
	if err != nil {
		return "", "", err
	}
	if a == nil {
		return "", "", errors.New("artifact not found")
	}
	var rel string
	if err := w.projects.db.QueryRowContext(ctx, "SELECT relative_path FROM creative_artifacts WHERE id=?", id).Scan(&rel); err != nil {
		return "", "", err
	}
	dir, err := w.projectFolder(a.ProjectID)
	if err != nil {
		return "", "", err
	}
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") || !strings.HasPrefix(rel, "artifacts"+string(filepath.Separator)) {
		return "", "", errors.New("invalid stored artifact path")
	}
	full := filepath.Join(dir, rel)
	info, err := os.Lstat(full)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("artifact is not a regular file")
	}
	return full, a.MimeType, nil
}

// ExportArtifact makes one explicit, verified copy for an Agent or user.
// It cannot overwrite an existing destination or follow a destination
// symlink. The stored SHA-256 and byte count must match the exported file.
func (w *CreativeWorkspace) ExportArtifact(ctx context.Context, id, destination string) (string, error) {
	if strings.TrimSpace(destination) == "" {
		return "", errors.New("output path is required")
	}
	artifact, err := w.GetArtifact(ctx, id)
	if err != nil {
		return "", err
	}
	if artifact == nil {
		return "", errors.New("artifact not found")
	}
	storedPath, _, err := w.artifactFilePath(ctx, id)
	if err != nil {
		return "", err
	}
	outputPath, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if outputPath == storedPath {
		return "", errors.New("export destination cannot be the stored artifact")
	}
	source, err := os.Open(storedPath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("export destination must not exist: %w", err)
	}
	complete := false
	defer func() {
		output.Close()
		if !complete {
			os.Remove(outputPath)
		}
	}()
	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(output, hasher), io.LimitReader(source, maxArtifactBytes+1))
	if err != nil {
		return "", err
	}
	if size != artifact.SizeBytes || hex.EncodeToString(hasher.Sum(nil)) != artifact.SHA256 {
		return "", errors.New("stored artifact checksum or size does not match its manifest")
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	complete = true
	return outputPath, nil
}

// OpenArtifact previews a saved, regular local asset in the user's macOS
// default application. Restrict it to passive media/document formats so a
// downloaded shell script or executable is never launched as a preview.
// This is an explicit user action, never run while refreshing a project.
func (w *CreativeWorkspace) OpenArtifact(ctx context.Context, id string) error {
	path, _, err := w.artifactFilePath(ctx, id)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("artifact preview is currently available on macOS only")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic",
		".wav", ".mp3", ".m4a", ".flac", ".ogg",
		".mp4", ".mov", ".webm",
		".pdf", ".json", ".txt", ".md", ".csv":
		// Only an explicitly selected safe media or document type is opened.
	default:
		return errors.New("unsupported preview format; use Finder to inspect this file")
	}
	return exec.CommandContext(ctx, "/usr/bin/open", path).Run()
}

func (w *CreativeWorkspace) RevealArtifact(ctx context.Context, id string) error {
	path, _, err := w.artifactFilePath(ctx, id)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("reveal is currently available on macOS only")
	}
	return exec.CommandContext(ctx, "open", "-R", path).Run()
}
