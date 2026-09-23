package port

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// Event UI 事件（联合类型）：DeltaEvent | ToolCallEvent | ToolResultEvent | CommittedEvent | ErrorEvent。
type Event any

// DeltaEvent 流式增量，只进 UI 不进领域（D3）；MessageID 为 Turn 开始时预分配的关联 ID。
type DeltaEvent struct {
	MessageID conversation.MessageID
	Delta     Delta
}

// ToolCallEvent 模型发起工具调用。
type ToolCallEvent struct {
	MessageID conversation.MessageID
	Call      tool.Call
}

// ToolResultEvent 工具执行回填。
type ToolResultEvent struct {
	Result tool.Result
}

// CommittedEvent Turn 结束一次性提交的不可变节点。
type CommittedEvent struct {
	Message conversation.Message
}

// ErrorEvent 错误上抛 UI。
type ErrorEvent struct {
	Err error
}

// Presenter 事件呈现端口。
type Presenter interface {
	Emit(ctx context.Context, ev Event) error
}

// Prompter 用户输入端口：文本、命令、多模态原始输入走同一入口。
type Prompter interface {
	Next(ctx context.Context) (UserInput, error)
}

// UserInput 一次用户输入。
type UserInput struct {
	Text    string    // "" = 命令
	Command *Command  // 斜杠命令
	Raw     *RawInput // 拖拽文件 / 粘贴图片 / 语音按钮走同一入口
}

// Command 斜杠命令。
type Command struct {
	Name string
	Args []string
}
