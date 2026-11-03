package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sogud/Muse/internal/core"
)

func testComposition() core.Composition {
	track := func(id, role, kind string, channel, program int) core.CompositionTrack {
		return core.CompositionTrack{
			ID: id, Type: kind, Name: role, Role: role, Channel: channel,
			Program: program, Volume: 95, Pan: 0,
			Notes: []core.Note{{Pitch: 60, Start: 0, Duration: 1, Velocity: 90}},
		}
	}
	return core.Composition{
		Version: "1.0", ID: "test-composition", Title: "Offline test",
		BPM: 100, TimeSignature: [2]int{4, 4}, Key: "C major", Bars: 4,
		Tracks: []core.CompositionTrack{
			track("drums", "drums", "drums", 9, 0),
			track("bass", "bass", "instrument", 0, 33),
			track("chords", "chords", "instrument", 1, 0),
			track("melody", "melody", "instrument", 2, 10),
		},
		CreatedAt: 1, UpdatedAt: 1,
	}
}

func TestCLIDoesNotNeedGUI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OTO_USER_DATA_DIR", dir)
	t.Setenv("MUREKA_API_KEY", "")
	ctx := context.Background()

	list, err := run(ctx, []string{"project", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.([]core.Project)) != 0 {
		t.Fatal("new database should be empty")
	}

	input := filepath.Join(dir, "source-composition.json")
	data, _ := json.Marshal(testComposition())
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}

	created, err := run(ctx, []string{"project", "create", "--file", input})
	if err != nil {
		t.Fatal(err)
	}
	project := created.(core.Project)
	if project.ID == "" {
		t.Fatal("created project has no ID")
	}

	got, err := run(ctx, []string{"project", "get", "--id", project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.(*core.Project).ID != project.ID {
		t.Fatal("loaded wrong project")
	}

	list, err = run(ctx, []string{"project", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.([]core.Project)) != 1 {
		t.Fatal("created project not persisted")
	}

	if _, err = run(ctx, []string{"project", "delete", "--id", project.ID}); err == nil {
		t.Fatal("delete without --confirm should be rejected")
	}

	_, err = run(ctx, []string{"project", "delete", "--id", project.ID, "--confirm"})
	if err != nil {
		t.Fatal(err)
	}

	list, err = run(ctx, []string{"project", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.([]core.Project)) != 0 {
		t.Fatal("deleted project persists")
	}
}

func TestPaidGenerationRequiresApproval(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MUREKA_API_KEY", "")
	if _, err := run(ctx, []string{"music", "generate", "--prompt", "demo"}); err == nil {
		t.Fatal("paid API calls must require explicit approval")
	} else if e, ok := err.(*cliError); !ok || e.code != "APPROVAL_REQUIRED" {
		t.Fatalf("expected approval gate: %v", err)
	}
	dry, err := run(ctx, []string{"music", "generate", "--prompt", "demo", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if dry.(map[string]any)["networkCalled"] != false {
		t.Fatal("dry-run attempted a network call")
	}
	if _, err := run(ctx, []string{"pattern", "generate", "--idea", "quiet piano"}); err == nil {
		t.Fatal("pattern AI must require approval")
	}
}
