# 关系图 D127 横树改造记录（归档）
<!-- docs-sync: 2026-11-22 @ c8e65d3 -->

> **归档说明**：本篇是 [relation-map-design.md](../relation-map-design.md) §10 的 2026-10-11 完成记录
> （改动面与推进序），2026-11-22 从规格文档移出——规格只写「现在为真」。形态与现状以
> relation-map-design.md 为准；后续演进（D128 源档物理、D129 结构锥形）见该文 §5.4 与
> [decisions.md](../decisions.md)。本文不随后续代码更新。

### 10.1 改动面（D127 横树改造）

| 块 | 文件 | 内容 |
|---|---|---|
| 物理内核 | `relmap_layout.go` | `initRest` 横化（`restX = 累加 colGap`、`restY = 兄弟叶数加权窗口，窗心对齐父`）；弹簧常数互换（X 2.0 / Y 1.0）；`adoptSlot` 纵向钳改**横向钳**（`[relColAdoptMin, relColAdoptMax] × colGap`）；改前每对父子存 `relRowGap`，改后列距统一为 `colGap` |
| 帧层 | `relmap_frame.go` | `fitView` + fit 后**右基准平移**（`offX = relAnchorDatumX×viewW − headX×z`；平移会出界则保持盒心对准）；触底回退仍走 `fitFollow`（目标位同步为右基准）；`focusAnchor` 改为右基准落点；新增 `relAnchorDatumX ≈ 0.72` |
| 卡片 pitch | `relmap.go` | 兄弟 pitch 喂 x 轴（`relSlotPitch` 沿横轴定义）；卡片几何不变 |
| 测试 | `relmap_layout_test.go` / `relmap_frame_test.go` | 纵梯断言换横轴规则：层序单调改 **x 单调**；adopt 带断言改横向；A8/A13 守门改右基准；A11/A12 等沿用 |
| 基准 | `relmap_anim_bench_test.go` | 横树夹具复测（收敛帧/残差预期变化） |
| 绘制 | `draw*` | **预期零改**（几何无关：边/箭头按坐标向量、卡片按节点中心） |

> 文件组织备注（2026-10-22）：帧层已按关注点拆成同包的 `relmap_frame.go`（帧态与帧循环）、
> `relmap_view.go`（视场取景）、`relmap_geom.go`（几何与命中）、`relmap_draw.go`（绘制）、
> `relmap_node.go`（节点动作与右键菜单）；上表按当时的单文件规则记录，符号归属不变。

---

### 10.3 推进序与门禁

| # | 内容 | 门禁 |
|---|---|---|
| 0 | 文档先行：D127 决策 + 本文（v2.0 全量重写）+ gui-design.md §15.7 同步 + roadmap S3 状态 | 文档 diff 自查 |
| 1 | 物理内核横化：`initRest` / 弹簧互换 / `adoptSlot` 横带 / `colGap` | 布局性质测试 + 不变量（x 层序单调、卡不深压、可复现） |
| 2 | 帧层视场：右基准平移 + `focusAnchor` + `fitFollow` 目标位 | A8/A13 帧测试（逐点视口内 + Head 基准位） |
| 3 | 测试与基准横化重写 | `go test ./...` + 基准复测（≥1s 采样） |
| 4 | 全门禁 + 手工验收（时间轴读感、版本分杈、长会话回看） | 见下 |

**每步门禁**：`gofmt -l .` 空 · `go vet ./...` · `go test ./...` · `go test -race ./...` ·
`go build ./cmd/aquarius` · GUI 冒烟 `AQUARIUS_GUI_SMOKE=1` + 手工验收清单。
**一个 commit 一件事**，英文祈使句（AGENTS.md）。

> 这张表是 D127 完成时的推进规则，四步均已完成；现在新增形态或物理变更按同样门禁走。
