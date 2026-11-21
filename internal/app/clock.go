package app

// clock.go Agent 的时间感知（D317）：`clock.mode` 三档——off 不注入；live 每次请求末尾一条
// 时间戳 system；tree 在 live 之上给历史 user 消息补各自 CreatedAt。戳**装配时生成**（每个
// 工具轮拿到当下时刻），文本进模型上下文——按 D275 口径用英文代码字面量，不进 i18n 表。
// `/clock [mode]` 查看与切换（会话级，不写配置）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// 时间感知的三个档位名：off 不注入、live 请求末尾一条时间戳、tree 再给历史用户消息
// 补各自时刻（clock.mode 的取值词表，D317）。
const (
	modeClockOff  = "off"
	modeClockLive = "live"
	modeClockTree = "tree"
)

// clockStamp 生成时钟段的文本（进模型上下文，英文——D275 口径，同 dateChangeNoteFmt）。
//
// 三段：日期时刻（时区偏移 + 星期）、本 Turn 已跑多久、第二轮起附上一批工具各自耗时。
// 模型要的是「现在几点、刚才那步等了多久」，不是全程流水——耗时只报上一批。
func clockStamp(now time.Time, buf *turnBuffer, mode string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Current time: %s (UTC%s, %s).",
		now.Local().Format("2006-01-02 15:04:05"),
		now.Local().Format("-07:00"),
		now.Local().Weekday().String())
	if buf == nil { // buildRequest 无缓冲的调用方（UsageReport 一类）：只有时刻，没有计时
		return b.String()
	}
	if !buf.turnStart.IsZero() {
		fmt.Fprintf(&b, " This turn has been running for %s.", elapsedHuman(now.Sub(buf.turnStart)))
	}
	if len(buf.lastBatch) > 0 {
		items := make([]string, 0, len(buf.lastBatch))
		for _, d := range buf.lastBatch {
			items = append(items, fmt.Sprintf("%s %s", d.name, elapsedHuman(d.d)))
		}
		fmt.Fprintf(&b, " Previous tool batch: %s.", strings.Join(items, ", "))
	}
	return b.String()
}

// elapsedHuman 时长的读法：不到 1s 显示毫秒级粒度、其余分秒——模型读相对刻度比读
// 纳秒更有用。
func elapsedHuman(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(100 * time.Millisecond).String()
}

// userClockSuffix tree 模式给历史 user 消息末尾拼的时刻行（D317：装配期投影，不落节点）。
func userClockSuffix(at time.Time) string {
	return fmt.Sprintf("\n[At: %s (UTC%s, %s)]",
		at.Local().Format("2006-01-02 15:04"),
		at.Local().Format("-07:00"),
		at.Local().Weekday().String())
}

// execClock 实现 /clock：无参显示当前档；有参切换（会话级，不写配置——D317 决策 5）。
func (s *Session) execClock(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return i18n.Tf("clock.show", "mode", s.agent.clockState()), nil
	}
	mode := strings.ToLower(strings.TrimSpace(args[0]))
	switch mode {
	case modeClockOff, modeClockLive, modeClockTree:
		s.agent.setClockMode(mode)
		return i18n.Tf("clock.switched", "mode", mode), nil
	default:
		return "", errors.New(i18n.T("usage.clock"))
	}
}
