package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"

	"github.com/sogud/Muse/internal/core"
)

// General creative projects live in the workspace CLI namespace. The older
// project command remains compatible with existing MIDI compositions.
func runWorkspace(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fail("INVALID_ARGUMENT", "workspace requires create/list/get/manifest/export")
	}
	switch args[0] {
	case "create":
		var title, description, kind string
		if _, err := parsedFlags("workspace create", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&title, "title", "", "project title")
			fs.StringVar(&description, "description", "", "description")
			fs.StringVar(&kind, "kind", "mixed", "music, story, visual, audio or mixed")
		}); err != nil {
			return nil, err
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewCreativeWorkspace(s).CreateProject(ctx, core.NewCreativeProject{
				Title: title, Description: description, Kind: kind})
		})
	case "list":
		if len(args) != 1 {
			return nil, fail("INVALID_ARGUMENT", "workspace list takes no arguments")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewCreativeWorkspace(s).ListProjects(ctx)
		})
	case "get", "manifest", "export":
		var id string
		if _, err := parsedFlags("workspace "+args[0], args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&id, "id", "", "project ID")
		}); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fail("INVALID_ARGUMENT", "--id is required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			w := core.NewCreativeWorkspace(s)
			switch args[0] {
			case "get":
				p, err := w.GetProject(ctx, id)
				if err != nil {
					return nil, err
				}
				if p == nil {
					return nil, fail("NOT_FOUND", "creative project not found")
				}
				return p, nil
			case "manifest":
				return w.GetManifest(ctx, id)
			case "export":
				path, err := w.ExportManifest(ctx, id)
				if err != nil {
					return nil, err
				}
				return map[string]any{"path": path, "snapshot": true}, nil
			}
			return nil, fail("INVALID_ARGUMENT", "unknown workspace subcommand")
		})
	default:
		return nil, fail("INVALID_ARGUMENT", "unknown workspace subcommand")
	}
}

func runCreativeJob(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fail("INVALID_ARGUMENT", "job requires create/list/get/approve/reject/cancel/submit/sync/status/attach/wait")
	}
	switch args[0] {
	case "submit", "sync", "resume", "status", "attach", "wait":
		return runJobLifecycle(ctx, args)
	}
	switch args[0] {
	case "create":
		var project, kind, provider, inputFile, idem, currency string
		var estimate, budget int64
		var noApproval bool
		if _, err := parsedFlags("job create", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&project, "project", "", "creative project ID")
			fs.StringVar(&kind, "kind", "", "job kind, e.g. music.generate")
			fs.StringVar(&provider, "provider", "", "model provider")
			fs.StringVar(&inputFile, "input", "", "JSON file with job parameters")
			fs.StringVar(&idem, "idempotency-key", "", "stable request id")
			fs.StringVar(&currency, "currency", "USD", "USD or CNY")
			fs.Int64Var(&estimate, "estimate-micros", -1, "estimated monetary cost in 1/1,000,000 currency unit")
			fs.Int64Var(&budget, "max-cost-micros", -1, "maximum monetary budget in 1/1,000,000 currency unit")
			fs.BoolVar(&noApproval, "no-approval", false, "mark non-paid intent as not requiring approval")
		}); err != nil {
			return nil, err
		}
		if project == "" || kind == "" {
			return nil, fail("INVALID_ARGUMENT", "--project and --kind are required")
		}
		if estimate < -1 || budget < -1 {
			return nil, fail("INVALID_ARGUMENT", "cost estimates must be nonnegative")
		}
		var input json.RawMessage
		if inputFile != "" {
			data, err := os.ReadFile(inputFile)
			if err != nil {
				return nil, err
			}
			if len(data) > 128<<10 {
				return nil, fail("INVALID_ARGUMENT", "job input exceeds 128 KiB")
			}
			input = data
		}
		approval := !noApproval
		record := core.NewCreativeJob{
			ProjectID: project, Kind: kind, Provider: provider, Input: input,
			RequiresApproval: &approval, IdempotencyKey: idem, Currency: currency,
		}
		if estimate >= 0 {
			record.EstimatedCostMicros = &estimate
		}
		if budget >= 0 {
			record.MaxCostMicros = &budget
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewCreativeWorkspace(s).CreateJob(ctx, record)
		})
	case "list":
		var project string
		if _, err := parsedFlags("job list", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&project, "project", "", "creative project ID")
		}); err != nil {
			return nil, err
		}
		if project == "" {
			return nil, fail("INVALID_ARGUMENT", "--project is required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewCreativeWorkspace(s).ListJobs(ctx, project)
		})
	case "get", "approve", "reject", "cancel":
		var id string
		if _, err := parsedFlags("job "+args[0], args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&id, "id", "", "job ID")
		}); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fail("INVALID_ARGUMENT", "--id is required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			w := core.NewCreativeWorkspace(s)
			switch args[0] {
			case "get":
				j, err := w.GetJob(ctx, id)
				if err != nil {
					return nil, err
				}
				if j == nil {
					return nil, fail("NOT_FOUND", "job not found")
				}
				return j, nil
			case "approve":
				return w.DecideJob(ctx, core.CreativeJobDecision{ID: id, Decision: "approve"})
			case "reject":
				return w.DecideJob(ctx, core.CreativeJobDecision{ID: id, Decision: "reject"})
			case "cancel":
				return w.CancelJob(ctx, id)
			}
			return nil, fail("INVALID_ARGUMENT", "unknown job subcommand")
		})
	default:
		return nil, fail("INVALID_ARGUMENT", "unknown job subcommand")
	}
}

func runCreativeArtifact(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fail("INVALID_ARGUMENT", "artifact requires import/list/get/export/review/reveal")
	}
	switch args[0] {
	case "import":
		var project, source, name, kind, provider, license, job string
		if _, err := parsedFlags("artifact import", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&project, "project", "", "project ID")
			fs.StringVar(&source, "source", "", "source file path")
			fs.StringVar(&name, "name", "", "display name")
			fs.StringVar(&kind, "kind", "other", "audio, image, video, story, document, other")
			fs.StringVar(&provider, "provider", "", "origin provider")
			fs.StringVar(&license, "license-note", "", "rights metadata, not an authorization guarantee")
			fs.StringVar(&job, "job", "", "optional associated job ID")
		}); err != nil {
			return nil, err
		}
		if project == "" || source == "" {
			return nil, fail("INVALID_ARGUMENT", "--project and --source are required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewCreativeWorkspace(s).AddArtifact(ctx, core.NewCreativeArtifact{
				ProjectID: project, SourcePath: source, Name: name, Kind: kind,
				Provider: provider, LicenseNote: license, JobID: job,
			})
		})
	case "list":
		var project string
		if _, err := parsedFlags("artifact list", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&project, "project", "", "creative project ID")
		}); err != nil {
			return nil, err
		}
		if project == "" {
			return nil, fail("INVALID_ARGUMENT", "--project is required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewCreativeWorkspace(s).ListArtifacts(ctx, project)
		})
	case "export":
		var id, output string
		if _, err := parsedFlags("artifact export", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&id, "id", "", "artifact ID")
			fs.StringVar(&output, "output", "", "new output file path (must not exist)")
		}); err != nil {
			return nil, err
		}
		if id == "" || output == "" {
			return nil, fail("INVALID_ARGUMENT", "--id and --output are required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			path, err := core.NewCreativeWorkspace(s).ExportArtifact(ctx, id, output)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "path": path, "exported": true}, nil
		})
	case "get", "reveal":
		var id string
		if _, err := parsedFlags("artifact "+args[0], args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&id, "id", "", "artifact ID")
		}); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fail("INVALID_ARGUMENT", "--id is required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			w := core.NewCreativeWorkspace(s)
			if args[0] == "reveal" {
				if err := w.RevealArtifact(ctx, id); err != nil {
					return nil, err
				}
				return map[string]any{"revealed": id}, nil
			}
			a, err := w.GetArtifact(ctx, id)
			if err != nil {
				return nil, err
			}
			if a == nil {
				return nil, fail("NOT_FOUND", "artifact not found")
			}
			return a, nil
		})
	case "review":
		var id, decision, note string
		if _, err := parsedFlags("artifact review", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&id, "id", "", "artifact ID")
			fs.StringVar(&decision, "decision", "", "approved, rejected or pending")
			fs.StringVar(&note, "note", "", "review reason")
		}); err != nil {
			return nil, err
		}
		if id == "" || decision == "" {
			return nil, fail("INVALID_ARGUMENT", "--id and --decision are required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewCreativeWorkspace(s).ReviewArtifact(ctx, core.CreativeArtifactReview{
				ID: id, Decision: decision, Note: note,
			})
		})
	default:
		return nil, fail("INVALID_ARGUMENT", "unknown artifact subcommand")
	}
}
