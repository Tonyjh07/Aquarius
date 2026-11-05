package uigui

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 界面动作会投给内核的命令名。静态命令表的正本在 internal/app，这里只登记界面用到的
// 那几条：装配根有一条对账测试保证两边一致，内核改名或删命令时，界面不会编译照过、
// 运行期才报未知命令。新增界面动作先在这张表补一行，别在投递点另写裸字符串。
const (
	uiCmdNew        = "new"
	uiCmdSwitch     = "switch"
	uiCmdRmconv     = "rmconv"
	uiCmdGoto       = "goto"
	uiCmdImport     = "import"
	uiCmdExport     = "export"
	uiCmdSummary    = "summary"
	uiCmdTreeEdit   = "tree-edit"
	uiCmdRegen      = "regen"
	uiCmdRespond    = "respond"
	uiCmdRm         = "rm"
	uiCmdPermission = "permission"
	uiCmdUnattended = "unattended"
	uiCmdEdit       = "edit"
	uiCmdReparent   = "reparent"
	uiCmdCharacter  = "character"
)

// uiCommand 一条界面动作与内核命令的对应关系。From 只写触发面，供排查用，不是事实来源；
// 权威是下面的表与源码里的常量引用，两者由装配根的对账测试互相校验。
type uiCommand struct {
	Name string
	From string
}

// uiCommandTable 界面会投给内核的命令清单。一个界面动作对应一条内核命令，同一条命令可以
// 由多个动作触发（例如 goto 来自分叉条、历史窗与关系图），所以表里按命令去重。
var uiCommandTable = []uiCommand{
	{Name: uiCmdNew, From: "history.newButton, menu.newConversation"},
	{Name: uiCmdSwitch, From: "history.switchRow"},
	{Name: uiCmdRmconv, From: "history.rowDelete, history.batchDelete"},
	{Name: uiCmdGoto, From: "branch.goto, history.gotoNode, relmap.gotoNode"},
	{Name: uiCmdImport, From: "history.importFile, history.importPick"},
	{Name: uiCmdExport, From: "history.exportFile, history.batchExport"},
	{Name: uiCmdSummary, From: "history.summaryDialog, relmap.summary"},
	{Name: uiCmdTreeEdit, From: "history.treeEditSwitch"},
	{Name: uiCmdRegen, From: "menu.bubbleRegen"},
	{Name: uiCmdRespond, From: "menu.nodeRespond"},
	{Name: uiCmdRm, From: "menu.nodeDelete"},
	{Name: uiCmdPermission, From: "menu.permissionLevel, confirm.elevate"},
	{Name: uiCmdUnattended, From: "menu.unattended"},
	{Name: uiCmdEdit, From: "edit.submit, overlay.submit, overlay.personaTemplate"},
	{Name: uiCmdReparent, From: "relmap.reparent"},
	{Name: uiCmdCharacter, From: "settings.characterActions"},
}

// UICommandNames 返回界面会投给内核的命令名副本，供装配根与内核静态命令表对账。
// 返回副本，调用方改不到表本身；装配根的测试拿它与 app.CommandCatalog 比对。
func UICommandNames() []string {
	names := make([]string, len(uiCommandTable))
	for i, e := range uiCommandTable {
		names[i] = e.Name
	}
	return names
}

// postCommand 投一条内核命令（与键入同路径：经 u.inCh；缓冲满丢弃，不阻塞 UI 帧）。
// 历史窗、菜单与设置页原先各写一份 select 包装，差异只在参数与空值判断，这里收成一处。
func postCommand(u *UI, name string, args ...string) bool {
	if u == nil || u.inCh == nil {
		return false
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: name, Args: args}}:
		return true
	default:
		return false
	}
}

// commandLine 拼一条文本命令（经 parseInput 的路径要带斜杠与空格分隔的参数）。
// 折叠条要把命令行记进待发清单，所以那几个动作仍走文本路径，不改用结构化投递。
func commandLine(name string, args ...string) string {
	if len(args) == 0 {
		return "/" + name
	}
	return "/" + name + " " + strings.Join(args, " ")
}
