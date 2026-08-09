# VatBrain Logo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 生成三张采用同一品牌骨架、分别呈现极简科技、神经生物和赛博科幻气质的 VatBrain README Logo 候选稿。

**Architecture:** 使用内置 `image_gen` 分别生成三个独立 PNG。每个提示共享固定字标、色彩、画布和禁止项，只改变图形语言。生成后逐张视觉检查，并将合格文件保存到 `assets/logo/`；本轮不修改 README，也不制作透明版或 SVG。

**Tech Stack:** Codex 内置图像生成、PNG、`view_image` 视觉检查、Git。

---

## 文件结构

- 创建 `assets/logo/vatbrain-logo-minimal.png`：极简科技方向候选稿。
- 创建 `assets/logo/vatbrain-logo-neural.png`：神经生物方向候选稿。
- 创建 `assets/logo/vatbrain-logo-cyberpunk.png`：赛博科幻方向候选稿。
- 修改 `.vatbrain/agent_context.md`：记录生成结果、校验结论和后续选择事项。

### Task 1: 生成极简科技方向

**Files:**
- Create: `assets/logo/vatbrain-logo-minimal.png`

- [ ] **Step 1: 创建资产目录**

Run: `mkdir -p assets/logo`

Expected: `assets/logo/` 存在，目录内不覆盖任何已有同名资产。

- [ ] **Step 2: 使用内置 image_gen 生成候选稿**

```text
Use case: logo-brand
Asset type: horizontal open-source project logo for a GitHub README
Primary request: Create a polished minimal technology logo for VatBrain, an AI Agent memory augmentation system inspired by a brain in a vat.
Subject: On the left, a distinctive abstract mark combining a rounded scientific vessel outline with a simplified brain made from a few continuous graph-and-vector paths. Include only a small number of circular memory nodes. On the right, the exact wordmark "VatBrain" exactly once.
Style/medium: clean vector-friendly geometric logo, flat shapes, restrained premium gradient, precise edges, professional open-source infrastructure brand
Composition/framing: wide horizontal lockup, mark and wordmark vertically centered, balanced whitespace, generous safe margins, 3:1 landscape composition
Color palette: deep ocean navy, neural violet, nutrient-fluid cyan-green, on a clean warm-white background
Text (verbatim): "VatBrain"
Constraints: the brain and vessel meanings must both be recognizable; graph nodes and paths hint at graph + vector memory; wordmark uses a modern geometric sans-serif; spelling must be exact; one logo only
Avoid: realistic anatomy, gore, lightbulb icon, mascot, poster scene, excessive detail, tiny text, slogan, watermark, mockup, shadowy background, duplicate words
```

Expected: 一张横向 PNG；左侧图形在缩小后仍清楚，右侧只出现一次准确的 `VatBrain`。

- [ ] **Step 3: 保存到项目路径**

将内置工具返回的 `$CODEX_HOME/generated_images/` 文件复制为 `assets/logo/vatbrain-logo-minimal.png`。保留默认生成文件，避免破坏性移动。

- [ ] **Step 4: 视觉检查**

使用 `view_image` 打开文件，确认文字准确且仅一次、图形同时可读为容器与脑、没有禁止项、四周有安全边距。若有一项失败，只修改对应问题重新生成一次。

### Task 2: 生成神经生物方向

**Files:**
- Create: `assets/logo/vatbrain-logo-neural.png`

- [ ] **Step 1: 使用内置 image_gen 生成候选稿**

```text
Use case: logo-brand
Asset type: horizontal open-source project logo for a GitHub README
Primary request: Create a polished neural-biological logo for VatBrain, an AI Agent memory augmentation system inspired by a brain in a vat.
Subject: On the left, a translucent simplified vessel containing an elegant suspended neural network. A restrained set of synapse nodes and organic connections subtly forms a letter V while retaining a recognizable brain silhouette. On the right, the exact wordmark "VatBrain" exactly once.
Style/medium: vector-friendly biomorphic logo, scientific but approachable, controlled organic curves, crisp professional finish, not a medical illustration
Composition/framing: wide horizontal lockup, mark and wordmark vertically centered, balanced whitespace, generous safe margins, 3:1 landscape composition
Color palette: the same deep ocean navy, neural violet, and nutrient-fluid cyan-green family, on a clean warm-white background
Text (verbatim): "VatBrain"
Constraints: vessel and neural-memory meanings must be immediately legible; moderate node count; wordmark uses a modern geometric sans-serif; spelling must be exact; one logo only
Avoid: realistic anatomy, gore, medical diagram labels, DNA helix, lightbulb icon, mascot, poster scene, excessive detail, tiny text, slogan, watermark, mockup, duplicate words
```

Expected: 生物有机气质明显不同于极简版，但使用相同字标布局和色彩家族。

- [ ] **Step 2: 保存到项目路径**

复制生成结果为 `assets/logo/vatbrain-logo-neural.png`，不得覆盖其他方向文件。

- [ ] **Step 3: 视觉检查**

使用 `view_image` 确认文字、容器、脑形、节点数量、安全边距和禁止项。若失败，只针对失败项重生成一次。

### Task 3: 生成赛博科幻方向

**Files:**
- Create: `assets/logo/vatbrain-logo-cyberpunk.png`

- [ ] **Step 1: 使用内置 image_gen 生成候选稿**

```text
Use case: logo-brand
Asset type: horizontal open-source project logo for a GitHub README
Primary request: Create a striking but usable cyber-science-fiction logo for VatBrain, an AI Agent memory augmentation system inspired by a brain in a vat.
Subject: On the left, a luminous abstract brain core suspended inside a compact cylindrical life-support vessel. A few graph-memory connections radiate from the core without becoming a scene. On the right, the exact wordmark "VatBrain" exactly once.
Style/medium: premium vector-friendly cyberpunk brand mark, controlled neon edge glow, crisp silhouette, sophisticated developer-tool identity, logo rather than illustration
Composition/framing: wide horizontal lockup, mark and wordmark vertically centered, balanced whitespace, generous safe margins, 3:1 landscape composition
Color palette: the same deep ocean navy, neural violet, and nutrient-fluid cyan-green family with restrained neon highlights, on a clean warm-white background
Text (verbatim): "VatBrain"
Constraints: the brain-in-a-vat story must read immediately; glow must not reduce edge clarity; wordmark uses a modern geometric sans-serif; spelling must be exact; one logo only
Avoid: dark poster background, full laboratory scene, character, realistic anatomy, gore, lightbulb icon, excessive glow, excessive detail, tiny text, slogan, watermark, mockup, duplicate words
```

Expected: 科幻冲击力明显强于前两版，但仍是可直接放入 README 的独立横向 Logo。

- [ ] **Step 2: 保存到项目路径**

复制生成结果为 `assets/logo/vatbrain-logo-cyberpunk.png`，不得覆盖其他方向文件。

- [ ] **Step 3: 视觉检查**

使用 `view_image` 确认文字、生命维持舱、脑核、克制发光、安全边距和禁止项。若失败，只针对失败项重生成一次。

### Task 4: 横向验收与提交

**Files:**
- Verify: `assets/logo/vatbrain-logo-minimal.png`
- Verify: `assets/logo/vatbrain-logo-neural.png`
- Verify: `assets/logo/vatbrain-logo-cyberpunk.png`
- Modify: `.vatbrain/agent_context.md`
- Modify: `.vatbrain/agent_context_archive.md`

- [ ] **Step 1: 检查三个文件的格式与尺寸**

Run: `file assets/logo/vatbrain-logo-*.png`

Expected: 三个路径都被识别为 PNG image data，且均为非零尺寸。

- [ ] **Step 2: 并列检查品牌一致性**

分别用 `view_image` 打开三张图，确认相近的横向比例、字标大小、配色家族与留白，同时确认三种图形语言容易区分。不得通过修改 README 来掩盖画布或构图问题。

- [ ] **Step 3: 更新 Agent Context**

记录三张文件路径、每张是否一次通过或重生成、最终校验结果，以及下一步由用户选择主方向。将超过最近三次交互的旧内容移动到 `.vatbrain/agent_context_archive.md`。

- [ ] **Step 4: 检查改动边界**

Run: `git status --short && git diff --check`

Expected: 只出现三个 Logo PNG 与 Agent Context 维护；`git diff --check` 无输出。

- [ ] **Step 5: 提交候选稿**

```bash
git add assets/logo/vatbrain-logo-minimal.png assets/logo/vatbrain-logo-neural.png assets/logo/vatbrain-logo-cyberpunk.png .vatbrain/agent_context.md .vatbrain/agent_context_archive.md
git commit -m "feat: add VatBrain logo concepts" -m "Co-Authored-By: Codex Opus 4.7 <noreply@anthropic.com>"
```

Expected: 新提交只包含三张候选稿与上下文维护，工作树干净；不推送、不修改 README。
