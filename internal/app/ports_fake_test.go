package app

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 命令面四个具名端口的测试替身（P2-4）。放在非 _test 文件里会污染生产包，
// 所以集中写在 _test 里；各替身只记调用、不碰 IO。

// fakePerms app.PermissionSettings 替身：记录写回次序，可选地把开关同步回本地变量。
type fakePerms struct {
	unattended bool
	levels     []perm.Level
	unattend   []bool
	err        error
}

var _ PermissionSettings = (*fakePerms)(nil)

func (f *fakePerms) Unattended() bool { return f.unattended }

func (f *fakePerms) PersistUnattended(on bool) error {
	if f.err != nil {
		return f.err
	}
	f.unattend = append(f.unattend, on)
	f.unattended = on
	return nil
}

func (f *fakePerms) PersistLevel(level perm.Level) error {
	if f.err != nil {
		return f.err
	}
	f.levels = append(f.levels, level)
	return nil
}

// fakeThinking app.ThinkingSettings 替身：记录写回的开关与档位。
type fakeThinking struct {
	thinks  []bool
	efforts []string
	err     error
}

var _ ThinkingSettings = (*fakeThinking)(nil)

func (f *fakeThinking) PersistThink(on bool) error {
	if f.err != nil {
		return f.err
	}
	f.thinks = append(f.thinks, on)
	return nil
}

func (f *fakeThinking) PersistEffort(level string) error {
	if f.err != nil {
		return f.err
	}
	f.efforts = append(f.efforts, level)
	return nil
}

// fakeWatchCancel app.WatcherCanceller 替身：记录被撤销的会话。
type fakeWatchCancel struct{ cancelled []conversation.ID }

var _ WatcherCanceller = (*fakeWatchCancel)(nil)

func (f *fakeWatchCancel) CancelSession(id conversation.ID) {
	f.cancelled = append(f.cancelled, id)
}

// fakeMemories app.MemoryOpener 替身：记录被打开的记忆文档名。
type fakeMemories struct {
	opened []string
	path   string
	err    error
}

var _ MemoryOpener = (*fakeMemories)(nil)

func (f *fakeMemories) Open(name string) (string, error) {
	f.opened = append(f.opened, name)
	if f.err != nil {
		return "", f.err
	}
	if f.path != "" {
		return f.path, nil
	}
	return "/tmp/" + name, nil
}

// fakeModels app.ModelCatalog + app.ModelProbe 替身（模型面测试用）：一次性给全读侧
// 快照与探测结论，并按需记录探测目标。
type fakeModels struct {
	models   []port.ModelInfo
	listErr  error
	status   ModelStatus
	report   ProbeReport
	probeErr error
	probed   []string
}

var (
	_ ModelCatalog = (*fakeModels)(nil)
	_ ModelProbe   = (*fakeModels)(nil)
)

// fakeModelSwitch app.ModelSwitcher 替身：记录写回的模型名，可令其失败。
type fakeModelSwitch struct {
	persisted []string
	err       error
}

var _ ModelSwitcher = (*fakeModelSwitch)(nil)

func (f *fakeModelSwitch) PersistModel(name string) error {
	if f.err != nil {
		return f.err
	}
	f.persisted = append(f.persisted, name)
	return nil
}

// capsPtr 取能力位的指针形式（`ModelStatus.Caps` 用 nil 表示「未声明」，
// 测试里要写「声明了三项中某几项」就得取地址）。
func capsPtr(c port.ModelCaps) *port.ModelCaps { return &c }

// setCaps 换能力位声明；nil 表示「未声明」。
func (f *fakeModels) setCaps(c *port.ModelCaps) { f.status.Caps = c }

func (f *fakeModels) Models(ctx context.Context) ([]port.ModelInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.models, nil
}

func (f *fakeModels) Current() ModelStatus { return f.status }

func (f *fakeModels) Probe(_ context.Context, model string) (ProbeReport, error) {
	f.probed = append(f.probed, model)
	return f.report, f.probeErr
}
