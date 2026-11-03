// Command creative is a headless, JSON-first interface for local AI agents.
// It depends on the same Go core as the optional MyGo desktop application.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sogud/Muse/internal/core"
)

type cliError struct {
	code string
	err  error
}

func (e *cliError) Error() string     { return e.err.Error() }
func fail(code, message string) error { return &cliError{code: code, err: errors.New(message)} }

func writeJSON(w io.Writer, v any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}

func parsedFlags(name string, args []string, setup func(*flag.FlagSet)) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	setup(fs)
	if err := fs.Parse(args); err != nil {
		return nil, fail("INVALID_ARGUMENT", err.Error())
	}
	if fs.NArg() != 0 {
		return nil, fail("INVALID_ARGUMENT", "unexpected positional arguments; use --help for commands")
	}
	return fs, nil
}

func withStore(ctx context.Context, fn func(*core.ProjectStore) (any, error)) (any, error) {
	store, err := core.OpenProjectStore()
	if err != nil {
		return nil, err
	}
	defer core.CloseProjectStore(store)
	return fn(store)
}

// run is separately testable: it never starts a GUI, server or background worker.
func run(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return map[string]any{
			"name": "creative", "mode": "headless", "version": "0.1.0-dev",
			"commands": []string{
				"creative doctor",
				"creative providers",
				"creative workspace create --title TITLE [--kind mixed|music|story|visual|audio]",
				"creative workspace list|get|manifest|export --id ID",
				"creative job create --project ID --kind KIND [--provider NAME --input FILE --estimate-micros N --max-cost-micros N --idempotency-key KEY]",
				"creative job list --project ID",
				"creative job get|approve|reject|cancel --id ID",
				"creative job submit --id ID --allow-paid",
				"creative job status|sync|resume --id ID",
				"creative job wait --id ID [--interval 5s --timeout 10m]",
				"creative job attach --id ID --provider-task-id EXISTING_TASK_ID",
				"creative artifact import --project ID --source FILE [--kind audio|image|video|story|document|other]",
				"creative artifact list --project ID",
				"creative artifact get|reveal --id ID",
				"creative artifact export --id ARTIFACT_ID --output NEW_FILE_PATH",
				"creative artifact review --id ID --decision approved|rejected|pending",

				"creative project list",
				"creative project get --id PROJECT_ID",
				"creative project create --file COMPOSITION_JSON [--title TITLE]",
				"creative project delete --id PROJECT_ID --confirm",
				"creative render check",
				"creative render project --id PROJECT_ID",
				"creative music providers",
				"creative music generate --provider mureka|elevenlabs|lyria --prompt TEXT [--instrumental --model MODEL --duration-seconds N] [--dry-run | --project ID --idempotency-key KEY --estimate-micros N --max-cost-micros N --approve-paid]",
				"creative music status --id TASK_ID [--instrumental]",
				"creative pattern generate --idea TEXT [--style STYLE] [--bpm 120] [--bars 4] --approve-paid",
				"creative story validate --input STORY_INPUT_JSON",
				"creative story build --input STORY_INPUT_JSON --output STORYPACK_JSON",
				"creative story build --input STORY_INPUT_JSON --project PROJECT_ID (local job + archived StoryPack + manifest)",
			},
			"credentials":     "never returned in JSON; configure OTO_USER_DATA_DIR for isolated projects",
			"paid_operations": "job submit requires approved draft and --allow-paid; legacy music generate requires --approve-paid; pricing is user-entered, not reconciled",
		}, nil
	}
	switch args[0] {
	case "doctor":
		if len(args) > 1 {
			return nil, fail("INVALID_ARGUMENT", "doctor takes no arguments")
		}
		tool := core.MusicGeneration{}.ListProviders()
		return map[string]any{
			"healthy": true, "interface": "CLI", "guiRequired": false,
			"musicProviders": tool,
			"jobRuntime": map[string]any{
				"supportedKinds": []string{"music.generate"}, "backgroundWorker": false,
				"paidSubmitRequiresApproval": true, "costReconciled": false,
			},
		}, nil
	case "providers":
		if len(args) != 1 {
			return nil, fail("INVALID_ARGUMENT", "providers takes no arguments")
		}
		return withStore(ctx, func(store *core.ProjectStore) (any, error) {
			return core.NewNativeSettings(store).ListProviderStatuses(), nil
		})
	case "workspace":
		return runWorkspace(ctx, args[1:])
	case "job":
		return runCreativeJob(ctx, args[1:])
	case "artifact":
		return runCreativeArtifact(ctx, args[1:])
	case "project":
		return runProject(ctx, args[1:])
	case "render":
		return runRender(ctx, args[1:])
	case "music":
		return runMusic(ctx, args[1:])
	case "pattern":
		return runPattern(ctx, args[1:])
	case "story":
		return runStory(ctx, args[1:])
	default:
		return nil, fail("INVALID_ARGUMENT", "unknown command: "+args[0])
	}
}

func runProject(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fail("INVALID_ARGUMENT", "project command requires list, get, create or delete")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return nil, fail("INVALID_ARGUMENT", "project list takes no arguments")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) { return s.List(ctx) })
	case "get":
		var id string
		if _, err := parsedFlags("project get", args[1:], func(fs *flag.FlagSet) { fs.StringVar(&id, "id", "", "project ID") }); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fail("INVALID_ARGUMENT", "--id is required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			p, err := s.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			if p == nil {
				return nil, fail("NOT_FOUND", "project not found")
			}
			return p, nil
		})
	case "create":
		var filename, title string
		if _, err := parsedFlags("project create", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&filename, "file", "", "composition.json")
			fs.StringVar(&title, "title", "", "project title")
		}); err != nil {
			return nil, err
		}
		if filename == "" {
			return nil, fail("INVALID_ARGUMENT", "--file is required")
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		if len(data) > 2<<20 {
			return nil, fail("INVALID_ARGUMENT", "composition exceeds 2 MiB")
		}
		var composition core.Composition
		if err := json.Unmarshal(data, &composition); err != nil {
			return nil, fail("INVALID_ARGUMENT", "composition is not valid JSON")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return s.Create(ctx, core.CreateProjectInput{Title: title, Composition: composition})
		})
	case "delete":
		var id string
		var confirmed bool
		if _, err := parsedFlags("project delete", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&id, "id", "", "project ID")
			fs.BoolVar(&confirmed, "confirm", false, "confirm permanent deletion")
		}); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fail("INVALID_ARGUMENT", "--id is required")
		}
		if !confirmed {
			return nil, fail("APPROVAL_REQUIRED", "deleting a project requires --confirm")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			if err := s.Remove(ctx, id); err != nil {
				return nil, err
			}
			return map[string]any{"removed": id}, nil
		})
	default:
		return nil, fail("INVALID_ARGUMENT", "unknown project subcommand")
	}
}

func runRender(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fail("INVALID_ARGUMENT", "render requires check or project")
	}
	switch args[0] {
	case "check":
		if len(args) != 1 {
			return nil, fail("INVALID_ARGUMENT", "render check takes no arguments")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewRenderService(s).CheckTools(ctx)
		})
	case "project":
		var id string
		if _, err := parsedFlags("render project", args[1:], func(fs *flag.FlagSet) { fs.StringVar(&id, "id", "", "project ID") }); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fail("INVALID_ARGUMENT", "--id is required")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			return core.NewRenderService(s).RenderProject(ctx, id)
		})
	default:
		return nil, fail("INVALID_ARGUMENT", "unknown render subcommand")
	}
}

func runMusic(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fail("INVALID_ARGUMENT", "music requires providers, generate or status")
	}
	service := core.MusicGeneration{}
	switch args[0] {
	case "providers":
		if len(args) > 1 {
			return nil, fail("INVALID_ARGUMENT", "music providers takes no arguments")
		}
		return service.ListProviders(), nil
	case "generate":
		// Compatibility shortcut: every paid music request is now recorded in
		// the same durable JobRuntime as job submit/sync. No direct paid POST.
		var prompt, lyrics, model, projectID, idempotencyKey, provider string
		var instrumental, approve, dry bool
		var estimate, ceiling int64
		var duration int
		if _, err := parsedFlags("music generate", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&provider, "provider", "mureka", "music provider: mureka, elevenlabs, lyria")
			fs.StringVar(&prompt, "prompt", "", "music prompt")
			fs.StringVar(&lyrics, "lyrics", "", "optional lyrics")
			fs.IntVar(&duration, "duration-seconds", 0, "optional target duration in seconds")
			fs.StringVar(&model, "model", "", "optional provider model")
			fs.StringVar(&projectID, "project", "", "creative workspace project ID")
			fs.StringVar(&idempotencyKey, "idempotency-key", "", "stable key to prevent duplicate paid requests")
			fs.Int64Var(&estimate, "estimate-micros", -1, "user-entered USD estimated cost in millionths")
			fs.Int64Var(&ceiling, "max-cost-micros", -1, "user-entered USD budget ceiling in millionths")
			fs.BoolVar(&instrumental, "instrumental", false, "instrumental generation")
			fs.BoolVar(&approve, "approve-paid", false, "explicitly authorize one paid API request")
			fs.BoolVar(&dry, "dry-run", false, "validate intention without a network request")
		}); err != nil {
			return nil, err
		}
		if len(strings.TrimSpace(prompt)) == 0 {
			return nil, fail("INVALID_ARGUMENT", "--prompt is required")
		}
		switch provider {
		case "mureka", "elevenlabs", "lyria":
		default:
			return nil, fail("INVALID_ARGUMENT", "unknown music provider; use creative music providers")
		}
		if duration < 0 || duration > 600 {
			return nil, fail("INVALID_ARGUMENT", "--duration-seconds must be 0–600")
		}
		if provider == "mureka" && (len([]rune(prompt)) > 1024 || len([]rune(lyrics)) > 5000) {
			return nil, fail("INVALID_ARGUMENT", "Mureka prompt or lyrics exceed input limits")
		}
		if provider != "mureka" && (len([]rune(prompt)) > 3600 || len([]rune(lyrics)) > 3500) {
			return nil, fail("INVALID_ARGUMENT", "direct music prompt or lyrics exceed input limits")
		}
		payload, err := json.Marshal(map[string]any{
			"prompt": prompt, "lyrics": lyrics, "model": model,
			"instrumental": instrumental, "durationSeconds": duration,
		})
		if err != nil {
			return nil, err
		}
		if err := core.ValidateMusicProviderRequest(provider, payload); err != nil {
			return nil, fail("INVALID_ARGUMENT", err.Error())
		}
		if dry {
			return map[string]any{
				"approved": false, "wouldCall": provider, "wouldCreateTasks": 1,
				"instrumental": instrumental, "model": model, "networkCalled": false,
				"note": "paid submission requires project, idempotency key and explicit USD estimate and ceiling",
			}, nil
		}
		if !approve {
			return nil, fail("APPROVAL_REQUIRED", "paid generation requires --approve-paid")
		}
		if projectID == "" || idempotencyKey == "" || estimate <= 0 || ceiling < estimate {
			return nil, fail("INVALID_ARGUMENT", "paid generation requires --project, --idempotency-key, a positive --estimate-micros and --max-cost-micros >= estimate")
		}
		return withStore(ctx, func(s *core.ProjectStore) (any, error) {
			workspace := core.NewCreativeWorkspace(s)
			job, err := workspace.CreateJob(ctx, core.NewCreativeJob{
				ProjectID: projectID, Kind: "music.generate", Provider: provider,
				Input: payload, IdempotencyKey: idempotencyKey, Currency: "USD",
				EstimatedCostMicros: &estimate, MaxCostMicros: &ceiling,
			})
			if err != nil {
				return nil, err
			}
			runtime := core.NewJobRuntime(workspace)
			// A repeated idempotency key never triggers another paid call.
			if job.Status != "draft" {
				return runtime.Status(ctx, job.ID)
			}
			if job.ApprovalStatus == "pending" {
				if _, err := workspace.DecideJob(ctx, core.CreativeJobDecision{
					ID: job.ID, Decision: "approve",
				}); err != nil {
					return nil, err
				}
			}
			return runtime.Submit(ctx, job.ID, true)
		})
	case "status":
		var id string
		var instrumental bool
		if _, err := parsedFlags("music status", args[1:], func(fs *flag.FlagSet) {
			fs.StringVar(&id, "id", "", "provider task ID")
			fs.BoolVar(&instrumental, "instrumental", false, "instrumental task")
		}); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fail("INVALID_ARGUMENT", "--id is required")
		}
		return service.GetMurekaTask(ctx, core.MusicTaskInput{TaskID: id, Instrumental: instrumental})
	default:
		return nil, fail("INVALID_ARGUMENT", "unknown music subcommand")
	}
}

func runPattern(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 || args[0] != "generate" {
		return nil, fail("INVALID_ARGUMENT", "pattern requires generate")
	}
	var idea, style string
	var bpm, bars int
	var approve bool
	if _, err := parsedFlags("pattern generate", args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&idea, "idea", "", "music idea")
		fs.StringVar(&style, "style", "", "style")
		fs.IntVar(&bpm, "bpm", 120, "BPM")
		fs.IntVar(&bars, "bars", 4, "number of bars")
		fs.BoolVar(&approve, "approve-paid", false, "allow a model API request")
	}); err != nil {
		return nil, err
	}
	if idea == "" {
		return nil, fail("INVALID_ARGUMENT", "--idea is required")
	}
	if !approve {
		return nil, fail("APPROVAL_REQUIRED", "AI Pattern generation requires --approve-paid")
	}
	return withStore(ctx, func(s *core.ProjectStore) (any, error) {
		return core.NewAiPattern(core.NewNativeSettings(s)).Generate(ctx, core.PatternInput{
			Idea: idea, Style: style, BPM: bpm, Bars: bars,
		})
	})
}

func runStory(ctx context.Context, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fail("INVALID_ARGUMENT", "story requires validate or build")
	}
	op := args[0]
	if op != "validate" && op != "build" {
		return nil, fail("INVALID_ARGUMENT", "unknown story command")
	}
	var input, output, projectID string
	if _, err := parsedFlags("story "+op, args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&input, "input", "", "story input JSON")
		fs.StringVar(&output, "output", "", "StoryPack JSON output")
		fs.StringVar(&projectID, "project", "", "optional creative project ID: build and archive the StoryPack")
	}); err != nil {
		return nil, err
	}
	if input == "" {
		return nil, fail("INVALID_ARGUMENT", "--input is required")
	}
	if projectID != "" {
		if op != "build" || output != "" {
			return nil, fail("INVALID_ARGUMENT", "--project is available for story build only; omit --output because Muse archives the result")
		}
		return buildProjectStoryPack(ctx, projectID, input)
	}
	if op == "build" && output == "" {
		return nil, fail("INVALID_ARGUMENT", "--output or --project is required for story build")
	}
	return runStoryBridge(ctx, op, input, output)
}

func runStoryBridge(ctx context.Context, op, input, output string) (any, error) {
	runner := os.Getenv("CREATIVE_STORY_RUNNER")
	if runner == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		runner = filepath.Join(cwd, "scripts", "story-cli.ts")
	}
	runner, err := filepath.Abs(runner)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(runner); err != nil || !info.Mode().IsRegular() {
		return nil, fail("RUNTIME_UNAVAILABLE", "StoryPack 源码未找到：请从 Muse 项目根目录运行，或设置 CREATIVE_STORY_RUNNER")
	}
	root := filepath.Dir(filepath.Dir(runner))
	for _, relative := range []string{
		"node_modules/zod/package.json",
		"node_modules/@aivn/story-schema/package.json",
		"node_modules/@aivn/validator/package.json",
		"node_modules/@aivn/storypack/package.json",
	} {
		if info, err := os.Stat(filepath.Join(root, relative)); err != nil || !info.Mode().IsRegular() {
			return nil, fail("RUNTIME_UNAVAILABLE", "StoryPack 依赖缺失：在 Muse 原生界面的「环境依赖」中点击安装，或运行 npm ci --ignore-scripts --no-audit --no-fund")
		}
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		home, _ := os.UserHomeDir()
		for _, path := range []string{filepath.Join(home, ".bun", "bin", "bun"), "/opt/homebrew/bin/bun", "/usr/local/bin/bun"} {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				bun = path
				break
			}
		}
	}
	if bun == "" {
		return nil, fail("RUNTIME_UNAVAILABLE", "Bun 未安装：在 Muse 原生界面的「环境依赖」中点击安装 Bun，或运行 brew install oven-sh/bun/bun")
	}
	inputPath, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	argv := []string{"run", runner, op, "--input", inputPath}
	if op == "build" {
		outputPath, err := filepath.Abs(output)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "--output", outputPath)
	}
	cmd := exec.CommandContext(ctx, bun, argv...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Bun's child output is parsed as a structured, bounded error.
		var typed struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(stderr.Bytes(), &typed) == nil && typed.Message != "" {
			return nil, fail("STORY_ERROR", typed.Message)
		}
		return nil, fail("STORY_ERROR", "story processing failed; check Bun and story input")
	}
	var result any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fail("STORY_ERROR", "story runner returned invalid JSON")
	}
	return result, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	data, err := run(ctx, os.Args[1:])
	if err != nil {
		code := "FAILED"
		var cli *cliError
		if errors.As(err, &cli) {
			code = cli.code
		}
		// Never print a provider response body or API key in an error.
		_ = writeJSON(os.Stderr, map[string]any{
			"ok":    false,
			"error": map[string]string{"code": code, "message": err.Error()},
		})
		os.Exit(1)
	}
	if err := writeJSON(os.Stdout, map[string]any{"ok": true, "data": data}); err != nil {
		fmt.Fprintln(os.Stderr, "failed to serialize result")
		os.Exit(1)
	}
}
