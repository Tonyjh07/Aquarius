package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// RuntimeEnv 运行环境事实（D37）：随 config 快照进 persona 首节点的英文环境块，
// 让模型开局就知道平台语义、终端形态、特权沙盒位置，而不必踩坑后才学。
type RuntimeEnv struct {
	Platform   string // 如 "windows/amd64"
	Terminal   string // UI 形态：tui / repl（cfg.UI.Kind）
	TERM       string // 终端 TERM 环境变量（可空）
	SandboxDir string // 特权沙盒目录（仅绝对路径才附提示，139d585 口径）
	// ToolTimeoutSec 缺省单次调用超时（D38）；0 = 不附超时说明行。
	ToolTimeoutSec int
}

// PromptModule 模块树节点（D144/S7）：叶 = 仅 Text（命名片段）；组 = Children（子模块名有序
// 列表，可多级嵌套），亦可同时带 Text（组头）。真源在 config `prompts.modules`。
type PromptModule struct {
	Text     string
	Children []string
}

// PromptSpec 人格装配规格（D144/S7）：模块树 + 有序顶层引用 + 兼容单串。
// 由装配根从 config `prompts` 段构造；app 侧只做纯组合、不读配置文件。
type PromptSpec struct {
	// Modules 模块树：name → 节点。用户模块名不得以 `@` 开头（保留给内置模块）。
	Modules map[string]PromptModule
	// Persona 人格 = 有序顶层引用列表；空 = 默认 DefaultPersonaRefs。
	Persona []string
	// Legacy 顶层 system_prompt（兼容）：作为内置 @default 的正文来源。
	Legacy string
}

// DefaultPersonaRefs prompts.persona 为空时的默认顶层引用（D144）。
var DefaultPersonaRefs = []string{"@default", "@env"}

// reservedModulePrefix 内置模块名保留前缀（用户模块不得占用，引擎侧一律以内置为准）。
const reservedModulePrefix = "@"

// ReservedModulePrefix 内置模块名保留前缀（导出供装配根/UI 校验用户模块名，D144）。
const ReservedModulePrefix = reservedModulePrefix

// builtinEnvRefs 内置 @env 组模块的子模块顺序（@env.date 恒出，其余按事实是否有值）。
var builtinEnvRefs = []string{"@env.platform", "@env.terminal", "@env.sandbox", "@env.timeout", "@env.date"}

// BuiltinModuleNames 内置模块引用名（含 `@` 前缀，展示序：@default → @env → 各子模块）。
// 设置窗用它列出「可引用的内置模块」（S7/D144）；返回副本，调用方可安全改写。
func BuiltinModuleNames() []string {
	out := make([]string, 0, 2+len(builtinEnvRefs))
	out = append(out, "@default", "@env")
	return append(out, builtinEnvRefs...)
}

// PersonaComposer 人格组合器（D144/S7）：绑定模块规格 + 内置模块动态取值（环境事实 + 时钟）。
// Compose 每次重新展开——@env.date 取 now()，故 persona 快照（会话创建时调用）得到「创建当天」。
type PersonaComposer struct {
	spec PromptSpec
	env  RuntimeEnv
	now  func() time.Time
}

// NewPersonaComposer 组装人格组合器；now 为 nil 时用 time.Now（@env.date 取值）。
func NewPersonaComposer(spec PromptSpec, env RuntimeEnv, now func() time.Time) *PersonaComposer {
	if now == nil {
		now = time.Now
	}
	return &PersonaComposer{spec: spec, env: env, now: now}
}

// Compose 展开人格为最终 system 文本，并返回缺失的模块名（供装配根记 warning、不静默）。
// 展开 = 深度优先：组自身 Text 在前、子模块换行接续（子模块间以换行连接）；顶层引用间以空行
// 连接；**空组不出**（有 children 但子模块全部无文本 ⇒ 整组含组文本一并不输出）；整段全空 ⇒
// 回落内置默认人格。环引用按递归栈截断。
func (c *PersonaComposer) Compose() (string, []string) {
	mods := c.modules()
	refs := c.spec.Persona
	if len(refs) == 0 {
		refs = DefaultPersonaRefs
	}
	var missing []string
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if t := expandModule(ref, mods, &missing, map[string]bool{}); t != "" {
			parts = append(parts, t)
		}
	}
	text := strings.Join(parts, "\n\n")
	if strings.TrimSpace(text) == "" {
		text = defaultSystem
	}
	return text, missing
}

// modules 合并内置模块与用户模块（用户模块名以 `@` 开头的被忽略——保留给内置）。
func (c *PersonaComposer) modules() map[string]PromptModule {
	mods := map[string]PromptModule{
		"@default": {Text: defaultPersonaText(c.spec.Legacy)},
		"@env":     {Text: "Runtime environment:", Children: builtinEnvRefs},
	}
	for name, text := range envLines(c.env) {
		mods[name] = PromptModule{Text: text}
	}
	// 缺席的 @env.* 显式置空（空字段不出行，但不是"缺失引用"——不算 missing）。
	for _, name := range builtinEnvRefs {
		if _, ok := mods[name]; !ok {
			mods[name] = PromptModule{}
		}
	}
	mods["@env.date"] = PromptModule{Text: "- Current date: " + c.now().Format("2006-01-02")}
	for name, m := range c.spec.Modules {
		if strings.HasPrefix(name, reservedModulePrefix) {
			continue
		}
		mods[name] = m
	}
	return mods
}

// defaultPersonaText @default 正文 = system_prompt 或内置默认（D20/D37 兼容）。
func defaultPersonaText(legacy string) string {
	if s := strings.TrimSpace(legacy); s != "" {
		return s
	}
	return defaultSystem
}

// envLines 环境信息内置模块取值（D37 口径）：字段为空不出行（空值不回传）。
func envLines(env RuntimeEnv) map[string]string {
	lines := map[string]string{}
	if env.Platform != "" {
		lines["@env.platform"] = "- Platform: " + env.Platform
	}
	if env.Terminal != "" {
		term := "TERM unset"
		if env.TERM != "" {
			term = "TERM=" + env.TERM
		}
		lines["@env.terminal"] = fmt.Sprintf("- Terminal: %s (%s)", env.Terminal, term)
	}
	// 沙盒提示必须是绝对路径——不能建议一个会被同一规则再拒的相对路径（139d585）。
	if filepath.IsAbs(env.SandboxDir) {
		lines["@env.sandbox"] = fmt.Sprintf("- Privileged sandbox directory: %q (writes there skip confirmation from the strict permission level upward).",
			env.SandboxDir)
	}
	// @env.timeout 保留参数的口径（D38/D171）：`job_start` 的 `timeout_sec` 是任务超时、
	// 自有语义，不参与保留参数（runner 按 schema 判定），所以这里不能写成"任何工具"。
	if env.ToolTimeoutSec > 0 {
		lines["@env.timeout"] = fmt.Sprintf("- Tool timeout: default %ds per call; pass the optional \"timeout_sec\" argument (1-%d) on any tool call to override it for that call (tools whose schema declares their own \"timeout_sec\" keep their own meaning).",
			env.ToolTimeoutSec, port.MaxCallTimeoutSec)
	}
	return lines
}

// expandModule 递归展开一个模块引用：组自身文本 + 子模块文本（换行接续）。
// 缺失名记入 missing 并返回空串；seen 为递归栈（截断环引用）。
func expandModule(name string, mods map[string]PromptModule, missing *[]string, seen map[string]bool) string {
	node, ok := mods[name]
	if !ok {
		*missing = append(*missing, name)
		return ""
	}
	if seen[name] {
		return "" // 环引用：截断
	}
	seen[name] = true
	defer delete(seen, name)

	var parts []string
	if s := strings.TrimSpace(node.Text); s != "" {
		parts = append(parts, s)
	}
	if len(node.Children) > 0 {
		childTexts := make([]string, 0, len(node.Children))
		for _, child := range node.Children {
			if t := expandModule(child, mods, missing, seen); t != "" {
				childTexts = append(childTexts, t)
			}
		}
		if len(childTexts) == 0 {
			return "" // 空组：整组（含组文本）不出
		}
		parts = append(parts, strings.Join(childTexts, "\n"))
	}
	return strings.Join(parts, "\n")
}

// waterline 计算装配水位（D21/D20）：persona = path[1] 的 system 节点；
// 摘要 = 其后最后一个 system 节点。返回 (personaIdx, watermarkIdx)，-1 表示无。
func waterline(path []conversation.Message) (personaIdx, watermarkIdx int) {
	personaIdx = -1
	if len(path) > 1 && path[1].Role == conversation.RoleSystem {
		personaIdx = 1
	}
	for i := len(path) - 1; i > personaIdx; i-- {
		if path[i].Role == conversation.RoleSystem {
			return personaIdx, i
		}
	}
	return personaIdx, -1
}

// assembleOpts 装配口径（§7.1）：附件库、思考回传（D42）、视觉能力（D169）。
//
// 三件事都只影响「怎么投影」，不影响树本身，收进一个结构体是为了让调用方
// 显式回答它们——散成多个布尔参数时，新加一个能力位很容易在某个调用点漏掉。
type assembleOpts struct {
	blobs        port.AttachmentStore
	echoThinking bool
	// vision = 当前模型接受图片输入。false 时图片一律降级为文字占位并写明原因，
	// 而不是把图片块发给一个不收图片的端点（那是一次没有出路的 400）。
	vision bool
}

// assemblePath 把 Path() 装配为模型消息序列（§7.1，含 D21 水位裁剪）：
// persona（path[1] 的 system 节点）恒回传；最新压缩摘要之上的历史（persona 之外）一律不回传；
// Root 空节点不进上下文。
func assemblePath(ctx context.Context, path []conversation.Message, opt assembleOpts) ([]port.PromptMessage, error) {
	personaIdx, watermarkIdx := waterline(path)
	start := 1 // 起点恒 ≥ 1：Root 不进上下文
	if watermarkIdx >= 0 {
		start = watermarkIdx
	}

	// 进入上下文的节点序列（水位之上）。工具分片的调用与结果同片（D95），
	// 不存在"结果在位/失联"之分，无需跨节点配对集合。

	// 遍历序列：有水位时先单独补发 persona（它不在 [start:] 内，恒回传，D20/D21）。
	seq := make([]int, 0, len(path))
	if watermarkIdx >= 0 && personaIdx == 1 {
		seq = append(seq, personaIdx)
	}
	for i := start; i < len(path); i++ {
		seq = append(seq, i)
	}

	var out []port.PromptMessage
	for _, i := range seq {
		m := path[i]
		switch m.Role {
		case conversation.RoleRoot:
			continue // Root 空节点不进上下文（D19）
		case conversation.RoleSystem:
			// 系统节点（persona / 压缩摘要）映射为 system 角色（D20/D21）。
			parts, err := assembleParts(ctx, m.Content, opt)
			if err != nil {
				return nil, err
			}
			// 摘要是模型写的、且吸收了其上的不可信内容，进去时标成"上下文"（D170）：
			// 否则等于把命令输出/文件内容经摘要洗成系统设定。persona 不套这句。
			if i == watermarkIdx {
				parts = append([]port.PromptPart{{Kind: port.PromptText, Text: summaryPrefix}}, parts...)
			}
			out = append(out, port.PromptMessage{Role: port.PromptSystem, Content: parts})
		case conversation.RoleInject:
			// 注入节点按**显式信任位**投影（D170）：宿主事实 → system（用户动作仍取 user）；
			// 其余（工具/插件/未知/未声明）→ user + 数据围栏（§9 口径，防伪装成用户指令）。
			pm, err := projectInjection(ctx, m, opt)
			if err != nil {
				return nil, err
			}
			if pm != nil {
				out = append(out, *pm)
			}
		case conversation.RoleAssistant:
			// D95 Turn 粒度：一个节点可含多段"思考/正文 + 工具分片"交错——按 tool 边界
			// 切段投影（调用与结果同片恒成对；Result=nil 的分片不声明也不应答）。
			msgs, err := assembleAssistantNode(ctx, m, opt)
			if err != nil {
				return nil, err
			}
			out = append(out, msgs...)
		case conversation.RoleUser:
			if len(m.Content) == 0 {
				continue
			}
			parts, err := assembleParts(ctx, m.Content, opt)
			if err != nil {
				return nil, err
			}
			out = append(out, port.PromptMessage{Role: port.PromptUser, Content: parts})
		default:
			// 未知角色：不入上下文（前向兼容；非法角色本就进不了树）。
			continue
		}
	}
	return out, nil
}

// 注入内容的数据围栏（D150/§9）：不可信上下文一律被这对标签包住，并前置一句英文
// 说明，防止工具/MCP 注入的内容伪装成用户指令。常量在 model_text.go（D36 集中处）。

// projectInjection 把一条 inject 节点投影为模型消息（D150/D170，投影表只在本处）：
//   - 可信（`conversation.TrustHost`）= 宿主事实 → system；来源是用户自己的动作时仍取
//     user 角色（换成 system 会让模型把用户原话当成系统设定）。两种情况都不套围栏；
//   - 外部（`conversation.TrustExternal`，零值）→ user + 数据围栏 + 前置说明句。
//
// **判定只看 `m.Trust`，不看 `Source` 前缀**：前缀是生产者自报的字符串（MCP 工具名、
// 插件名都进这条路径），拿它当信任依据等于把"谁能造前缀"变成"谁能写宿主指令"。
// 前缀仍用于展示与审计。
//
// 空内容返回 nil（不占一条消息，与 user/system 空节点同规则）。
func projectInjection(ctx context.Context, m conversation.Message, opt assembleOpts) (*port.PromptMessage, error) {
	if len(m.Content) == 0 {
		return nil, nil
	}
	parts, err := assembleParts(ctx, m.Content, opt)
	if err != nil {
		return nil, err
	}
	if m.Trust == conversation.TrustHost {
		// 用户自己的动作仍是 user 角色：换成 system 会让模型把用户的原话当成
		// 系统设定，回答语气与优先级都会跑偏。
		role := port.PromptSystem
		if m.Source == "user" || strings.HasPrefix(m.Source, "user:") {
			role = port.PromptUser
		}
		return &port.PromptMessage{Role: role, Content: parts}, nil
	}
	// 围栏正文同样中和闭合标签（D170 口径二）：否则外部内容可以提前收尾，
	// 把后文伪装成围栏之外的用户/宿主说明。
	fenced := make([]port.PromptPart, 0, len(parts)+2)
	fenced = append(fenced, port.PromptPart{Kind: port.PromptText, Text: injectFenceNote})
	fenced = append(fenced, port.PromptPart{
		Kind: port.PromptText,
		Text: fmt.Sprintf(injectFenceOpen, escapeFenceAttr(m.Source)),
	})
	for _, p := range parts {
		if p.Kind == port.PromptText {
			p.Text = neutralizeFence(p.Text)
		}
		fenced = append(fenced, p)
	}
	fenced = append(fenced, port.PromptPart{Kind: port.PromptText, Text: injectFenceClose})
	return &port.PromptMessage{Role: port.PromptUser, Content: fenced}, nil
}

// neutralizeFence 中和注入正文里的围栏闭合标签（大小写不敏感）。
//
// 只动这一个标签，不整体做 HTML 转义：查询串、代码片段、正则里的 `<` `>` 一旦被改写，
// 模型读到的东西就和真实文件不一致，而它要靠读到的内容做判断。
func neutralizeFence(s string) string {
	lower := strings.ToLower(s)
	for {
		i := strings.Index(lower, injectFenceClose)
		if i < 0 {
			return s
		}
		s = s[:i] + "&lt;/injected-context&gt;" + s[i+len(injectFenceClose):]
		lower = strings.ToLower(s)
	}
}

// escapeFenceAttr 转义围栏属性值（§9）：`"` 与 `<`/`>` 是标签边界字符，来源名可能来自
// 外部（MCP 工具名），不能让它把围栏提前闭合。
func escapeFenceAttr(s string) string {
	r := strings.NewReplacer(`"`, "&quot;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// assembleAssistantNode 把一个 assistant 节点按工具分片边界切段投影（D95 Turn 粒度）：
// 段 = 连续的思考/正文分片 + 其后的工具分片；「tool 之后再来的 thinking/text」开启新段。
// 每段产出 1 条 assistant 消息（Reasoning=段内思考拼接、Content=段内正文、
// ToolCalls=段内 Result≠nil 的调用）+ N 条 tool 应答消息——与 OpenAI 协议的
// assistant(tool_calls)→tool 应答交错要求一致。全空段不发（如仅 nil 结果分片的轮）。
//
// 工具产图（D169）投影的 user 消息必须排在**全部** tool 应答之后：协议要求
// assistant(tool_calls) 后面紧跟每一条调用对应的 tool 应答，中间夹一条 user 会让
// 「两图两调用」的轮次被判成 insufficient tool messages（各自紧随其后只在前一调用
// 带图、后一调用不带图时碰巧成立）。
func assembleAssistantNode(ctx context.Context, m conversation.Message, opt assembleOpts) ([]port.PromptMessage, error) {
	var out []port.PromptMessage
	var (
		reasoning []string
		thinkSig  string
		content   []port.PromptPart
		calls     []tool.Call
		replies   []port.PromptMessage // tool 应答（紧跟 assistant 的全部调用）
		images    []port.PromptMessage // 工具产图（全部应答之后）
	)
	flush := func() {
		if len(content) == 0 && len(reasoning) == 0 && len(calls) == 0 && thinkSig == "" {
			reasoning, thinkSig, content, calls, replies, images = nil, "", nil, nil, nil, nil
			return
		}
		out = append(out, port.PromptMessage{
			Role:               port.PromptAssistant,
			Content:            content,
			Reasoning:          strings.Join(reasoning, "\n\n"),
			ReasoningSignature: thinkSig,
			ToolCalls:          calls,
		})
		out = append(out, replies...)
		if len(images) > 0 {
			// 多张工具图合成一条 user 消息：连续 user 消息虽然多数端点接受，但
			// 一条消息里「说明句 + 图片」成组更不容易被端点的多模态校验挑刺。
			var parts []port.PromptPart
			for _, im := range images {
				parts = append(parts, im.Content...)
			}
			out = append(out, port.PromptMessage{Role: port.PromptUser, Content: parts})
		}
		reasoning, thinkSig, content, calls, replies, images = nil, "", nil, nil, nil, nil
	}
	for _, p := range m.Content {
		switch p.Kind {
		case conversation.PartThinking:
			if opt.echoThinking { // 关（显式 false）= 丢弃，不回传（D42）
				if len(calls) > 0 || len(replies) > 0 || len(images) > 0 {
					flush() // tool 之后再来的思考：开启新段
				}
				reasoning = append(reasoning, p.Text)
				if p.Signature != "" {
					thinkSig = p.Signature // D175：段内后到的签名覆盖先到的（一轮一签）
				}
			}
		case conversation.PartText, conversation.PartImage, conversation.PartAudio, conversation.PartDoc:
			if len(calls) > 0 || len(replies) > 0 || len(images) > 0 {
				flush() // tool 之后再来的正文：开启新段
			}
			pp, err := assembleParts(ctx, []conversation.Part{p}, opt)
			if err != nil {
				return nil, err
			}
			content = append(content, pp...)
		case conversation.PartTool:
			if p.Tool == nil || p.Tool.Result == nil {
				continue // 未执行/被取消：不声明也不应答（API 不接受无应答的 tool_calls）
			}
			calls = append(calls, tool.Call{
				ID:   tool.CallID(p.Tool.CallID),
				Name: p.Tool.Name,
				Args: p.Tool.Args,
			})
			replies = append(replies, port.PromptMessage{
				Role:    port.PromptTool,
				CallID:  p.Tool.CallID,
				Content: []port.PromptPart{{Kind: port.PromptText, Text: toolResultText(p.Tool.Result)}},
			})
			// 工具产出的图片（D169）：tool 应答之后另起一条 user 消息承载图片块，
			// 因为 OpenAI 兼容端点不接受 tool 角色带图片。先攒着，段末排在全部
			// tool 应答之后发（见函数头注释）。
			img, err := toolImageMessage(ctx, p.Tool.Name, p.Tool.Result.Images, opt)
			if err != nil {
				return nil, err
			}
			if img != nil {
				images = append(images, *img)
			}
		}
	}
	flush()
	return out, nil
}

// toolImageMessage 把工具结果里的图片引用投影为一条 user 消息（D169）。
//
// 为什么是 user 而不是 tool：OpenAI 兼容端点的 tool 角色只收文本，图片块挂在
// tool 应答上会被拒。位置在**这一段全部 tool 应答之后**（D169 修订）：协议要求
// assistant(tool_calls) 后紧跟每条调用的 tool 应答，图片只能排在应答之后——放在
// 每条应答的紧后方时，一轮里两个带图调用会让第二条应答落在 user 之后而被拒。
// 同段多图时靠首行「Image returned by the X call:」把图片与调用对上。
// 视觉得不到时同样投影为文字占位并写明原因：静默丢掉图片，模型会以为工具没产出。
func toolImageMessage(ctx context.Context, toolName string, refs []tool.ImageRef, opt assembleOpts) (*port.PromptMessage, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	parts := make([]port.PromptPart, 0, len(refs)+1)
	parts = append(parts, port.PromptPart{Kind: port.PromptText, Text: fmt.Sprintf(toolImageNoteFmt, toolName)})
	for _, ref := range refs {
		if !opt.vision {
			parts = append(parts, port.PromptPart{Kind: port.PromptText, Text: fmt.Sprintf(imageUnsupportedFmt, ref.Name)})
			continue
		}
		if opt.blobs == nil {
			parts = append(parts, port.PromptPart{Kind: port.PromptText, Text: fmt.Sprintf(imageOmittedFmt, ref.Name)})
			continue
		}
		rc, err := opt.blobs.Get(ctx, conversation.BlobRef{Hash: ref.Hash, MIME: ref.MIME, Name: ref.Name, Size: ref.Size})
		if err != nil {
			return nil, fmt.Errorf("read image %s: %w", ref.Name, err)
		}
		data, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			return nil, fmt.Errorf("read image %s: %w", ref.Name, rerr)
		}
		parts = append(parts, port.PromptPart{Kind: port.PromptImage, MIME: ref.MIME, Data: data})
	}
	return &port.PromptMessage{Role: port.PromptUser, Content: parts}, nil
}

// assembleParts 把领域分片装配为提示词分片：
// audio 取转写文本、doc 取提取文本（D10）、image 经 AttachmentStore 内联字节（§4.2）；
// thinking 不进 Content——由 assemblePath 经 PromptMessage.Reasoning 独立回传（D42）。
func assembleParts(ctx context.Context, parts []conversation.Part, opt assembleOpts) ([]port.PromptPart, error) {
	out := make([]port.PromptPart, 0, len(parts))
	for _, p := range parts {
		switch p.Kind {
		case conversation.PartText:
			out = append(out, port.PromptPart{Kind: port.PromptText, Text: p.Text})
		case conversation.PartAudio:
			// 模型只见文本，音频留附件库回放（§4.2）。
			if p.Transcript != "" {
				out = append(out, port.PromptPart{Kind: port.PromptText, Text: p.Transcript})
			}
		case conversation.PartDoc:
			// 文档正文在入库时已提取并截断存进节点，装配时直接取这段文本（D10）。
			if p.Text != "" {
				out = append(out, port.PromptPart{Kind: port.PromptText, Text: p.Text})
			}
		case conversation.PartImage:
			if p.Ref == nil {
				continue
			}
			if !opt.vision {
				out = append(out, port.PromptPart{Kind: port.PromptText, Text: fmt.Sprintf(imageUnsupportedFmt, p.Ref.Name)})
				continue
			}
			if opt.blobs == nil {
				out = append(out, port.PromptPart{Kind: port.PromptText, Text: fmt.Sprintf(imageOmittedFmt, p.Ref.Name)})
				continue
			}
			rc, err := opt.blobs.Get(ctx, *p.Ref)
			if err != nil {
				return nil, fmt.Errorf("读取附件 %s: %w", p.Ref.Name, err)
			}
			data, rerr := io.ReadAll(rc)
			_ = rc.Close()
			if rerr != nil {
				return nil, fmt.Errorf("read attachment %s: %w", p.Ref.Name, rerr)
			}
			out = append(out, port.PromptPart{Kind: port.PromptImage, MIME: p.Ref.MIME, Data: data})
		case conversation.PartThinking:
			// 思考走 PromptMessage.Reasoning（D42），不与正文混装。
			continue
		case conversation.PartTool:
			// 工具分片不进正文（D95）：调用经 PromptMessage.ToolCalls、结果经 tool 应答消息。
			continue
		default:
			// 未知分片种类：忽略（前向兼容）。
		}
	}
	return out, nil
}

// thinkingText 提取思考分片文本（D42）：流内分片已合并为至多一段，取首个非空；
// 无思考返回空串（展示侧的同名助手在 uitui/repl 各自实现，出口剥控制序列）。
func thinkingText(parts []conversation.Part) string {
	for _, p := range parts {
		if p.Kind == conversation.PartThinking && strings.TrimSpace(p.Text) != "" {
			return p.Text
		}
	}
	return ""
}

// toolResultText 工具结果的提示词文本。
// Output/Err 均为不可信数据，只作文本回填给模型、不执行（§9）。
// 正文进模型上下文，按硬规则用英文（D36）；模型是唯一读者。
func toolResultText(res *tool.Result) string {
	if res == nil {
		return toolNoOutput
	}
	text := strings.TrimSpace(res.Output)
	if res.OK {
		if text == "" {
			return toolNoOutput
		}
		return text
	}
	e := res.Err
	if e == "" {
		e = toolFailedNoReason
	}
	out := toolFailedPrefix + e
	if text != "" {
		out += "\n" + text
	}
	return out
}
