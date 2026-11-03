package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedWorkspace(t *testing.T) (*CreativeWorkspace, context.Context) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("OTO_USER_DATA_DIR", root)
	s, err := OpenProjectStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseProjectStore(s) })
	return NewCreativeWorkspace(s), context.Background()
}

func TestCreativeWorkspaceLifecycleAndManifest(t *testing.T) {
	w, ctx := isolatedWorkspace(t)
	projects, err := w.ListProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("new workspace has projects: %v %v", projects, err)
	}
	p, err := w.CreateProject(ctx, NewCreativeProject{Title: "Night Scene", Kind: "story"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Kind != "story" {
		t.Fatalf("unexpected project: %+v", p)
	}
	if _, err := w.CreateProject(ctx, NewCreativeProject{Title: "x", Kind: "unknown"}); err == nil {
		t.Fatal("invalid kind accepted")
	}

	estimate, maxBudget := int64(85000), int64(100000)
	jobInput := NewCreativeJob{ProjectID: p.ID, Kind: "music.generate", Provider: "mureka",
		Input:               json.RawMessage(`{"style":"quiet","instrumental":true}`),
		EstimatedCostMicros: &estimate, MaxCostMicros: &maxBudget,
		IdempotencyKey: "music:scene-01:v1",
	}
	job, err := w.CreateJob(ctx, jobInput)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "draft" || job.ApprovalStatus != "pending" {
		t.Fatalf("job auto-ran: %+v", job)
	}
	repeat, err := w.CreateJob(ctx, jobInput)
	if err != nil || repeat.ID != job.ID {
		t.Fatalf("idempotency broken: %+v %v", repeat, err)
	}
	jobInput.Input = json.RawMessage(`{"style":"different"}`)
	if _, err := w.CreateJob(ctx, jobInput); err == nil {
		t.Fatal("idempotency key reused for different input")
	}
	if _, err := w.DecideJob(ctx, CreativeJobDecision{ID: job.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if jobAfter, err := w.GetJob(ctx, job.ID); err != nil || jobAfter.ApprovalStatus != "approved" || jobAfter.Status != "draft" {
		t.Fatalf("approval must not execute provider: %+v %v", jobAfter, err)
	}

	file := filepath.Join(t.TempDir(), "sample.wav")
	payload := []byte("RIFF-not-a-real-wav-but-valid-test-bytes")
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := w.AddArtifact(ctx, NewCreativeArtifact{ProjectID: p.ID, JobID: job.ID, SourcePath: file,
		Name: "Scene audio", Kind: "audio", Provider: "manual", LicenseNote: "Rights to be reviewed"})
	if err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256.Sum256(payload)
	if a.SHA256 != hex.EncodeToString(expectedHash[:]) || a.SizeBytes != int64(len(payload)) {
		t.Fatalf("import copy corrupted: %+v", a)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	localPath, _, err := w.artifactFilePath(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(localPath)
	if err != nil || string(data) != string(payload) {
		t.Fatalf("local copy lost: %v", err)
	}
	reviewed, err := w.ReviewArtifact(ctx, CreativeArtifactReview{ID: a.ID, Decision: "approved", Note: "Reviewed"})
	if err != nil || reviewed.ReviewStatus != "approved" {
		t.Fatalf("review failed: %+v %v", reviewed, err)
	}
	manifest, err := w.GetManifest(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != creativeSchemaVersion || len(manifest.Jobs) != 1 || len(manifest.Artifacts) != 1 {
		t.Fatalf("incomplete manifest: %+v", manifest)
	}
	path, err := w.ExportManifest(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "manifest.json") {
		t.Fatalf("wrong manifest path: %s", path)
	}
	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exported CreativeManifest
	if err := json.Unmarshal(snapshot, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Artifacts[0].ID != a.ID {
		t.Fatal("manifest snapshot differs from DB")
	}
	list, err := w.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("projects missing: %v %v", list, err)
	}
	// Reopening must preserve jobs and imported files.
	root := w.projects.root
	if err := CloseProjectStore(w.projects); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenProjectStore()
	if err != nil {
		t.Fatal(err)
	}
	w.projects = reopened
	if w.projects.root != root {
		t.Fatal("data root changed")
	}
	jobs, err := w.ListJobs(ctx, p.ID)
	if err != nil || len(jobs) != 1 || jobs[0].ApprovalStatus != "approved" {
		t.Fatalf("durability failed: %v %v", jobs, err)
	}
}

func TestCreativeApprovalAndImportGuards(t *testing.T) {
	w, ctx := isolatedWorkspace(t)
	p, err := w.CreateProject(ctx, NewCreativeProject{Title: "Review required"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := w.CreateJob(ctx, NewCreativeJob{ProjectID: p.ID, Kind: "image.generate",
		Provider: "paid-api", Input: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.DecideJob(ctx, CreativeJobDecision{ID: j.ID, Decision: "approve"}); err == nil {
		t.Fatal("approved job without explicit estimate and cap")
	}
	j, err = w.CancelJob(ctx, j.ID)
	if err != nil || j.Status != "cancelled" {
		t.Fatalf("cancel failed: %+v %v", j, err)
	}
	if _, err := w.DecideJob(ctx, CreativeJobDecision{ID: j.ID, Decision: "approve"}); err == nil {
		t.Fatal("cancelled job approved")
	}
	source := filepath.Join(t.TempDir(), "file.json")
	if err := os.WriteFile(source, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddArtifact(ctx, NewCreativeArtifact{ProjectID: p.ID, SourcePath: link}); err == nil {
		t.Fatal("symlink import unexpectedly allowed")
	}
	if _, err := w.AddArtifact(ctx, NewCreativeArtifact{ProjectID: p.ID, SourcePath: source, Name: "../escape", Kind: "other"}); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if _, err := w.AddArtifact(ctx, NewCreativeArtifact{ProjectID: p.ID, JobID: "nonexistent", SourcePath: source}); err == nil {
		t.Fatal("foreign job accepted")
	}
}

func TestLegacyCompositionSchemaPreserved(t *testing.T) {
	w, ctx := isolatedWorkspace(t)
	var tableCount int
	for _, name := range []string{"projects", "render_outputs", "settings", "creative_projects", "creative_jobs", "creative_artifacts"} {
		if err := w.projects.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&tableCount); err != nil {
			t.Fatal(err)
		}
		if tableCount != 1 {
			t.Fatalf("missing schema table: %s", name)
		}
	}
	p, err := w.CreateProject(ctx, NewCreativeProject{Title: "Does not create legacy compositions"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Fatal("no ID")
	}
	var legacyCount int
	if err := w.projects.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&legacyCount); err != nil {
		t.Fatal(err)
	}
	if legacyCount != 0 {
		t.Fatal("unexpected legacy composition mutation")
	}
}

func TestCreativeJobRejectsSecretFields(t *testing.T) {
	w, ctx := isolatedWorkspace(t)
	p, err := w.CreateProject(ctx, NewCreativeProject{Title: "Secret guard"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"apiKey":"should-not-save"}`,
		`{"config":{"client_secret":"should-not-save"}}`,
		`{"messages":[{"authorization":"should-not-save"}]}`,
	} {
		_, err := w.CreateJob(ctx, NewCreativeJob{ProjectID: p.ID, Kind: "music.generate", Input: json.RawMessage(input)})
		if err == nil {
			t.Fatalf("accepted credentials in job input: %s", input)
		}
	}
	_, err = w.CreateJob(ctx, NewCreativeJob{ProjectID: p.ID, Kind: "music.generate", Input: json.RawMessage(`{"prompt":"night scene","tokenCount":100}`)})
	if err != nil {
		t.Fatal(err)
	}
}
