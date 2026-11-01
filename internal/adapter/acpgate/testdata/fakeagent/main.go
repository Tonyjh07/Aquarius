// Command fakeagent 是一个脚本化的 ACP 代理（只用于测试）：说 newline-delimited
// JSON-RPC，行为由 FAKE_AGENT_MODE 决定。它不进任何构建产物（testdata 目录被 go
// 工具忽略，测试时现编）。
//
// 模式：
//
//	ok         握手 → 建会话 → 提示：发正文/工具调用/改动文件 → end_turn
//	auth       initialize 回 authMethods（对方没登录）
//	v2         initialize 报协议版本 2
//	crash      initialize 之后直接退出（stderr 留一行）
//	slow       提示一直不回，收到 session/cancel 才回 cancelled
//	ignore     提示一直不回，连 cancel 也不理（测试宽限杀进程）
//	permission 提示里先反问权限，按应答回一段正文
//	badcwd     要求 cwd 是绝对路径、mcpServers 为空，否则回 -32602
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
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
)

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
		case "crash":
			fmt.Fprintln(os.Stderr, "fake agent: boom on purpose")
			os.Exit(3)
		}
		reply(m.ID, map[string]any{
			"protocolVersion": 1,
			"agentInfo":       map[string]any{"name": "fake", "title": "Fake Agent", "version": "9.9"},
			"authMethods":     []any{},
			"agentCapabilities": map[string]any{
				"promptCapabilities": map[string]any{"image": false, "audio": false, "embeddedContext": false},
			},
		})

	case "session/new":
		var p struct {
			Cwd        string `json:"cwd"`
			McpServers []any  `json:"mcpServers"`
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
		reply(m.ID, map[string]any{"sessionId": "sess-1"})

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

// neverIfIgnore ignore 模式下永远阻塞（等被 kill）；slow 模式返回一个永不就绪的 channel。
func neverIfIgnore(mode string) <-chan struct{} {
	if mode == "ignore" {
		select {}
	}
	return make(chan struct{})
}
