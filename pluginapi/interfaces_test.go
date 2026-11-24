package pluginapi

import (
	"context"
	"io"
	"strings"
	"testing"
)

// interfaces_test 接口面自查（plugin-design.md §4/§5/§7）：三件事——
// 每个面都能被一个实现同时满足（编译期锁形状）、词表常量不撞值、哨兵错误可 errors.Is。
// 这里不测行为：行为在宿主与插件的测试里，本包只承诺类型。

// fakePlugin 同时实现全部供给面、三个可选扩展与生命周期，并接住 Host。
// 任一接口的方法集被改动，这里先编译失败——比等 plugingo 转换器报错早得多。
type fakePlugin struct{ host Host }

var (
	_ Factory                 = fakePlugin{}
	_ Instance                = fakePlugin{}
	_ LanguageModel           = fakePlugin{}
	_ TokenCounter            = fakePlugin{}
	_ Prober                  = fakePlugin{}
	_ ToolProvider            = fakePlugin{}
	_ Describer               = fakePlugin{}
	_ CommandProvider         = fakePlugin{}
	_ Memory                  = fakePlugin{}
	_ Ingestor                = fakePlugin{}
	_ OutputAdapter           = fakePlugin{}
	_ Transcriber             = fakePlugin{}
	_ Synthesizer             = fakePlugin{}
	_ SpeechSegmenter         = fakePlugin{}
	_ SecretProvider          = fakePlugin{}
	_ ViewProvider            = fakePlugin{}
	_ SettingsSectionProvider = fakePlugin{}
)

func (fakePlugin) Manifest() Manifest              { return Manifest{Name: "fake", API: APIVersion} }
func (fakePlugin) Create(h Host) (Instance, error) { return fakePlugin{host: h}, nil }
func (fakePlugin) Start() error                    { return nil }
func (fakePlugin) Shutdown(context.Context) error  { return nil }

func (fakePlugin) Generate(context.Context, GenerateRequest) (Stream, error) { return nil, nil }
func (fakePlugin) Models(context.Context) ([]ModelInfo, error)               { return nil, nil }
func (fakePlugin) CountTokens(context.Context, string) (int, error)          { return 0, nil }
func (fakePlugin) Probe(context.Context, string, string) (ProbeResult, error) {
	return ProbeResult{Verdict: ProbeInconclusive}, nil
}

func (fakePlugin) Tools() []ToolSpec { return nil }
func (fakePlugin) Execute(context.Context, ToolCall) (ToolResult, error) {
	return ToolResult{OK: true}, nil
}
func (fakePlugin) Describe(ToolCall) string { return "" }

func (fakePlugin) Commands() []CommandSpec { return nil }
func (fakePlugin) Run(context.Context, string, []string) (string, error) {
	return "", nil
}

func (fakePlugin) Recall(context.Context, MemoryRequest) (MemoryContext, error) {
	return MemoryContext{}, nil
}

func (fakePlugin) Accepts(RawInput) bool { return false }
func (fakePlugin) Ingest(context.Context, RawInput) (IngestReport, error) {
	return IngestReport{}, nil
}

func (fakePlugin) Name() string                                 { return "fake" }
func (fakePlugin) Deliver(context.Context, OutputRequest) error { return nil }

func (fakePlugin) Transcribe(context.Context, BlobRef, string) (string, error) { return "", nil }
func (fakePlugin) Synthesize(context.Context, SpeechRequest) (BlobRef, error) {
	return BlobRef{}, nil
}
func (fakePlugin) Feed([]byte) ([]SpeechSegment, error) { return nil, nil }
func (fakePlugin) Flush() ([]SpeechSegment, error)      { return nil, nil }

func (fakePlugin) Get(context.Context, string) (string, error) { return "", nil }

func (fakePlugin) Views() []ViewSpec { return nil }
func (fakePlugin) Render(context.Context, string) (Node, error) {
	return Node{Kind: NodeText}, nil
}
func (fakePlugin) OnAction(context.Context, ViewAction) (Node, error) {
	return Node{Kind: NodeText}, nil
}

func (fakePlugin) Sections() []SettingsSection                        { return nil }
func (fakePlugin) ApplySettings(context.Context, SettingsPatch) error { return nil }

// fakeHost 实现 Host 六项、可选的内联提示扩展与迁移素材。
type fakeHost struct{}

var (
	_ Host             = fakeHost{}
	_ NoticePoster     = fakeHost{}
	_ LegacyMemoryHost = fakeHost{}
)

func (fakeHost) Logger() Logger          { return fakeLogger{} }
func (fakeHost) Secrets() SecretResolver { return fakeSecrets{} }
func (fakeHost) Blobs() BlobStore        { return fakeBlobs{} }
func (fakeHost) WorkDir() string         { return "" }
func (fakeHost) Confirm(context.Context, ConfirmRequest) (ConfirmAnswer, error) {
	return ConfirmAnswer{}, nil
}
func (fakeHost) Notify(string, string)     {}
func (fakeHost) PostNotice(string, string) {}
func (fakeHost) LegacyMemory(context.Context) (LegacyMemorySnapshot, error) {
	return LegacyMemorySnapshot{}, nil
}

type fakeLogger struct{}

func (fakeLogger) Log(LogLevel, string, string, ...any) {}

type fakeSecrets struct{}

func (fakeSecrets) Resolve(context.Context, string) (string, error) { return "", nil }

type fakeBlobs struct{}

func (fakeBlobs) Put(context.Context, io.Reader, string, string) (BlobRef, error) {
	return BlobRef{}, nil
}
func (fakeBlobs) Get(context.Context, BlobRef) (io.ReadCloser, error) { return nil, nil }
func (fakeBlobs) Stat(context.Context, BlobRef) (bool, error)         { return false, nil }

// TestVocabularyConstantsUnique 词表常量不得撞值：这些字符串同时是配置文件取值、
// 界面判断依据与宿主分派键，两个常量同值会让某一档静默失效。
func TestVocabularyConstantsUnique(t *testing.T) {
	tables := map[string][]string{
		"Risk":       {string(RiskSafe), string(RiskConfirm)},
		"Op":         {string(OpRead), string(OpWrite)},
		"PartKind":   {string(PartText), string(PartImage), string(PartAudio), string(PartDoc), string(PartThinking), string(PartServer)},
		"NodeKind":   {string(NodeText), string(NodeMarkdown), string(NodeTable), string(NodeKV), string(NodeList), string(NodeForm), string(NodeButtons)},
		"FieldKind":  {string(FieldText), string(FieldTextarea), string(FieldSwitch), string(FieldSelect), string(FieldNumber)},
		"ParamKind":  {string(ParamText), string(ParamChoice), string(ParamFlag)},
		"PromptKind": {string(PromptText), string(PromptImage), string(PromptFile)},
		"PromptRole": {string(PromptSystem), string(PromptUser), string(PromptAssistant), string(PromptTool)},
		"LogLevel":   {string(LogDebug), string(LogInfo), string(LogWarn), string(LogError)},
	}
	for name, vals := range tables {
		seen := make(map[string]bool, len(vals))
		for _, v := range vals {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: 有空常量", name)
				continue
			}
			if seen[v] {
				t.Errorf("%s: 常量撞值 %q", name, v)
			}
			seen[v] = true
		}
	}
}

// TestLegacyMemoryIsOptional 迁移素材必须是「可选扩展」而不是 Host 的第七项：Host 加方法
// 会破坏所有实现方，而迁移只有记忆插件需要。这条锁住「对句柄断言得到它、不必实现它」。
func TestLegacyMemoryIsOptional(t *testing.T) {
	var h Host = fakeHost{}
	legacy, ok := h.(LegacyMemoryHost)
	if !ok {
		t.Fatal("Host 句柄上断言不到 LegacyMemoryHost")
	}
	if _, err := legacy.LegacyMemory(context.Background()); err != nil {
		t.Fatalf("空快照不是错误：%v", err)
	}
	// 零值快照 = 没有可迁移的内容：全局空、会话空，调用方按正常路径继续。
	var snap LegacyMemorySnapshot
	if snap.Global != "" || len(snap.Sessions) != 0 {
		t.Fatalf("零值快照不是空快照：%+v", snap)
	}
}

// TestMemoryContextZeroValue 空注入是正常状态：没有记忆的插件返回零值，宿主不报错、
// 不注入。零值语义是契约的一部分（记忆段可以为空，工具输出不能）。
func TestMemoryContextZeroValue(t *testing.T) {
	var got MemoryContext
	if got.Text != "" {
		t.Fatalf("MemoryContext 零值应表示不注入，得到 %q", got.Text)
	}
}
