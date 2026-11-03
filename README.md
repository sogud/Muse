# Muse

面向 AI Agent 的本地多模态创作工具。**一个仓库、一个 Go Core、两种独立入口：CLI 和 MyGo 原生 UI。** Muse 调用成熟的模型 API，不训练模型、不实现完整 DAW，也不内置第二套 Agent 聊天窗口。

## 当前结构

```text
Agent / 项目 Skill ──> creative CLI ─────┐
                                       ├─> internal/core/
MyGo 原生窗口 (Go ui) ───────────────────┘     ├─ JobRuntime / Music Provider Adapters
                                             ├─ SQLite / Artifact Store
                                             ├─ MIDI / FluidSynth
                                             └─ Project Manifest
story 子命令 ──> Bun ──> packages/{story-schema,story-engine,validator,storypack}
```

- **Go CLI**：`cmd/creative/`，输出机器可读 JSON，不依赖 GUI。
- **共享 Core**：`internal/core/`，管理创作项目、持久 Job、授权、素材和音乐 API。
- **MyGo UI**：`main.go` + `native_ui.go`；使用 MyGo 默认原生控件外观，没有 Vue、Electron、Vite、WebView 或额外前端服务。
- **故事引擎**：`packages/` + `scripts/story-cli.ts`，提供可移植的 StoryPack 能力；仅剧情命令需要 Bun 和安装的 TS 依赖。
- **Agent Skill**：`.agents/skills/muse/SKILL.md`；目前没有 MCP Server。

## MyGo 原生管理界面

Muse 不提供 AI 对话、聊天消息、创作提示词或任务草稿输入界面。创作意图由 Codex 等外部 Agent 接收，Agent 使用 `.agents/skills/muse/SKILL.md` 调用独立的 `creative` CLI 创建项目、执行任务、归档素材。

MyGo 是可选的**管理与配置工具**，使用 MyGo 自带原生组件与系统主题：

- **项目概览**：显示 CLI/Agent 已创建的项目、类型、任务与作品数量、最近任务；可按需导出 Manifest。
- **任务记录**：左侧任务列表与右侧可选择的详情，显示实际任务状态、错误、来源、Provider Task ID、时间及相关素材；明确标注费用估算未经真实账单核对。只有用户主动点击才查询已有 Provider 任务，不会自动产生费用。
- **我的作品**：左侧作品列表，右侧展示 MIME、SHA-256、来源、授权声明和人工审核；点击「打开预览」使用 macOS 默认应用查看安全类型的图片、音频、视频及文本文件，点击「导出」使用系统保存弹窗，复用 Core 的哈希校验并拒绝覆盖。
- **设置**：显示三家音乐 Provider（Mureka、ElevenLabs、Google Lyria）和文本 Pattern 的本地配置状态；音乐密钥分别使用 `MUREKA_API_KEY`、`ELEVENLABS_API_KEY`、`GEMINI_API_KEY` 环境变量。文本模型 Key 不用于音乐 API。

MyGo 打开期间每 **3 秒仅从本地 SQLite 更新**项目、任务、作品和当前选中任务详情，不会调用 Provider API；关闭窗口后停止定时同步。用户仍可点击「刷新」。若远端生成仍在运行，必须由 Agent/CLI 或用户点击「手动查询 Provider 状态」来更新远端结果。

GUI 中不再提供「新建项目」文本框、「创作需求」输入区或「保存需求」按钮。项目创建、创作与执行通过 Agent / CLI 完成；模型设置中的输入框属于配置行为，不是聊天窗口。`creative.request` 旧草稿和原有数据库记录仍可被查看，不会因界面变更被删除。

## 运行

在 Apple Silicon macOS 上，从本仓库根目录执行：

```bash
# 无界面的 CLI
go build -o build/creative ./cmd/creative
./build/creative doctor
./build/creative --help

# 纯 Go 原生窗口，无需 npm 或前端构建
go run github.com/egoist/mygo/cmd/mygo@v0.7.3 build -skip-dmg
open build/darwin-arm64/Muse.app

# 原生开发
go run github.com/egoist/mygo/cmd/mygo@v0.7.3 dev
```

当前 App 输出：`build/darwin-arm64/Muse.app`。macOS Bundle ID 暂时保留 `com.oto.app`，本地数据路径仍为 `~/.oto/`，避免破坏历史作品。

### CLI 示例

```bash
./build/creative workspace create --title "03:17 配乐" --kind music
./build/creative workspace list
./build/creative music providers
./build/creative providers  # Mureka + 文本 Pattern 的本地配置状态，不联网
./build/creative music generate --provider mureka --prompt "quiet suspense piano" --dry-run
./build/creative music generate --provider elevenlabs --prompt "quiet suspense piano" --instrumental --dry-run
./build/creative music generate --provider lyria --model lyria-3-clip-preview --prompt "quiet suspense piano" --instrumental --dry-run
./build/creative job list --project PROJECT_ID
./build/creative job status --id JOB_ID
./build/creative job sync --id JOB_ID
./build/creative artifact list --project PROJECT_ID
./build/creative workspace export --id PROJECT_ID
```

`creative providers` 与 MyGo 设置页复用同一份 Go Core 状态检查（三家音乐 Provider 与文本 Pattern）。它只返回 `configured`、`credentialSource` 和 `detail` 等非敏感字段，不会调用网络或产生费用。**已配置不代表网络连通、额度充足或生成成功。** 当前 Mureka 和文本 Pattern 的密钥来源仍不同；安全统一存储需要后续 Keychain 迁移。

### 多音乐 Provider

| Provider | CLI ID | 默认模型 | 结果形式 | 环境变量 |
| --- | --- | --- | --- | --- |
| Mureka | `mureka` | `auto` | 异步 Task ID，`job sync` 查询并归档 | `MUREKA_API_KEY` |
| ElevenLabs Music | `elevenlabs` | `music_v2_5` | 直接 MP3，先本地暂存再归档 | `ELEVENLABS_API_KEY` |
| Google Lyria | `lyria` | `lyria-3-clip-preview` | Gemini Interactions API 的 base64 MP3 | `GEMINI_API_KEY` |

可通过 `--model lyria-3.5` 选择完整歌曲模型；Clip 目标是 30 秒。ElevenLabs 可以使用 `--duration-seconds` 指定时长。所有 Provider 仅在明确批准并执行 `--allow-paid` 或 `--approve-paid` 时发起一次付费请求。`--dry-run` 只返回计划，不联网。

Mureka 有异步任务 ID，支持查询原始任务状态。ElevenLabs 与 Lyria 直接返回音频，因此 `job sync` 对它们只能恢复**已经完成的本地文件归档**，不能查询远端，也绝不会重复付费生成；`job attach` 不适用于这两家。遇到不确定的付费结果，需人工核对服务商账单和暂存文件。**手工填写的预算字段并非真实收费上限**，商业授权按各服务条款核对。

官方文档：[ElevenLabs Music](https://elevenlabs.io/docs/api-reference/music/compose)、[Google Lyria](https://ai.google.dev/gemini-api/docs/music-generation)。目前未用真实付费账号验证这两家接口与归档结果。

`workspace` 管通用创作项目；旧 `project` 子命令管理 MIDI 工程，不可混淆。`job status` 只读本地状态，`job sync` 只查询已提交的供应商任务并尝试归档结果；两者不会发起新的付费生成。`job approve` 只记录审核决定，不等于执行。

付费请求必须经过单独授权后，显式运行 `job submit --id JOB_ID --allow-paid`。调用前需具备预算估算和批准状态。Mureka 的真实收费尚未接入核算，用户输入的预算不是供应商保证的实际限额；发生提交结果不确定时，**禁止自动再次 POST**，应核实已有任务并使用 `job attach` 恢复。

### GUI 依赖检查与点击安装

MyGo 原生应用左侧进入「**设置**」，再选择「**环境依赖**」标签页。只有 StoryPack 剧情命令需要 Bun 与 JavaScript 包；独立 Go CLI 和 MyGo UI 不需要 Node.js 或 Bun。

- 缺少 Bun：点击「安装 Bun」，确认弹窗展示安装命令后才执行 `brew install oven-sh/bun/bun`。
- 缺少 Node.js/npm：点击「安装 Node.js（含 npm）」，确认后运行 `brew install node`。
- 缺少 StoryPack 包：点击「安装 StoryPack 依赖」，确认后仅在识别出的 Muse 源码根目录运行 `npm ci --ignore-scripts --no-audit --no-fund`。注意 `npm ci` 会替换该目录中的现有 `node_modules`，但不会执行 npm 生命周期脚本。
- App 移至源码仓之外时，可点「选择 Muse 源码目录…」，指定包含 `package-lock.json` 和 `scripts/story-cli.ts` 的项目目录。没有 Homebrew 时提供 Bun、Node.js、Homebrew 官方安装文档。
- FluidSynth 和 SoundFont 在当前应用中已内置，仅检测完整性，不自行下载来源不明的可执行文件。

**只有用户点击安装并通过 macOS 原生弹窗再次确认，才会执行 Homebrew 或 npm 命令**。安装运行在后台，结束后重新检查依赖；启动 Muse、切换页面、普通检测均不执行安装，也不调用付费模型。CLI 缺少 Bun/StoryPack 包时亦会提示到此页面处理。检测主要检查文件及可执行程序路径，联网、权限、版本兼容等问题仍需通过实际功能使用确认。

### StoryPack（需要 Bun / TypeScript）

```bash
npm install --ignore-scripts
npm run typecheck:story
npm run test:story
./build/creative story validate --input test/fixtures/agent-storypack-input.json
./build/creative story build --input test/fixtures/agent-storypack-input.json --output tmp/example-storypack.json

# 推荐：直接归档到 Muse 项目（Bun 在本机运行，不调用付费 API）
./build/creative workspace create --title "剧情素材" --kind story
./build/creative story build --input test/fixtures/agent-storypack-input.json --project PROJECT_ID
# 从 JSON 结果读取 data.storyPackArtifactId
./build/creative artifact export --id ARTIFACT_ID --output ./storypack.json
```

StoryPack 子命令通过 Bun 调用迁入的 TypeScript 包，不会启动 Node Web 服务。新用法 `--project` 在本机创建可重试的 `story.build` Job，将**输入快照与输出 StoryPack** 归档到同一 Artifact Store，并自动导出项目 Manifest。JSON 结果中的 `storyPackArtifactId` 为持久文件 ID；`artifact export` 可导出到指定新路径、校验 SHA-256、拒绝覆盖已有文件。重复提交**相同输入内容**会复用原 Job。原 `--output` 用法仍可单独生成 JSON 文件，但不进入 Muse 项目。

此流程**零付费 API 调用**；真实作品内容会保存在当前 `OTO_USER_DATA_DIR` 内，务必确认项目目录和隐私边界。

## 数据与安全

- 默认数据库：`~/.oto/oto.sqlite`；创作素材在 `~/.oto/creative/projects/<id>/artifacts/`。`OTO_USER_DATA_DIR` 可隔离测试。
- 素材导入会复制文件，写入哈希、Provider 来源及审核状态；`manifest.json` 是 SQLite 的导出快照，不是第二份可写数据库。
- `MUREKA_API_KEY` 由进程环境变量提供；文本模型配置仍使用旧本地设置表，**Keychain 迁移尚未完成**。不要将数据库、密钥或真实项目输入提交到 Git。
- 只允许明确授权的付费操作。真实 Mureka 任务和音频归档尚未进行付费端到端验收。
- MyGo 使用自带组件和明暗主题样式，不单独维护 CSS、AppKit 包装层或自定义卡片皮肤。

## 当前欠缺

真实计费核对和可靠预算执行、跨进程后台 Worker、图片/TTS/视频 Provider、MCP、Keychain、真实 MyGo 手工交互验收、最终品牌和发布签名。

## License

Muse 自有代码采用 [MIT License](LICENSE)。第三方依赖和素材保留各自许可证，不因根许可证改变授权。内置 SoundFont 的使用与再分发须遵循其 [版权说明](resources/soundfonts/VintageDreamsWaves-v2.COPYRIGHT.txt)；音乐服务生成内容另按服务商条款处理。
