// Command fakeagent 是一个脚本化的 ACP 代理（只用于测试）：说 newline-delimited
// JSON-RPC，行为由 FAKE_AGENT_MODE 决定。它不进任何构建产物（testdata 目录被 go
// 工具忽略，测试时现编）。
//
// 模式：
//
//	ok         握手 → 建会话 → 提示：发正文/工具调用/改动文件 → end_turn
//	auth       initialize 回 authMethods，session/new 回 -32000（真没登录）
//	ok         的 initialize 也带一条 authMethods（已登录的代理照样会报，不代表没登录）
//	v2         initialize 报协议版本 2
//	crash      initialize 之后直接退出（stderr 留一行）
//	slow       提示一直不回，收到 session/cancel 才回 cancelled
//	ignore     提示一直不回，连 cancel 也不理（测试宽限杀进程）
//	permission 提示里先反问权限，按应答回一段正文
//	partial    先发两段正文，然后直接退出（测试「中途失败也要把已收到的带出去」）
//	additional initialize 申报 sessionCapabilities.additionalDirectories，且 session/new
//	           不带这个字段就回 -32602（验证客户端确实发出去了）
//	fs         按 fs 模式发五个文件请求（读切片 / 写新文件建父目录 / 越界写 / 越界读 /
//	           写执行控制文件），把每个应答原样编进正文——断言全在测试里做
//	caps       把 initialize 收到的 clientCapabilities 原样编进正文（验证申报了什么）
//	terminal   把五个 terminal/* 方法各走一遍（建两条命令、取输出、等退出、杀、释放，
//	           再取一次已释放的 id），应答原样编进正文；命令路径由 FAKE_AGENT_CMD 给
//	delayed    先报一句进度，等 FAKE_AGENT_DELAY_MS（缺省 8000）毫秒再交报告——手验
//	           「派活后切走，报告仍落在原会话」靠它，秒回测不出时间差
//	badcwd     要求 cwd 是绝对路径、mcpServers 为空，否则回 -32602
//	options    session/new 返回一批会话配置选项（model / thinking / mode / 分组形状的
//	           variant / 开关形状的 verbose），并处理 session/set_config_option；
//	           thinking 的候选跟着当前模型走（切到 beta 才有 low/high），用来验证
//	           设置的顺序与快照更新；prompt 时把收到的设置编进正文
//	options-echobad 同 options，但设 model 后回报旧的 currentValue（验证客户端必须读回）
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var (
	outMu      sync.Mutex
	pending    sync.Map // request id (string) → chan rpcMsg
	nextID     int
	idMu       sync.Mutex
	cancel     = make(chan struct{})
	cancelOnce sync.Once
	// sessCwd session/new 给的 cwd：fs 模式下用它拼路径（真代理也是这么来的）。
	sessCwd string
	// clientCaps initialize 收到的客户端能力（caps 模式下原样回吐）。
	clientCaps json.RawMessage
	// fakeOpts options 模式的会话配置选项状态。
	fakeOpts = newFakeOptions()
)

// ---- options 模式：会话配置选项 ----

// fakeOptions 会话配置选项的假状态。thinking 的候选跟着当前模型走——真代理就是这样
// （kimi 的 supportEfforts 随模型变），这也是「先设模型再设 thinking」那条顺序的理由：
// 顺序反了，high 在当前模型的候选里根本不存在。
type fakeOptions struct {
	mu       sync.Mutex
	model    string
	thinking string
	mode     string
	variant  string
	sets     []string // 收到的设置，按到达序记 "id=value"
}

func newFakeOptions() *fakeOptions {
	return &fakeOptions{model: "alpha/model-1", thinking: "off", mode: "default", variant: "plain"}
}

// thinkingAllowed 当前模型允许的档位（alpha 只有一个，beta 有三个）。
func (f *fakeOptions) thinkingAllowed() []string {
	if f.model == "beta/model-2" {
		return []string{"off", "low", "high"}
	}
	return []string{"off"}
}

// snapshot 当前配置快照：select 与 boolean 两支都在，候选同时覆盖不分组与分组两种形状。
func (f *fakeOptions) snapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var levels []any
	for _, v := range f.thinkingAllowed() {
		levels = append(levels, map[string]any{"value": v, "name": strings.ToUpper(v)})
	}
	return []map[string]any{
		{
			"type": "select", "id": "model", "name": "Model", "category": "model",
			"currentValue": f.model,
			"options": []any{
				map[string]any{"value": "alpha/model-1", "name": "Alpha One"},
				map[string]any{"value": "beta/model-2", "name": "Beta Two"},
				// 广告了却设不上：真代理就这样（opencode 的 deepseek/* 全在候选里，
				// 设它回 model not found），用来验证我们把代理的拒绝原样带出去。
				map[string]any{"value": "gamma/model-3", "name": "Gamma Three"},
			},
		},
		{
			"type": "select", "id": "thinking", "name": "Thinking", "category": "thought_level",
			"currentValue": f.thinking, "options": levels,
		},
		{
			"type": "select", "id": "mode", "name": "Mode", "category": "mode",
			"currentValue": f.mode,
			"options": []any{
				map[string]any{"value": "default", "name": "Default"},
				map[string]any{"value": "plan", "name": "Plan"},
			},
		},
		{
			// 分组形状：展平后才找得到 fancy。
			"type": "select", "id": "variant", "name": "Variant", "currentValue": f.variant,
			"options": []any{
				map[string]any{"group": "g1", "name": "Group One", "options": []any{
					map[string]any{"value": "plain", "name": "Plain"},
				}},
				map[string]any{"group": "g2", "name": "Group Two", "options": []any{
					map[string]any{"value": "fancy", "name": "Fancy"},
				}},
			},
		},
		{
			"type": "boolean", "id": "verbose", "name": "Verbose", "currentValue": false,
		},
	}
}

// set 处理一次设置。返回值即代理给出的拒绝理由（原样回到客户端）。
// echoBad 为真时「设置成功但不生效」，用来验证客户端必须读回 currentValue。
func (f *fakeOptions) set(echoBad bool, id, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, id+"="+value)
	switch id {
	case "model":
		if value != "alpha/model-1" && value != "beta/model-2" {
			// gamma/model-3 也在候选里，但这里照样拒：广告过的值不一定设得上。
			return fmt.Errorf("model not found: %s", value)
		}
		if echoBad {
			return nil
		}
		f.model = value
		f.thinking = "off" // 换了模型，旧档位不保证还在
	case "thinking":
		if !hasString(f.thinkingAllowed(), value) {
			return fmt.Errorf("thinking cannot be %s while the model is %s", value, f.model)
		}
		f.thinking = value
	case "mode":
		if value != "default" && value != "plan" {
			return fmt.Errorf("unknown mode: %s", value)
		}
		f.mode = value
	case "variant":
		if value != "plain" && value != "fancy" {
			return fmt.Errorf("unknown variant: %s", value)
		}
		f.variant = value
	case "verbose":
		return fmt.Errorf("%s is a on/off switch", id)
	default:
		return fmt.Errorf("unknown config option: %s", id)
	}
	return nil
}

func (f *fakeOptions) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.sets, ",")
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func send(v any) {
	outMu.Lock()
	defer outMu.Unlock()
	b, _ := json.Marshal(v)
	os.Stdout.Write(append(b, '\n'))
}

func reply(id json.RawMessage, result any) {
	send(rpcMsg{JSONRPC: "2.0", ID: id, Result: result})
}

func fail(id json.RawMessage, code int, msg string) {
	send(rpcMsg{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

// notify 发一条 session/update 通知。
func notify(sessionID, update string, body map[string]any) {
	u := map[string]any{"sessionUpdate": update}
	for k, v := range body {
		u[k] = v
	}
	send(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params":  map[string]any{"sessionId": sessionID, "update": u},
	})
}

// request 向客户端发请求并等应答（只用于 request_permission）。
func request(method string, params any) rpcMsg {
	idMu.Lock()
	nextID++
	id := nextID
	idMu.Unlock()
	ch := make(chan rpcMsg, 1)
	pending.Store(fmt.Sprint(id), ch)
	send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return <-ch
}

func main() {
	mode := os.Getenv("FAKE_AGENT_MODE")
	if mode == "" {
		mode = "ok"
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m rpcMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go handleRequest(mode, m)
		case m.Method != "":
			handleNotification(m)
		default:
			// 应答 id 原样等到 "1"（与 request 登记时同一个键）。
			if v, ok := pending.LoadAndDelete(string(m.ID)); ok {
				v.(chan rpcMsg) <- m
			}
		}
	}
}

func handleNotification(m rpcMsg) {
	if m.Method == "session/cancel" {
		cancelOnce.Do(func() { close(cancel) })
	}
}

func handleRequest(mode string, m rpcMsg) {
	switch m.Method {
	case "initialize":
		var ip struct {
			ClientCapabilities json.RawMessage `json:"clientCapabilities"`
		}
		_ = json.Unmarshal(m.Params, &ip)
		clientCaps = ip.ClientCapabilities
		switch mode {
		case "auth":
			reply(m.ID, map[string]any{
				"protocolVersion": 1,
				"agentInfo":       map[string]any{"name": "fake", "title": "Fake Agent", "version": "9.9"},
				"authMethods":     []any{map[string]any{"type": "agent", "id": "login", "name": "Log in first"}},
			})
			return
		case "v2":
			reply(m.ID, map[string]any{"protocolVersion": 2, "authMethods": []any{}})
			return
		case "additional":
			reply(m.ID, map[string]any{
				"protocolVersion": 1,
				"agentInfo":       map[string]any{"name": "fake", "title": "Fake Agent", "version": "9.9"},
				"authMethods":     []any{},
				"agentCapabilities": map[string]any{
					"sessionCapabilities": map[string]any{"additionalDirectories": map[string]any{}},
				},
			})
			return
		case "crash":
			fmt.Fprintln(os.Stderr, "fake agent: boom on purpose")
			os.Exit(3)
		}
		reply(m.ID, map[string]any{
			"protocolVersion": 1,
			"agentInfo":       map[string]any{"name": "fake", "title": "Fake Agent", "version": "9.9"},
			// 真的代理（gemini-cli 这类）已登录也会照报自己支持哪些登录方式；这一条
			// 留在默认路径上是回归护栏：谁再拿「authMethods 非空」判「没登录」，ok
			// 系列用例会一起红。
			"authMethods": []any{map[string]any{"type": "agent", "id": "login", "name": "Log in first"}},
			"agentCapabilities": map[string]any{
				"promptCapabilities": map[string]any{"image": false, "audio": false, "embeddedContext": false},
			},
		})

	case "session/new":
		if mode == "auth" {
			// 真没登录的代理在这里拒绝（JSON-RPC -32000 auth required），而不是在
			// initialize 里用 authMethods 表达。
			fail(m.ID, -32000, "authentication required")
			return
		}
		var p struct {
			Cwd                   string   `json:"cwd"`
			McpServers            []any    `json:"mcpServers"`
			AdditionalDirectories []string `json:"additionalDirectories"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if !strings.HasPrefix(p.Cwd, "/") && !(len(p.Cwd) > 1 && p.Cwd[1] == ':') {
			fail(m.ID, -32602, "cwd must be an absolute path")
			return
		}
		if p.McpServers == nil || len(p.McpServers) != 0 {
			fail(m.ID, -32602, "mcpServers must be an empty list")
			return
		}
		if mode == "additional" && len(p.AdditionalDirectories) == 0 {
			// 这一模式扮演「申报了 additionalDirectories 的代理」：客户端不发就该被拒，
			// 发了则原样回显，测试据此断言发的是哪几个根。
			fail(m.ID, -32602, "additionalDirectories is required in this mode")
			return
		}
		sessCwd = p.Cwd
		ns := map[string]any{"sessionId": "sess-1"}
		if strings.HasPrefix(mode, "options") {
			ns["configOptions"] = fakeOpts.snapshot()
		}
		reply(m.ID, ns)

	case "session/set_config_option":
		if !strings.HasPrefix(mode, "options") {
			fail(m.ID, -32601, "method not found")
			return
		}
		var p struct {
			SessionId string `json:"sessionId"`
			ConfigId  string `json:"configId"`
			Value     string `json:"value"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if err := fakeOpts.set(mode == "options-echobad", p.ConfigId, p.Value); err != nil {
			fail(m.ID, -32602, err.Error())
			return
		}
		reply(m.ID, map[string]any{"configOptions": fakeOpts.snapshot()})

	case "session/prompt":
		var p struct {
			SessionId string `json:"sessionId"`
			Prompt    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"prompt"`
		}
		_ = json.Unmarshal(m.Params, &p)
		sid := p.SessionId
		switch mode {
		case "slow", "ignore":
			select {
			case <-cancel:
				reply(m.ID, map[string]any{"stopReason": "cancelled"})
			case <-neverIfIgnore(mode):
				// ignore：等进程被杀
			}
		case "permission":
			resp := request("session/request_permission", map[string]any{
				"sessionId": sid,
				"toolCall": map[string]any{
					"toolCallId": "t1",
					"title":      "Write src/main.go",
					"content": []any{map[string]any{
						"type":    "content",
						"content": map[string]any{"type": "text", "text": "overwrite src/main.go"},
					}},
				},
				"options": []any{
					map[string]any{"optionId": "allow", "name": "Allow once", "kind": "allow_once"},
					map[string]any{"optionId": "deny", "name": "Reject", "kind": "reject_once"},
				},
			})
			raw, _ := json.Marshal(resp)
			txt := "permission: " + string(raw)
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": txt}})
			reply(m.ID, map[string]any{"stopReason": "end_turn"})

		case "fs":
			// 四个请求固定顺序：读切片、整读、写新文件（父目录不存在）、越界写、越界读。
			// 断言全在测试侧做——这里只把每个应答原样带出去。
			esc := os.Getenv("FAKE_AGENT_ESCAPE")
			out := map[string]string{
				"slice": fsResult(request("fs/read_text_file", map[string]any{
					"sessionId": sid, "path": filepath.Join(sessCwd, "hello.txt"), "line": 2, "limit": 1,
				})),
				"whole": fsResult(request("fs/read_text_file", map[string]any{
					"sessionId": sid, "path": filepath.Join(sessCwd, "whole.txt"),
				})),
				"write": fsResult(request("fs/write_text_file", map[string]any{
					"sessionId": sid, "path": filepath.Join(sessCwd, "sub", "dir", "new.txt"),
					"content": "written by agent\nsecond line\n",
				})),
				"escape_write": fsResult(request("fs/write_text_file", map[string]any{
					"sessionId": sid, "path": filepath.Join(esc, "outside.txt"), "content": "should not land\n",
				})),
				"escape_read": fsResult(request("fs/read_text_file", map[string]any{
					"sessionId": sid, "path": filepath.Join(esc, "secret.txt"),
				})),
				"control_write": fsResult(request("fs/write_text_file", map[string]any{
					"sessionId": sid, "path": filepath.Join(sessCwd, ".git", "hooks", "pre-commit"),
					"content": "#!/bin/sh\nexit 0\n",
				})),
			}
			raw, _ := json.Marshal(out)
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": string(raw)}})
			reply(m.ID, map[string]any{"stopReason": "end_turn"})

		case "caps":
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": string(clientCaps)}})
			reply(m.ID, map[string]any{"stopReason": "end_turn"})

		case "options":
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "sets: " + fakeOpts.log()}})
			reply(m.ID, map[string]any{"stopReason": "end_turn"})

		case "terminal":
			out := map[string]string{}
			cmd := os.Getenv("FAKE_AGENT_CMD")
			if cmd == "" {
				fail(m.ID, -32603, "FAKE_AGENT_CMD is not set")
				return
			}
			// 1. 一条会自己结束的命令：不显式给 cwd（验证缺省 = 会话 cwd），
			//    环境变量原样传下去。
			ca := request("terminal/create", map[string]any{
				"sessionId": sid, "command": cmd, "args": []string{"one", "two"},
				"env": []any{
					map[string]any{"name": "ECHOCMD_ECHO", "value": "FOO"},
					map[string]any{"name": "FOO", "value": "bar"},
					map[string]any{"name": "AQUARIUS_SECRET", "value": "leak"},
				},
				"outputByteLimit": 65536,
			})
			out["create_a"] = fsResult(ca)
			ida := terminalID(ca)
			if ida != "" {
				out["output_a"] = fsResult(pollOutput(sid, ida))
				out["wait_a"] = fsResult(request("terminal/wait_for_exit",
					map[string]any{"sessionId": sid, "terminalId": ida}))
			}
			// 2. 一条长命令：活着时 output 没有 exitStatus；kill 之后仍要能拿到退出信息。
			cb := request("terminal/create", map[string]any{
				"sessionId": sid, "command": cmd, "args": []string{"sleep"},
				"env":             []any{map[string]any{"name": "ECHOCMD_SLEEP_MS", "value": "30000"}},
				"outputByteLimit": 4096,
			})
			out["create_b"] = fsResult(cb)
			idb := terminalID(cb)
			if idb != "" {
				out["output_b"] = fsResult(request("terminal/output",
					map[string]any{"sessionId": sid, "terminalId": idb}))
				out["kill_b"] = fsResult(request("terminal/kill",
					map[string]any{"sessionId": sid, "terminalId": idb}))
				out["wait_b"] = fsResult(request("terminal/wait_for_exit",
					map[string]any{"sessionId": sid, "terminalId": idb}))
				out["release_b"] = fsResult(request("terminal/release",
					map[string]any{"sessionId": sid, "terminalId": idb}))
			}
			if ida != "" {
				out["release_a"] = fsResult(request("terminal/release",
					map[string]any{"sessionId": sid, "terminalId": ida}))
				// 释放之后再取：id 已经不认识，这就是「release 才清」的样子。
				out["output_released"] = fsResult(request("terminal/output",
					map[string]any{"sessionId": sid, "terminalId": ida}))
			}
			raw, _ := json.Marshal(out)
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": string(raw)}})
			reply(m.ID, map[string]any{"stopReason": "end_turn"})

		case "delayed":
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "已收到，正在处理。"}})
			select {
			case <-cancel:
				reply(m.ID, map[string]any{"stopReason": "cancelled"})
				return
			case <-time.After(fakeDelay()):
			}
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "\n\n处理完了：三处改动，全部通过。"}})
			notify(sid, "tool_call", map[string]any{
				"toolCallId": "t9", "title": "edit file", "kind": "edit",
				"locations": []any{map[string]any{"path": "src/a.go"}, map[string]any{"path": "src/b.go"}},
			})
			reply(m.ID, map[string]any{"stopReason": "end_turn"})

		case "partial":
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "第一段结论：缓存层没问题。"}})
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "第二段：开始改 src/cache.go"}})
			fmt.Fprintln(os.Stderr, "fake agent: died mid-turn")
			os.Exit(4)

		default:
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "Hello "}})
			notify(sid, "tool_call", map[string]any{
				"toolCallId": "t1", "title": "read file", "kind": "read",
				"locations": []any{map[string]any{"path": "/tmp/a.go"}},
			})
			notify(sid, "tool_call", map[string]any{
				"toolCallId": "t2", "title": "edit file", "kind": "edit",
			})
			notify(sid, "tool_call_update", map[string]any{
				"toolCallId": "t2",
				"locations":  []any{map[string]any{"path": "/tmp/a.go"}, map[string]any{"path": "/tmp/b.go"}},
			})
			notify(sid, "agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "world"}})
			reply(m.ID, map[string]any{"stopReason": "end_turn"})
		}

	default:
		fail(m.ID, -32601, "method not found: "+m.Method)
	}
}

// fsResult 一个 fs 应答的紧凑描述：成功给结果 JSON，失败给「error 码: 消息」。
// 测试拿它断言闸门放行还是拒绝，不在这里做判断。
func fsResult(m rpcMsg) string {
	if m.Error != nil {
		return fmt.Sprintf("error %d: %s", m.Error.Code, m.Error.Message)
	}
	b, err := json.Marshal(m.Result)
	if err != nil {
		return "unencodable result"
	}
	return string(b)
}

// terminalID 从 terminal/create 的应答里取 id（失败时空串）。
func terminalID(m rpcMsg) string {
	if m.Error != nil {
		return ""
	}
	if res, ok := m.Result.(map[string]any); ok {
		if id, ok := res["terminalId"].(string); ok {
			return id
		}
	}
	return ""
}

// pollOutput 轮询 terminal/output 直到出现 exitStatus（最多 5 秒），返回最后一次应答。
// 真代理也是这么用的（kimi 每 250 ms 取一次），所以这里顺带验证「退出后还能取到输出」。
func pollOutput(sid, id string) rpcMsg {
	params := map[string]any{"sessionId": sid, "terminalId": id}
	var last rpcMsg
	for i := 0; i < 100; i++ {
		last = request("terminal/output", params)
		if last.Error != nil {
			return last
		}
		if res, ok := last.Result.(map[string]any); ok && res["exitStatus"] != nil {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	return last
}

// fakeDelay 延后模式等多久（手验时可以直接调成 1000 让节奏快一点）。
func fakeDelay() time.Duration {
	if ms, err := strconv.Atoi(os.Getenv("FAKE_AGENT_DELAY_MS")); err == nil && ms >= 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 8 * time.Second
}

// neverIfIgnore ignore 模式下永远阻塞（等被 kill）；slow 模式返回一个永不就绪的 channel。
func neverIfIgnore(mode string) <-chan struct{} {
	if mode == "ignore" {
		select {}
	}
	return make(chan struct{})
}
