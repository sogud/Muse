package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/sogud/Muse/internal/core"
)

// nativeUI is the sole Go-native MyGo view sharing CreativeWorkspace
// with the headless CLI. It needs no additional database or WebView.
type nativeUI struct {
	window   *mygo.Window
	creative *core.CreativeWorkspace
	settings *core.NativeSettings
	runner   *core.JobRuntime

	page               string
	projects           []core.CreativeProject
	selectedID         string
	jobs               []core.CreativeJob
	artifacts          []core.CreativeArtifact
	selectedJobID      string
	jobDetail          *core.JobExecution
	selectedArtifactID string
	lastSynced         time.Time

	artifactFilter int
	settingsTab    int

	provider string
	model    string
	endpoint string
	apiKey   string

	busy    bool
	message string
	err     string

	dependencies      dependencyStatus
	dependencyBusy    bool
	dependencyMessage string
}

func newNativeUI(w *core.CreativeWorkspace, settings *core.NativeSettings) *nativeUI {
	a := &nativeUI{
		creative: w, settings: settings, runner: core.NewJobRuntime(w),
		page: "overview", provider: "local",
	}
	a.refresh()
	a.loadSettings()
	a.dependencies = inspectDependencies("")
	return a
}

func (a *nativeUI) refresh() {
	projects, err := a.creative.ListProjects(context.Background())
	if err != nil {
		a.err = err.Error()
		return
	}
	a.projects = projects
	found := false
	for _, p := range projects {
		if p.ID == a.selectedID {
			found = true
			break
		}
	}
	if !found {
		a.selectedID = ""
		if len(projects) > 0 {
			a.selectedID = projects[0].ID
		}
	}
	if a.reloadSelected() {
		a.lastSynced = time.Now()
	}
}

func (a *nativeUI) reloadSelected() bool {
	if a.selectedID == "" {
		a.jobs, a.artifacts = nil, nil
		a.selectedJobID = ""
		a.jobDetail = nil
		a.selectedArtifactID = ""
		return true
	}
	ctx := context.Background()
	jobs, err := a.creative.ListJobs(ctx, a.selectedID)
	if err != nil {
		a.err = err.Error()
		return false
	}
	artifacts, err := a.creative.ListArtifacts(ctx, a.selectedID)
	if err != nil {
		a.err = err.Error()
		return false
	}
	a.jobs, a.artifacts = jobs, artifacts

	jobExists := false
	for _, job := range jobs {
		if job.ID == a.selectedJobID {
			jobExists = true
			break
		}
	}
	if !jobExists {
		a.selectedJobID = ""
		if len(jobs) > 0 {
			a.selectedJobID = jobs[0].ID
		}
	}
	a.jobDetail = nil
	if a.selectedJobID != "" {
		detail, err := a.runner.Status(ctx, a.selectedJobID)
		if err != nil {
			a.err = "任务详情同步失败：" + err.Error()
			return false
		} else {
			a.jobDetail = &detail
		}
	}

	artifactExists := false
	for _, artifact := range artifacts {
		if artifact.ID == a.selectedArtifactID {
			artifactExists = true
			break
		}
	}
	if !artifactExists {
		a.selectedArtifactID = ""
		if len(artifacts) > 0 {
			a.selectedArtifactID = artifacts[0].ID
		}
	}
	return true
}

// startLocalSync refreshes only the local SQLite-backed view. It never
// polls a provider, charges a model, or changes project records.
func (a *nativeUI) startLocalSync(window *mygo.Window) {
	done := make(chan struct{})
	window.OnClosed(func() { close(done) })
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if window.IsDestroyed() {
					return
				}
				window.Update(func() {
					if !window.IsDestroyed() {
						a.refresh()
					}
				})
			}
		}
	}()
}

func (a *nativeUI) currentProject() *core.CreativeProject {
	for i := range a.projects {
		if a.projects[i].ID == a.selectedID {
			return &a.projects[i]
		}
	}
	return nil
}

func (a *nativeUI) reviewArtifact(id, decision string) {
	a.err, a.message = "", ""
	_, err := a.creative.ReviewArtifact(context.Background(),
		core.CreativeArtifactReview{ID: id, Decision: decision})
	if err != nil {
		a.err = err.Error()
		return
	}
	a.reloadSelected()
	if decision == "approved" {
		a.message = "作品已标记为采用（仅本地审核）。"
	} else {
		a.message = "作品已标记为不采用，文件仍然保留。"
	}
}

func (a *nativeUI) importFile() {
	if a.window == nil || a.selectedID == "" || a.busy {
		return
	}
	a.busy, a.err, a.message = true, "", ""
	window, projectID := a.window, a.selectedID
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: window, Title: "导入本机作品", Multiple: false,
		})
		name, importedID := "", ""
		if err == nil && len(paths) > 0 {
			artifact, addErr := a.creative.AddArtifact(context.Background(), core.NewCreativeArtifact{
				ProjectID: projectID, SourcePath: paths[0], Kind: "other",
			})
			err, name = addErr, artifact.Name
			if addErr == nil {
				importedID = artifact.ID
			}
		}
		window.Update(func() {
			a.busy = false
			if err != nil {
				a.err = err.Error()
			} else if len(paths) > 0 {
				if a.selectedID == projectID {
					a.selectedArtifactID = importedID
					a.reloadSelected()
				}
				a.message = "已保存到项目：" + name
			}
		})
	}()
}

func (a *nativeUI) revealArtifact(id string) {
	a.err, a.message = "", ""
	if err := a.creative.RevealArtifact(context.Background(), id); err != nil {
		a.err = err.Error()
	}
}

func (a *nativeUI) loadSettings() {
	if a.settings == nil {
		return
	}
	values, err := a.settings.GetAll(context.Background())
	if err != nil {
		a.err = err.Error()
		return
	}
	a.provider = values["ai.provider"]
	switch a.provider {
	case "local", "openai", "google", "anthropic", "deepseek":
	default:
		a.provider = "local"
	}
	a.model, a.endpoint = values["ai.model"], values["ai.baseUrl"]
}

func (a *nativeUI) saveSettings() {
	a.err, a.message = "", ""
	if a.settings == nil {
		a.err = "设置服务不可用。"
		return
	}
	err := a.settings.ConfigureAi(context.Background(), core.AIConfiguration{
		Provider: a.provider, Model: strings.TrimSpace(a.model),
		BaseURL: strings.TrimSpace(a.endpoint), APIKey: a.apiKey,
	})
	if err != nil {
		a.err = err.Error()
		return
	}
	a.apiKey = ""
	a.message = "模型配置已保存，尚未调用任何模型。"
}

// syncJob only reads an already-submitted provider task and archives the
// result. It cannot submit or authorize a new paid operation.
func (a *nativeUI) syncJob(id string) {
	if a.window == nil || a.busy {
		return
	}
	a.busy = true
	win := a.window
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		result, err := a.runner.Sync(ctx, id)
		win.Update(func() {
			a.busy = false
			if err != nil {
				a.err = "状态更新失败：" + err.Error()
				return
			}
			a.err = ""
			a.reloadSelected()
			a.message = "任务状态：" + nativeJobStatus(result.Job)
		})
	}()
}

func nativeShort(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return string(runes)
}

func nativeJobTitle(job core.CreativeJob) string {
	var input map[string]any
	if json.Unmarshal(job.Input, &input) == nil {
		if prompt, ok := input["prompt"].(string); ok && prompt != "" {
			return prompt
		}
	}
	switch job.Kind {
	case "story.build":
		return "StoryPack 构建"
	case "music.generate":
		return "音乐生成"
	case "creative.request":
		return "创作需求"
	default:
		return job.Kind
	}
}

func nativeJobStatus(job core.CreativeJob) string {
	switch job.Status {
	case "succeeded":
		return "已完成"
	case "processing":
		return "处理中"
	case "submitted":
		return "已提交"
	case "awaiting_archive":
		return "待归档"
	case "archiving":
		return "归档中"
	case "failed":
		return "执行失败"
	case "needs_reconciliation", "submitting":
		return "需人工核对"
	case "cancelled":
		return "已取消"
	case "draft":
		switch job.ApprovalStatus {
		case "approved":
			return "已批准 · 未执行"
		case "rejected":
			return "已拒绝"
		default:
			return "草稿 · 未执行"
		}
	default:
		return job.Status
	}
}

func (a *nativeUI) view(c *ui.Context) {
	// The native app manages existing Agent/CLI work. It is not a chat
	// client, prompt editor, or alternative execution engine.
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(216).Shrink(0).Padding(12).Gap(10).Children(func() {
			ui.Text(c, "Muse").FontSize(18).Bold().Padding(6, 4)
			if ui.Sidebar(c, &a.page, func() {
				ui.SidebarSection(c, "管理", nil, func() {
					ui.SidebarItem(c, "overview", nil, "项目概览")
					ui.SidebarItem(c, "jobs", nil, "任务记录")
					ui.SidebarItem(c, "results", nil, "我的作品")
					ui.SidebarItem(c, "settings", nil, "设置")
				})
				ui.SidebarSection(c, "项目", nil, func() {
					for _, project := range a.projects {
						ui.SidebarItem(c, "project:"+project.ID, nil, nativeShort(project.Title, 22))
					}
				})
			}).Grow(1).Changed() {
				a.err = ""
				a.message = ""
				if strings.HasPrefix(a.page, "project:") {
					id := strings.TrimPrefix(a.page, "project:")
					if id != a.selectedID {
						a.selectedID = id
						a.reloadSelected()
					}
				}
			}
			ui.Text(c, "项目和任务由 Agent / CLI 创建。").FontSize(12).Wrap()
		})

		ui.Divider(c)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Scroll(c).Grow(1).Padding(24).Gap(18).Children(func() {
				ui.Column(c).MaxWidth(920).Gap(18).Children(func() {
					switch a.page {
					case "jobs":
						a.jobsView(c)
					case "results":
						a.resultsView(c)
					case "settings", "dependencies":
						a.settingsView(c)
					default:
						a.startView(c)
					}
				})
			})
			if a.err != "" || a.message != "" {
				ui.Divider(c)
				ui.Row(c).Padding(11, 24).Children(func() {
					if a.err != "" {
						ui.Text(c, "操作失败："+a.err).Wrap()
					} else {
						ui.Text(c, a.message).Wrap()
					}
				})
			}
		})
	})
}

func (a *nativeUI) header(c *ui.Context, title, subtitle string) {
	ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Text(c, title).FontSize(22).Bold()
			ui.Text(c, subtitle).FontSize(13).Wrap()
		})
		if !a.lastSynced.IsZero() {
			ui.Text(c, "本地自动同步 · "+a.lastSynced.Format("15:04:05")).FontSize(11)
		}
		if ui.Button(c, "刷新").Clicked() {
			a.refresh()
			a.message = "已重新读取本地项目、任务与作品。"
		}
	})
	ui.Divider(c)
}

func (a *nativeUI) exportProjectManifest() {
	if a.selectedID == "" {
		a.err = "请先从左侧选择项目。"
		return
	}
	path, err := a.creative.ExportManifest(context.Background(), a.selectedID)
	if err != nil {
		a.err = err.Error()
		return
	}
	a.err = ""
	a.message = "项目清单已导出到本机：" + path
}

func nativeProjectKind(kind string) string {
	switch kind {
	case "story":
		return "剧情"
	case "music":
		return "音乐"
	case "visual":
		return "图像"
	case "audio":
		return "音频"
	default:
		return "综合创作"
	}
}

// startView is now a project overview only. Authoring and generation
// belong to the external Agent using Muse's headless CLI.
func (a *nativeUI) startView(c *ui.Context) {
	project := a.currentProject()
	if project == nil {
		a.header(c, "项目概览", "查看 Agent 创建的项目、执行任务和保存的作品。")
		ui.Text(c, "还没有项目。").FontSize(17).Bold()
		ui.Text(c, "请在 Codex 等外部 Agent 中使用 Muse Skill 建立项目；完成后点击右上角的「刷新」。").Wrap()
		ui.Text(c, "本应用不提供聊天、创作提示词输入或自动付费执行入口。").Wrap()
		return
	}

	a.header(c, "项目概览", project.Title)
	ui.Column(c).Gap(10).Children(func() {
		ui.Text(c, "项目信息").FontSize(17).Bold()
		ui.Text(c, "类型："+nativeProjectKind(project.Kind))
		if project.Description != "" {
			ui.Text(c, project.Description).Wrap()
		}
		ui.Text(c, fmt.Sprintf("任务 %d 项  ·  作品 %d 件", len(a.jobs), len(a.artifacts)))
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "查看任务").Clicked() {
				a.page = "jobs"
			}
			if ui.Button(c, "查看作品").Clicked() {
				a.page = "results"
			}
			if ui.Button(c, "导出项目清单").Clicked() {
				a.exportProjectManifest()
			}
		})
	})
	ui.Divider(c)
	ui.Text(c, "最近任务").FontSize(17).Bold()
	if len(a.jobs) == 0 {
		ui.Text(c, "还没有任务。Agent 创建后会出现在这里。").Wrap()
		return
	}
	limit := min(len(a.jobs), 5)
	ui.List(c, nil, limit, func(i int) {
		job := a.jobs[i]
		ui.Row(c).AlignItems(ui.Center).Gap(12).Padding(8, 4).Children(func() {
			ui.Text(c, nativeShort(nativeJobTitle(job), 92)).Grow(1).MaxLines(2)
			ui.Text(c, nativeJobStatus(job)).FontSize(12)
		})
	}).Height(float32(min(limit*58, 290))).FillWidth()
}

func (a *nativeUI) inspectJob(id string) {
	a.selectedJobID = id
	detail, err := a.runner.Status(context.Background(), id)
	if err != nil {
		a.jobDetail = nil
		a.err = "读取任务详情失败：" + err.Error()
		return
	}
	a.jobDetail = &detail
	a.err = ""
}

func nativeApprovalLabel(status string) string {
	switch status {
	case "approved":
		return "已批准"
	case "pending":
		return "待批准"
	case "rejected":
		return "已拒绝"
	case "not_required":
		return "无需付费审批"
	default:
		return status
	}
}

func nativeTimestamp(ms int64) string {
	if ms <= 0 {
		return "尚无记录"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04:05")
}

func nativeMoney(micros int64, currency string) string {
	return fmt.Sprintf("%.3f %s", float64(micros)/1_000_000, currency)
}

func (a *nativeUI) jobDetailView(c *ui.Context) {
	ui.Text(c, "任务详情").FontSize(17).Bold()
	if a.jobDetail == nil {
		ui.Text(c, "选择一条任务以查看执行记录、错误原因和关联作品。").Wrap()
		return
	}
	detail := a.jobDetail
	job := detail.Job
	ui.Text(c, nativeShort(nativeJobTitle(job), 140)).Bold().Wrap()
	ui.Text(c, "状态："+nativeJobStatus(job))
	origin := job.Provider
	if origin == "" {
		origin = "本地草稿"
	}
	ui.Text(c, "类型："+nativeJobKind(job.Kind)+" · 来源："+origin).Wrap()
	ui.Text(c, "审批："+nativeApprovalLabel(job.ApprovalStatus))
	ui.Text(c, "创建时间："+nativeTimestamp(job.CreatedAt)).FontSize(12)
	ui.Text(c, "最近更新："+nativeTimestamp(job.UpdatedAt)).FontSize(12)
	ui.Text(c, "任务 ID："+job.ID).Selectable().Wrap().FontSize(12)

	if job.Error != "" {
		ui.Divider(c)
		ui.Text(c, "错误与处理提示").Bold()
		ui.Text(c, job.Error).Wrap()
	}
	if job.Status == "needs_reconciliation" || job.Status == "submitting" {
		ui.Text(c, "远端可能已接收这次付费请求。请通过 Agent 核对原任务 ID，切勿重复提交。").Wrap()
	}
	if detail.Receipt != nil {
		receipt := detail.Receipt
		ui.Divider(c)
		ui.Text(c, "Provider 回执").Bold()
		ui.Text(c, "远端任务 ID："+receipt.ProviderTaskID).Selectable().Wrap().FontSize(12)
		ui.Text(c, "远端状态："+receipt.ProviderState)
		ui.Text(c, "最后检查："+nativeTimestamp(receipt.LastCheckedAt)).FontSize(12)
		if receipt.LastError != "" {
			ui.Text(c, "最近提示："+receipt.LastError).Wrap()
		}
	}

	if job.EstimatedCostMicros != nil || job.MaxCostMicros != nil {
		ui.Divider(c)
		ui.Text(c, "费用记录").Bold()
		if job.EstimatedCostMicros != nil {
			ui.Text(c, "用户估算："+nativeMoney(*job.EstimatedCostMicros, job.Currency))
		}
		if job.MaxCostMicros != nil {
			ui.Text(c, "用户预算字段："+nativeMoney(*job.MaxCostMicros, job.Currency))
		}
		ui.Text(c, "预算尚未由 Provider 保证或强制执行；账单费用未对接。").Wrap().FontSize(12)
	} else if job.Provider != "" && job.Provider != "local" {
		ui.Text(c, "尚未核实这次调用的实际费用。").FontSize(12)
	}
	if detail.Receipt != nil && detail.Receipt.ActualCostMicros != nil {
		ui.Text(c, "记录的费用："+nativeMoney(*detail.Receipt.ActualCostMicros, job.Currency)).FontSize(12)
	}
	if len(detail.Artifacts) > 0 {
		ui.Divider(c)
		ui.Text(c, fmt.Sprintf("关联作品（%d）", len(detail.Artifacts))).Bold()
		for i, artifact := range detail.Artifacts {
			if i >= 3 {
				break
			}
			ui.Text(c, nativeShort(artifact.Name, 90)).Wrap()
		}
		if ui.Button(c, "在作品库查看").Clicked() {
			a.selectedArtifactID = detail.Artifacts[0].ID
			a.page = "results"
		}
	}
}

func (a *nativeUI) jobsView(c *ui.Context) {
	project := a.currentProject()
	subtitle := "记录来自外部 Agent 和 Muse CLI；这里只读取本地状态。"
	if project != nil {
		subtitle = fmt.Sprintf("%s · %d 项任务", project.Title, len(a.jobs))
	}
	a.header(c, "任务记录", subtitle)
	if project == nil {
		ui.Text(c, "请从侧栏选择项目；创建和执行仍由外部 Agent 完成。").Wrap()
		return
	}
	if len(a.jobs) == 0 {
		ui.Text(c, "还没有任务。Agent 提交后会自动出现在这里。").Wrap()
		return
	}
	ui.Row(c).Gap(22).AlignItems(ui.Start).Wrap().Children(func() {
		ui.Column(c).Width(360).Shrink(0).Gap(10).Children(func() {
			ui.Text(c, "任务列表").FontSize(17).Bold()
			ui.List(c, nil, len(a.jobs), func(i int) {
				job := a.jobs[i]
				ui.Column(c).Padding(8, 4).Gap(6).Children(func() {
					ui.Text(c, nativeShort(nativeJobTitle(job), 84)).MaxLines(2).Wrap()
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, nativeJobStatus(job)).FontSize(12).Grow(1)
						if ui.Button(c, "详情").Clicked() {
							a.inspectJob(job.ID)
						}
					})
				})
			}).Height(float32(min(len(a.jobs)*82, 414))).FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(300).Gap(10).Children(func() {
			a.jobDetailView(c)
		})
	})
	ui.Divider(c)
	ui.Text(c, "自动同步仅查看本地记录，不会自动向 Provider 发起请求或产生费用。").FontSize(12).Wrap()
	if a.jobDetail != nil {
		switch a.jobDetail.Job.Status {
		case "submitted", "processing", "awaiting_archive", "archiving":
			if ui.Button(c, "手动查询 Provider 状态").Disabled(a.busy).Clicked() {
				a.syncJob(a.jobDetail.Job.ID)
			}
		}
	}
}

func nativeJobKind(kind string) string {
	switch kind {
	case "creative.request":
		return "创作草稿（旧记录）"
	case "music.generate":
		return "音乐生成"
	case "story.build":
		return "StoryPack 构建"
	default:
		return kind
	}
}

func nativeReviewStatus(status string) string {
	switch status {
	case "approved":
		return "已采用"
	case "rejected":
		return "不采用"
	default:
		return "待选择"
	}
}

func nativeArtifactKind(kind string) string {
	switch kind {
	case "audio":
		return "音频"
	case "image":
		return "图片"
	case "video":
		return "视频"
	case "story":
		return "剧情素材"
	case "document":
		return "文档"
	default:
		return "其他文件"
	}
}

func (a *nativeUI) previewArtifact(id string) {
	if a.window == nil || a.busy {
		return
	}
	a.busy = true
	win := a.window
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := a.creative.OpenArtifact(ctx, id)
		win.Update(func() {
			a.busy = false
			if err != nil {
				a.err = "无法预览：" + err.Error()
			} else {
				a.err = ""
			}
		})
	}()
}

func (a *nativeUI) exportArtifact(artifact core.CreativeArtifact) {
	if a.window == nil || a.busy {
		return
	}
	a.busy = true
	win := a.window
	go func() {
		destination, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      win,
			Title:       "导出作品：" + artifact.Name,
			DefaultPath: artifact.Name,
			ButtonLabel: "导出",
		})
		var result string
		if err == nil && destination != "" {
			result, err = a.creative.ExportArtifact(context.Background(), artifact.ID, destination)
		}
		win.Update(func() {
			a.busy = false
			if err != nil {
				a.err = "无法导出作品：" + err.Error()
				a.message = "导出不会覆盖已有文件，请选择一个新文件名。"
			} else if result != "" {
				a.err = ""
				a.message = "已验证 SHA-256 并导出到：" + result
			}
		})
	}()
}

func (a *nativeUI) artifactDetailView(c *ui.Context, selected *core.CreativeArtifact) {
	ui.Text(c, "作品详情").FontSize(17).Bold()
	if selected == nil {
		ui.Text(c, "选择左侧作品，查看预览、审核、导出与来源记录。").Wrap()
		return
	}
	artifact := *selected
	ui.Text(c, artifact.Name).Bold().Wrap()
	ui.Text(c, fmt.Sprintf("%s · %s · %.1f KB",
		nativeArtifactKind(artifact.Kind), nativeReviewStatus(artifact.ReviewStatus),
		float64(artifact.SizeBytes)/1024)).FontSize(12).Wrap()
	ui.Row(c).Gap(8).Wrap().Children(func() {
		if ui.Button(c, "打开预览").Disabled(a.busy).Clicked() {
			a.previewArtifact(artifact.ID)
		}
		if ui.Button(c, "导出…").Disabled(a.busy).Clicked() {
			a.exportArtifact(artifact)
		}
		if ui.Button(c, "Finder 定位").Clicked() {
			a.revealArtifact(artifact.ID)
		}
	})
	ui.Text(c, "预览将使用 macOS 默认应用打开；导出会验证哈希且拒绝覆盖已有文件。").
		FontSize(12).Wrap()
	ui.Divider(c)
	ui.Text(c, "人工审核").Bold()
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "采用").
			Disabled(artifact.ReviewStatus == "approved").Clicked() {
			a.reviewArtifact(artifact.ID, "approved")
		}
		if ui.Button(c, "不采用").
			Disabled(artifact.ReviewStatus == "rejected").Clicked() {
			a.reviewArtifact(artifact.ID, "rejected")
		}
	})
	if artifact.ReviewNote != "" {
		ui.Text(c, "审核备注："+artifact.ReviewNote).Wrap()
	}
	ui.Divider(c)
	ui.Text(c, "文件来源").Bold()
	provider := artifact.Provider
	if provider == "" {
		provider = "未记录"
	}
	ui.Text(c, "Provider："+provider)
	ui.Text(c, "MIME："+artifact.MimeType).Wrap().FontSize(12)
	ui.Text(c, "归档时间："+nativeTimestamp(artifact.CreatedAt)).FontSize(12)
	if artifact.JobID != "" {
		ui.Text(c, "关联任务："+artifact.JobID).Selectable().Wrap().FontSize(12)
		if ui.Button(c, "查看关联任务").Clicked() {
			a.inspectJob(artifact.JobID)
			a.page = "jobs"
		}
	}
	ui.Text(c, "SHA-256："+artifact.SHA256).Selectable().Wrap().FontSize(12)
	if artifact.LicenseNote != "" {
		ui.Text(c, "授权声明："+artifact.LicenseNote).Wrap().FontSize(12)
	} else {
		ui.Text(c, "未记录素材授权说明，请自行核实可使用范围。").Wrap().FontSize(12)
	}
}

func (a *nativeUI) resultsView(c *ui.Context) {
	project := a.currentProject()
	subtitle := "本地作品及审核结果，可在系统应用中预览。"
	if project != nil {
		subtitle = fmt.Sprintf("%s · %d 件作品", project.Title, len(a.artifacts))
	}
	a.header(c, "我的作品", subtitle)
	if project == nil {
		ui.Text(c, "请先从侧栏选择一个项目。").Wrap()
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		if ui.Segmented(c, &a.artifactFilter, "全部", "待审核", "已采用", "不采用").
			Label("作品筛选").Changed() {
			a.selectedArtifactID = ""
		}
		ui.Spacer(c)
		if ui.PrimaryButton(c, "导入本地文件…").Disabled(a.busy).Clicked() {
			a.importFile()
		}
	})

	visible := make([]core.CreativeArtifact, 0, len(a.artifacts))
	for _, artifact := range a.artifacts {
		switch a.artifactFilter {
		case 1:
			if artifact.ReviewStatus != "pending" {
				continue
			}
		case 2:
			if artifact.ReviewStatus != "approved" {
				continue
			}
		case 3:
			if artifact.ReviewStatus != "rejected" {
				continue
			}
		}
		visible = append(visible, artifact)
	}
	if len(visible) == 0 {
		if len(a.artifacts) == 0 {
			ui.Text(c, "目前没有作品。Agent 归档或手动导入后，这里会自动显示。").Wrap()
		} else {
			ui.Text(c, "当前筛选条件下没有作品。").Wrap()
		}
		return
	}

	var selected *core.CreativeArtifact
	for i := range visible {
		if visible[i].ID == a.selectedArtifactID {
			selected = &visible[i]
			break
		}
	}
	if selected == nil {
		a.selectedArtifactID = visible[0].ID
		selected = &visible[0]
	}

	ui.Row(c).Gap(22).AlignItems(ui.Start).Wrap().Children(func() {
		ui.Column(c).Width(360).Shrink(0).Gap(10).Children(func() {
			ui.Text(c, "作品列表").FontSize(17).Bold()
			ui.List(c, nil, len(visible), func(i int) {
				artifact := visible[i]
				ui.Column(c).Padding(8, 4).Gap(6).Children(func() {
					ui.Text(c, nativeShort(artifact.Name, 95)).MaxLines(2).Wrap()
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, nativeReviewStatus(artifact.ReviewStatus)).
							Grow(1).FontSize(12)
						if ui.Button(c, "详情").Clicked() {
							a.selectedArtifactID = artifact.ID
						}
					})
				})
			}).Height(float32(min(len(visible)*82, 410))).FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(300).Gap(10).Children(func() {
			a.artifactDetailView(c, selected)
		})
	})
}

func (a *nativeUI) settingsView(c *ui.Context) {
	a.header(c, "设置", "管理模型连接与本机运行环境。")
	ui.Tabs(c, &a.settingsTab, "模型连接", "环境依赖")
	ui.Divider(c)
	if a.settingsTab == 1 {
		a.dependenciesView(c)
		return
	}

	ui.Column(c).MaxWidth(680).Gap(12).Children(func() {
		ui.Text(c, "Provider 状态").FontSize(17).Bold()
		ui.Text(c, "以下只检查本机配置，不会验证网络、额度或产生费用。").FontSize(12).Wrap()
		for _, provider := range a.settings.ListProviderStatuses() {
			status := "未配置"
			if provider.Configured {
				status = "已配置 · 未连接验证"
			}
			ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Text(c, provider.Label).Bold().Grow(1)
				ui.Text(c, status).FontSize(12)
			})
			ui.Text(c, provider.Detail).FontSize(12).Wrap()
			ui.Divider(c)
		}
		ui.Text(c, "音乐 Provider 分别读取各自进程环境变量中的 API Key；下方文本模型 Key 不用于音乐生成。").FontSize(12).Wrap()
	})

	ui.Column(c).MaxWidth(680).Gap(14).Children(func() {
		ui.Text(c, "文本模型 · Pattern DSL").FontSize(17).Bold()
		ui.Text(c, "按需填写已有的模型服务。保存设置不会调用付费 API。").Wrap()
		ui.Form(c, func() {
			ui.Field(c, "服务", func() {
				ui.Select(c, &a.provider,
					[]string{"local", "openai", "google", "anthropic", "deepseek"})
			})
			ui.Field(c, "模型", func() {
				ui.TextInput(c, &a.model).Placeholder("模型 ID")
			})
			ui.Field(c, "接口地址", func() {
				ui.TextInput(c, &a.endpoint).Placeholder("http://127.0.0.1:3425/v1")
			})
			ui.Field(c, "API Key", func() {
				ui.TextInput(c, &a.apiKey).Password().
					Placeholder("已保存则可留空")
			})
		})
		ui.Row(c).Children(func() {
			if ui.PrimaryButton(c, "保存模型配置").
				Disabled(strings.TrimSpace(a.model) == "").Clicked() {
				a.saveSettings()
			}
		})
		ui.Divider(c)
		ui.Text(c, "注意：目前 API Key 仍保存在本机旧版 SQLite 中，Keychain 迁移尚未完成。").
			FontSize(12).Wrap()
	})
}
