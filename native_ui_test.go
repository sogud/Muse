package main

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/sogud/Muse/internal/core"
)

func nativeFixture(t *testing.T) (*nativeUI, *core.ProjectStore) {
	t.Helper()
	t.Setenv("OTO_USER_DATA_DIR", t.TempDir())
	store, err := core.OpenProjectStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.CloseProjectStore(store) })
	return newNativeUI(core.NewCreativeWorkspace(store), core.NewNativeSettings(store)), store
}

func TestNativeUIManagementNavigation(t *testing.T) {
	state, _ := nativeFixture(t)
	tester := ui.NewTester(state.view, 1100, 740)
	if !tester.HasText("项目概览") || !tester.HasText("还没有项目。") {
		t.Fatalf("empty state should guide users to their Agent: %q", tester.Texts())
	}
	if tester.HasText("创作需求") || tester.HasText("保存需求") || tester.HasText("创建项目") {
		t.Fatal("management GUI must not expose chat or project authoring inputs")
	}
	if len(state.projects) != 0 || len(state.jobs) != 0 {
		t.Fatal("management GUI must not create sample data")
	}

	project, err := state.creative.CreateProject(context.Background(), core.NewCreativeProject{
		Title: "夜雨配乐", Kind: "music",
	})
	if err != nil {
		t.Fatal(err)
	}
	noApproval := false
	_, err = state.creative.CreateJob(context.Background(), core.NewCreativeJob{
		ProjectID:        project.ID,
		Kind:             "creative.request",
		Input:            json.RawMessage([]byte("{\"prompt\":\"为第三章做一段悬疑音乐\"}")),
		RequiresApproval: &noApproval,
	})
	if err != nil {
		t.Fatal(err)
	}
	state.refresh()
	tester = ui.NewTester(state.view, 1100, 740)
	if len(state.jobs) != 1 || !tester.HasText("夜雨配乐") {
		t.Fatalf("shared Core records should appear in the GUI: %q", tester.Texts())
	}
	if err := tester.Click("任务记录"); err != nil {
		t.Fatal(err)
	}
	if state.page != "jobs" || !tester.HasText("为第三章做一段悬疑音乐") {
		t.Fatalf("task monitoring navigation failed: %q", tester.Texts())
	}
	if err := tester.Click("我的作品"); err != nil {
		t.Fatal(err)
	}
	if state.page != "results" {
		t.Fatalf("artifact navigation failed: %s", state.page)
	}
	if err := tester.Click("设置"); err != nil {
		t.Fatal(err)
	}
	if state.page != "settings" || !tester.HasText("模型连接") {
		t.Fatalf("settings navigation failed: %q", tester.Texts())
	}
	if err := tester.Click("环境依赖"); err != nil {
		t.Fatal(err)
	}
	if state.settingsTab != 1 || !tester.HasText("本机运行环境") {
		t.Fatalf("dependency settings navigation failed: %q", tester.Texts())
	}
	if len(state.jobs) != 1 {
		t.Fatal("navigating the GUI must not create creative jobs")
	}
}

func TestNativeUIExistingArtifactReview(t *testing.T) {
	state, _ := nativeFixture(t)
	project, err := state.creative.CreateProject(context.Background(), core.NewCreativeProject{
		Title: "03:17 配乐", Kind: "story",
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "test.json")
	if err := os.WriteFile(file, []byte(`{"sample":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	artifact, err := state.creative.AddArtifact(context.Background(), core.NewCreativeArtifact{
		ProjectID: project.ID, SourcePath: file, Kind: "document",
	})
	if err != nil {
		t.Fatal(err)
	}
	state.refresh()
	state.page = "results"
	tester := ui.NewTester(state.view, 1100, 740)
	if !tester.HasText("test.json") {
		t.Fatalf("artifact was not listed in native view: %q", tester.Texts())
	}
	if err := tester.Click("采用"); err != nil {
		t.Fatal(err)
	}
	after, err := state.creative.GetArtifact(context.Background(), artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ReviewStatus != "approved" {
		t.Fatalf("native review should update the same DB: %+v", after)
	}
}

func TestNativeUIScreenshot(t *testing.T) {
	output := os.Getenv("MUSE_NATIVE_SNAPSHOT")
	if output == "" {
		t.Skip("Set MUSE_NATIVE_SNAPSHOT to a path to render a local preview")
	}
	state, _ := nativeFixture(t)
	project, err := state.creative.CreateProject(context.Background(), core.NewCreativeProject{
		Title: "03:17 电影配乐", Kind: "music",
	})
	if err != nil {
		t.Fatal(err)
	}
	noApproval := false
	payload, _ := json.Marshal(map[string]string{
		"prompt": "给第三章制作 60 秒的悬疑配乐，没有人声",
	})
	if _, err := state.creative.CreateJob(context.Background(), core.NewCreativeJob{
		ProjectID: project.ID, Kind: "creative.request", Input: payload,
		RequiresApproval: &noApproval,
	}); err != nil {
		t.Fatal(err)
	}
	state.refresh()
	switch os.Getenv("MUSE_NATIVE_SNAPSHOT_PAGE") {
	case "jobs":
		state.page = "jobs"
	case "results":
		state.page = "results"
	case "settings":
		state.page = "settings"
	case "dependencies":
		state.page = "settings"
		state.settingsTab = 1
	}
	tester := ui.NewTester(state.view, 1100, 740)
	if os.Getenv("MUSE_NATIVE_DARK") == "1" {
		tester.SetDark(true)
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, tester.Image()); err != nil {
		t.Fatal(err)
	}
}
