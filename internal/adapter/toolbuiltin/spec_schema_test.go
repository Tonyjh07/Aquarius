package toolbuiltin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// schemaObject 工具声明的最小可断言形状（只取门禁关心的关键字）。
type schemaObject struct {
	Type                 string                    `json:"type"`
	Properties           map[string]schemaProperty `json:"properties"`
	Required             []string                  `json:"required"`
	AdditionalProperties *bool                     `json:"additionalProperties"`
}

// schemaProperty 单参数的声明；Properties 与 Items 只用于嵌套对象/数组。
type schemaProperty struct {
	Type        string                    `json:"type"`
	Description string                    `json:"description"`
	Enum        []string                  `json:"enum"`
	Minimum     *float64                  `json:"minimum"`
	Maximum     *float64                  `json:"maximum"`
	MinItems    *float64                  `json:"minItems"`
	MaxItems    *float64                  `json:"maxItems"`
	Properties  map[string]schemaProperty `json:"properties"`
	Required    []string                  `json:"required"`
	Items       *schemaProperty           `json:"items"`
}

// TestEverySpecSchemaIsWellFormed 手写 JSON 的唯一护栏：每个工具的 Schema 必须
// 能反序列化成对象、有类型、参数都有描述、required 只列已声明的参数。
//
// 手写 JSON 打错一个逗号，过去要到真正发请求时才炸（那时模型已经看不到工具了），
// 所以这里把「能解析」也当成断言，而不是让 json.RawMessage 一路裸奔到适配器。
func TestEverySpecSchemaIsWellFormed(t *testing.T) {
	for _, tl := range New(Deps{}) {
		s := tl.Spec()
		var root schemaObject
		if err := json.Unmarshal(s.Schema, &root); err != nil {
			t.Fatalf("%s: schema is not valid JSON: %v\n%s", s.Name, err, s.Schema)
		}
		if root.Type != "object" {
			t.Fatalf("%s: schema root type = %q, want object", s.Name, root.Type)
		}
		if root.Properties == nil {
			t.Fatalf("%s: schema has no properties object (a no-arg tool must use tool.NoArgsSchema)", s.Name)
		}
		declared := map[string]bool{}
		for name, p := range root.Properties {
			declared[name] = true
			if p.Type == "" {
				t.Fatalf("%s.%s: parameter has no type", s.Name, name)
			}
			if strings.TrimSpace(p.Description) == "" {
				t.Fatalf("%s.%s: parameter has no description", s.Name, name)
			}
			if p.Enum != nil && len(p.Enum) == 0 {
				t.Fatalf("%s.%s: enum is empty", s.Name, name)
			}
		}
		for _, r := range root.Required {
			if !declared[r] {
				t.Fatalf("%s: required lists %q, which is not a declared parameter", s.Name, r)
			}
		}
	}
}

// TestNoArgsToolsShareOneShape 无参工具必须与适配器/探针的兜底同形（单一事实源）：
// 各自手写会让「没有参数」在不同端点上长成不同样子。
func TestNoArgsToolsShareOneShape(t *testing.T) {
	seen := 0
	for _, tl := range New(Deps{}) {
		s := tl.Spec()
		var root schemaObject
		if err := json.Unmarshal(s.Schema, &root); err != nil {
			t.Fatalf("%s: invalid schema: %v", s.Name, err)
		}
		if len(root.Properties) != 0 {
			continue
		}
		seen++
		if !reflect.DeepEqual([]byte(s.Schema), []byte(tool.NoArgsSchema)) {
			t.Fatalf("%s: no-arg schema = %s, want tool.NoArgsSchema = %s", s.Name, s.Schema, tool.NoArgsSchema)
		}
	}
	if seen == 0 {
		t.Fatal("没有找到无参工具：本测试失去意义（工具表变了？）")
	}
}

// TestSpecBoundsMatchConstants 模型读到的边界必须落在 schema 关键字上，且与代码里的
// 常量同源（D171）：只写在描述里的范围，严格按 schema 产参数的客户端根本看不见。
//
// 判据是**取值相等**，不是子串命中——过去描述写 1-3600、schema 一个边界都没有，
// 子串断言照样绿。
func TestSpecBoundsMatchConstants(t *testing.T) {
	specs := map[string]tool.Spec{}
	for _, tl := range New(Deps{}) {
		s := tl.Spec()
		specs[s.Name] = s
	}

	cases := []struct {
		tool  string
		param string
		min   float64 // <0 = 该参数不声明下界
		max   float64 // <0 = 该参数不声明上界
	}{
		{"sleep", "seconds", 1, port.MaxCallTimeoutSec},
		{"file_read", "offset", 1, -1},
		{"file_read", "limit", 1, maxReadLines},
		{"file_list", "offset", 0, -1},
		{"file_list", "limit", 1, maxListEntries},
		{"job_start", "timeout_sec", 0, maxJobTimeoutSec},
		{"job_wait", "timeout_sec", 0, maxJobTimeoutSec},
		{"job_logs", "tail", 1, -1},
		{"watch_start", "interval_sec", watchMinIntervalSec, watchMaxIntervalSec},
		{"watch_start", "max_checks", 1, watchMaxChecksBound},
		{"watch_start", "deadline_sec", 1, watchMaxDeadlineSec},
		{"ask_user", "questions", -1, maxAskQuestions},
	}
	for _, tc := range cases {
		s, ok := specs[tc.tool]
		if !ok {
			t.Fatalf("%s not registered", tc.tool)
		}
		var root schemaObject
		if err := json.Unmarshal(s.Schema, &root); err != nil {
			t.Fatalf("%s: invalid schema: %v", tc.tool, err)
		}
		p, ok := root.Properties[tc.param]
		if !ok {
			t.Fatalf("%s has no parameter %q", tc.tool, tc.param)
		}
		if tc.min >= 0 && (p.Minimum == nil || *p.Minimum != tc.min) {
			t.Fatalf("%s.%s minimum = %v, want %v", tc.tool, tc.param, p.Minimum, tc.min)
		}
		if tc.max >= 0 {
			// 数组型参数（ask_user.questions）的上限写在 maxItems 上。
			got := p.Maximum
			if p.MaxItems != nil {
				got = p.MaxItems
			}
			if got == nil || *got != tc.max {
				t.Fatalf("%s.%s upper bound = %v, want %v", tc.tool, tc.param, got, tc.max)
			}
		}
	}
}

// TestSleepKeepsTimeoutReserved sleep 的 seconds 声明自己的范围，但**不得**把
// timeout_sec 写进 schema：那是调用级保留参数（D38），一旦被工具「自声明」，
// runner 就不再按次覆盖超时，300s 的 sleep 会被缺省超时掐掉。
func TestSleepKeepsTimeoutReserved(t *testing.T) {
	var root schemaObject
	if err := json.Unmarshal(prettySpec(t, "sleep").Schema, &root); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
	if _, ok := root.Properties["timeout_sec"]; ok {
		t.Fatal("sleep schema declares timeout_sec; the per-call reserved parameter would stop applying")
	}
}

// prettySpec 取指定工具的 Spec（找不到即失败）。
func prettySpec(t *testing.T, name string) tool.Spec {
	t.Helper()
	for _, tl := range New(Deps{}) {
		if s := tl.Spec(); s.Name == name {
			return s
		}
	}
	t.Fatalf("%s not registered", name)
	return tool.Spec{}
}
