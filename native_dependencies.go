package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// dependencyStatus is a read-only snapshot. Installation is never started
// during app startup, dependency checks or view rendering.
type dependencyStatus struct {
	root          string
	bun           string
	node          string
	npm           string
	brew          string
	storyPackages bool
	fluidSynth    bool
	soundFont     bool
}

func toolDirectories() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, "n", "bin"),
		filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}
}

func findExecutable(name string) string {
	// Only use known Homebrew installation paths for executable installs;
	// PATH may contain project-local binaries which are not trusted installers.
	if name == "brew" {
		for _, path := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
			info, err := os.Stat(path)
			if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				return path
			}
		}
		return ""
	}
	if result, err := exec.LookPath(name); err == nil {
		return result
	}
	for _, directory := range toolDirectories() {
		path := filepath.Join(directory, name)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path
		}
	}
	return ""
}

func dependencyEnvironment() []string {
	directories := toolDirectories()
	directories = append(directories, os.Getenv("PATH"))
	path := strings.Join(directories, string(os.PathListSeparator))
	env := os.Environ()
	for i, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			env[i] = "PATH=" + path
			return env
		}
	}
	return append(env, "PATH="+path)
}

func isMuseSourceRoot(root string) bool {
	if root == "" {
		return false
	}
	for _, relative := range []string{
		"package.json",
		"package-lock.json",
		filepath.Join("scripts", "story-cli.ts"),
		filepath.Join("packages", "storypack", "package.json"),
	} {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func sourceRootFrom(path string) string {
	if path == "" {
		return ""
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		path = filepath.Dir(path)
	}
	for {
		if isMuseSourceRoot(path) {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func detectMuseSourceRoot() string {
	if runner := os.Getenv("CREATIVE_STORY_RUNNER"); runner != "" {
		if root := sourceRootFrom(runner); root != "" {
			return root
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if root := sourceRootFrom(cwd); root != "" {
			return root
		}
	}
	if executable, err := os.Executable(); err == nil {
		return sourceRootFrom(executable)
	}
	return ""
}

func areStoryPackagesInstalled(root string) bool {
	if !isMuseSourceRoot(root) {
		return false
	}
	for _, relative := range []string{
		"node_modules/zod/package.json",
		"node_modules/@aivn/story-schema/package.json",
		"node_modules/@aivn/validator/package.json",
		"node_modules/@aivn/storypack/package.json",
	} {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func hasRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (d *dependencyStatus) checkAudioResources() {
	binName := "fluidsynth"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	bin := filepath.Join("bin", runtime.GOOS+"-"+runtime.GOARCH, binName)
	font := filepath.Join("soundfonts", "VintageDreamsWaves-v2.sf2")
	locations := []string{}
	if executable, err := os.Executable(); err == nil {
		locations = append(locations, filepath.Join(filepath.Dir(filepath.Dir(executable)), "Resources"))
	}
	if d.root != "" {
		locations = append(locations, filepath.Join(d.root, "resources"))
	}
	for _, location := range locations {
		if hasRegularFile(filepath.Join(location, bin)) {
			d.fluidSynth = true
		}
		if hasRegularFile(filepath.Join(location, font)) {
			d.soundFont = true
		}
	}
}

func inspectDependencies(overrideRoot string) dependencyStatus {
	root := overrideRoot
	if !isMuseSourceRoot(root) {
		root = detectMuseSourceRoot()
	}
	result := dependencyStatus{
		root: root,
		bun:  findExecutable("bun"),
		node: findExecutable("node"),
		npm:  findExecutable("npm"),
		brew: findExecutable("brew"),
	}
	result.storyPackages = areStoryPackagesInstalled(root)
	result.checkAudioResources()
	return result
}

type dependencyInstallPlan struct {
	label      string
	executable string
	arguments  []string
	directory  string
	command    string
	note       string
}

func planDependencyInstall(kind string, status dependencyStatus) (dependencyInstallPlan, error) {
	if runtime.GOOS != "darwin" {
		return dependencyInstallPlan{}, errors.New("一键安装目前仅支持 macOS，请使用系统包管理器手动安装。")
	}
	switch kind {
	case "bun":
		if status.bun != "" {
			return dependencyInstallPlan{}, errors.New("Bun 已检测到，不需要安装。")
		}
		if status.brew == "" {
			return dependencyInstallPlan{}, errors.New("未找到 Homebrew。可以点击「查看 Bun 安装说明」。")
		}
		return dependencyInstallPlan{
			label:      "Bun",
			executable: status.brew,
			arguments:  []string{"install", "oven-sh/bun/bun"},
			command:    "brew install oven-sh/bun/bun",
			note:       "Homebrew 将通过网络下载 Bun，并修改 Homebrew 管理的目录。不会触碰 Muse 作品库。",
		}, nil
	case "node":
		if status.node != "" && status.npm != "" {
			return dependencyInstallPlan{}, errors.New("Node.js 与 npm 已检测到。")
		}
		if status.brew == "" {
			return dependencyInstallPlan{}, errors.New("未找到 Homebrew。请通过 Node.js 官网安装。")
		}
		return dependencyInstallPlan{
			label:      "Node.js 和 npm",
			executable: status.brew,
			arguments:  []string{"install", "node"},
			command:    "brew install node",
			note:       "Homebrew 将通过网络安装 Node.js 和 npm，可能同时安装它们的系统依赖。",
		}, nil
	case "packages":
		if !isMuseSourceRoot(status.root) {
			return dependencyInstallPlan{}, errors.New("未找到有效的 Muse 源码目录，请先选择项目目录。")
		}
		if status.node == "" || status.npm == "" {
			return dependencyInstallPlan{}, errors.New("需要先安装 Node.js 和 npm。")
		}
		if status.storyPackages {
			return dependencyInstallPlan{}, errors.New("StoryPack 依赖已安装。")
		}
		return dependencyInstallPlan{
			label:      "Muse StoryPack 依赖",
			executable: status.npm,
			arguments:  []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"},
			directory:  status.root,
			command:    "npm ci --ignore-scripts --no-audit --no-fund",
			note:       "只修改所选 Muse 源码目录的 node_modules，按已有锁文件安装。npm ci 会替换该目录已有的 node_modules；不会运行 npm 生命周期脚本。",
		}, nil
	default:
		return dependencyInstallPlan{}, errors.New("不支持的依赖安装操作")
	}
}

func (a *nativeUI) refreshDependencies() {
	a.dependencies = inspectDependencies(a.dependencies.root)
	a.dependencyMessage = "检查完成；未执行任何安装。"
}

func (a *nativeUI) chooseMuseDirectory() {
	if a.window == nil || a.dependencyBusy {
		return
	}
	a.dependencyBusy = true
	win := a.window
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: win, Title: "选择 Muse 源码目录", Directory: true,
			ButtonLabel: "选择目录",
		})
		win.Update(func() {
			a.dependencyBusy = false
			if err != nil {
				a.dependencyMessage = "选择目录失败：" + err.Error()
				return
			}
			if len(paths) == 0 {
				return
			}
			if !isMuseSourceRoot(paths[0]) {
				a.dependencyMessage = "该目录没有 Muse 的 package-lock.json 和 StoryPack 源码，请选择正确的项目根目录。"
				return
			}
			a.dependencies = inspectDependencies(paths[0])
			a.dependencyMessage = "已选择源码目录：" + paths[0]
		})
	}()
}

func (a *nativeUI) installDependency(kind string) {
	if a.window == nil || a.dependencyBusy {
		return
	}
	plan, err := planDependencyInstall(kind, a.dependencies)
	if err != nil {
		a.dependencyMessage = err.Error()
		return
	}
	selectedRoot := a.dependencies.root
	a.dependencyBusy = true
	win := a.window
	go func() {
		confirmation, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:        win,
			Type:          mygo.MessageQuestion,
			Title:         "安装 " + plan.label,
			Message:       "确认在此 Mac 上执行安装吗？",
			Detail:        plan.command + "\n执行文件：" + plan.executable + "\n\n" + plan.note,
			Buttons:       []string{"取消", "安装"},
			DefaultButton: 0,
			CancelButton:  0,
		})
		if err != nil || confirmation.Button != 1 {
			win.Update(func() {
				a.dependencyBusy = false
				if err != nil {
					a.dependencyMessage = "安装确认失败：" + err.Error()
				}
			})
			return
		}
		win.Update(func() {
			a.dependencyMessage = "正在安装 " + plan.label + "…"
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)
		cmd.Dir = plan.directory
		cmd.Env = dependencyEnvironment()
		// Never send CLI output, environment variables or credentials
		// directly into the MyGo GUI.
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		installError := cmd.Run()
		updated := inspectDependencies(selectedRoot)
		win.Update(func() {
			a.dependencies = updated
			a.dependencyBusy = false
			switch {
			case ctx.Err() == context.DeadlineExceeded:
				a.dependencyMessage = "安装超时。请在终端检查是否完成：" + plan.command
			case installError != nil:
				a.dependencyMessage = "安装未完成（" + installError.Error() + "）。可在终端运行：" + plan.command
			default:
				a.dependencyMessage = "命令执行完成，已重新检查依赖状态。"
			}
		})
	}()
}

func (a *nativeUI) openDependencyGuide(kind string) {
	url := ""
	switch kind {
	case "bun":
		url = "https://bun.com/docs/installation"
	case "node":
		url = "https://nodejs.org/en/download"
	case "brew":
		url = "https://brew.sh"
	}
	if url == "" || runtime.GOOS != "darwin" {
		return
	}
	if err := exec.Command("/usr/bin/open", url).Start(); err != nil {
		a.dependencyMessage = "无法打开浏览器，请手动访问：" + url
	}
}

func (a *nativeUI) dependenciesView(c *ui.Context) {
	ui.Column(c).MaxWidth(730).Gap(15).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, "本机运行环境").FontSize(17).Bold().Grow(1)
			if ui.Button(c, "重新检查").Disabled(a.dependencyBusy).Clicked() {
				a.refreshDependencies()
			}
		})
		ui.Text(c, "仅 StoryPack 构建需要 Bun 和本地项目包。Go CLI 与 MyGo 不依赖它们。").Wrap()
		if a.dependencyBusy {
			ui.Text(c, "正在等待安装确认或执行安装…").Wrap()
		}
		if a.dependencyMessage != "" {
			ui.Text(c, a.dependencyMessage).Wrap()
		}
		ui.Divider(c)

		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "Bun · 剧情运行时").Bold()
				if a.dependencies.bun != "" {
					ui.Text(c, "已安装").FontSize(12)
				} else {
					ui.Text(c, "未检测到 Bun，无法运行 StoryPack 构建。").Wrap()
				}
			})
			if a.dependencies.bun == "" {
				if a.dependencies.brew != "" {
					if ui.Button(c, "安装 Bun").Disabled(a.dependencyBusy).Clicked() {
						a.installDependency("bun")
					}
				} else if ui.Button(c, "安装说明").Clicked() {
					a.openDependencyGuide("bun")
				}
			}
		})
		ui.Divider(c)

		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "Node.js 与 npm").Bold()
				if a.dependencies.node != "" && a.dependencies.npm != "" {
					ui.Text(c, "已安装 · 仅用于管理 StoryPack 包").FontSize(12)
				} else {
					ui.Text(c, "缺少 Node.js 或 npm，不能安装 StoryPack 包。").Wrap()
				}
			})
			if a.dependencies.node == "" || a.dependencies.npm == "" {
				if a.dependencies.brew != "" {
					if ui.Button(c, "安装 Node.js").Disabled(a.dependencyBusy).Clicked() {
						a.installDependency("node")
					}
				} else if ui.Button(c, "安装说明").Clicked() {
					a.openDependencyGuide("node")
				}
			}
		})
		ui.Divider(c)

		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "Muse StoryPack 包").Bold()
				switch {
				case a.dependencies.root == "":
					ui.Text(c, "未定位到源码目录。请先选择 Muse 项目。").Wrap()
				case a.dependencies.storyPackages:
					ui.Text(c, "已安装").FontSize(12)
				default:
					ui.Text(c, "缺少项目依赖包，需从锁文件安装。").Wrap()
				}
			})
			if a.dependencies.root != "" && !a.dependencies.storyPackages &&
				a.dependencies.node != "" && a.dependencies.npm != "" {
				if ui.Button(c, "安装项目依赖").Disabled(a.dependencyBusy).Clicked() {
					a.installDependency("packages")
				}
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "选择源码目录…").Disabled(a.dependencyBusy).Clicked() {
				a.chooseMuseDirectory()
			}
			if a.dependencies.root != "" {
				ui.Text(c, nativeShort(a.dependencies.root, 84)).FontSize(11).Wrap()
			}
		})
		ui.Divider(c)

		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "FluidSynth 与 SoundFont").Bold()
				if a.dependencies.fluidSynth && a.dependencies.soundFont {
					ui.Text(c, "已包含在当前应用中，无需安装").FontSize(12)
				} else {
					ui.Text(c, "打包资源不完整，请从 Muse 项目重新构建应用。").Wrap()
				}
			})
		})
		if a.dependencies.brew == "" &&
			(a.dependencies.bun == "" || a.dependencies.node == "" || a.dependencies.npm == "") {
			if ui.Button(c, "Homebrew 官网").Clicked() {
				a.openDependencyGuide("brew")
			}
		}
		ui.Divider(c)
		ui.Text(c, "安装仅在你点击按钮并通过 macOS 确认后执行；不会自动提权或发起付费 API 请求。").
			FontSize(12).Wrap()
	})
}
