package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execInject 实现 /inject：手动注入一条上下文（D150）。
//
// 用法：/inject [--source <名>] [--no-trigger] <文本>。与 /respond 同形（移 Head 类命令）：
// 入队 → **驱动式排空**（触发则自己跑一轮）→ 清屏回放把新路径（含注入节点）铺开。
//
// 契约要点：
//   - `--source <名>` 任意名（不设白名单）：缺省 `user`——手动注入是用户自己的动作，
//     默认该起一轮（与外部来源的默认表同表，见 session_inject.go）。
//   - `--no-trigger` 显式不起响应（只把上下文塞进树，等下一次输入）；`--source` 给的来源
//     若默认该触发，没有 `--no-trigger` 就会起一轮。
//   - 入队前的校验由 Inject 做（坏包当场拒），本命令只负责把参数拼成 Injection。
func (s *Session) execInject(ctx context.Context, cmd port.Command) (string, error) {
	source, noTrigger, text, err := parseInjectArgs(cmd.Args)
	if err != nil {
		return "", err
	}
	in := port.Injection{
		Source: source,
		// 手动注入是用户自己的动作 → 宿主事实（D170）。`--source` 给任意名也不改信任级：
		// 它只影响展示与触发默认，不是提权开关。
		Trust: port.TrustHost,
		Parts: []conversation.Part{{Kind: conversation.PartText, Text: text}},
	}
	if noTrigger {
		v := false
		in.Trigger = &v
	}
	if err := s.Inject(ctx, in); err != nil {
		return "", err
	}
	// 驱动式排空：本条触发的注入落地后起一轮（Trigger 记 true），不触发的只落地。
	// 排空时 Announce=false——本条命令移动了 Head，走下面的清屏回放统一铺开。
	landed, wantTurn, err := s.drainInjections(ctx, true, false)
	if err != nil {
		return "", err
	}
	if len(landed) == 0 {
		return "注入已入队（未被排空）", nil
	}
	node := landed[len(landed)-1]
	// 移 Head 类命令统一口径（D81/D92）：清屏 + 回放新路径，转写区恒与 Head 一致；
	// 回放在跑轮之前——先让用户看到「注入已就位」，再跑生成。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已注入"); err != nil {
		return "", err
	}
	if wantTurn {
		if err := s.runTurn(ctx); err != nil {
			return "", err
		}
	}
	state := "未触发响应"
	if node.Trigger {
		state = "已触发响应"
	}
	return fmt.Sprintf("已注入一条 %s 上下文（%s；Head → %s）", port.InjectSourceLabel(node.Source), state, s.cur.Head), nil
}

// parseInjectArgs 解析 /inject 参数：`[--source <名>] [--no-trigger] <文本>`（顺序自由、
// 选项可在文本前任意位置）。文本为剩余参数空格拼接（与 /summary 同口径）。
func parseInjectArgs(args []string) (source string, noTrigger bool, text string, err error) {
	source = "user" // 缺省来源：手动注入（默认触发）
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-trigger":
			noTrigger = true
		case "--source":
			if i+1 >= len(args) {
				return "", false, "", errors.New("用法: /inject [--source <名>] [--no-trigger] <文本>（--source 缺参数）")
			}
			source = args[i+1]
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	text = strings.TrimSpace(strings.Join(rest, " "))
	if text == "" {
		return "", false, "", errors.New("用法: /inject [--source <名>] [--no-trigger] <文本>")
	}
	return source, noTrigger, text, nil
}

// 来源友好化表在 port.InjectSourceLabel——命令输出与 GUI 展示共用同一张表（D166）。
