package main

// 空 provider 状态下的占位 LLM（D278 决策 3）。
//
// 「一条 provider 都没有」从配置错误降成了可启动空态：程序起得来、设置窗能补、语言与协议
// 都能先设好，只有「真发一次请求」这件事做不到。做法不是让调用方到处判空，而是照常装配一条
// LLM 链——只不过链上这一环只会回一条说人话的错。这样 `/model`、发送路径、装配根的装饰器
// 顺序都不用为「没有模型」另开分支，错误也只在一个地方写。

import (
	"context"
	"errors"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// errNoProvider 空态的报因。每次现取文案：语言在启动之后才定（向导第 1 页），
// 包级变量会在 `i18n.Use` 之前就把词冻住。
func errNoProvider() error { return errors.New(i18n.T("cmd.asm.err.no_provider")) }

// unconfiguredLLM 没有 provider 时的占位实现：两条方法都只回 errNoProvider。
type unconfiguredLLM struct{}

var _ port.LLM = unconfiguredLLM{}

func (unconfiguredLLM) Generate(context.Context, port.GenerateRequest) (port.Stream, error) {
	return nil, errNoProvider()
}

func (unconfiguredLLM) Models(context.Context) ([]port.ModelInfo, error) {
	return nil, errNoProvider()
}
