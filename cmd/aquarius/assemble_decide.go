package main

// 决策模型的装配（D290）：建客户端、套到模型链的确认器上、把判定送进审计。
//
// 顺序上有个约束：审计句柄原本在 stackDecorators 里才建，而那一步要等 ToolRunner
// 就位之后（它包的是 runner）。决策模型的判定要落 `kind=decide` 行，所以审计提前到
// 确认链之前打开——见 openAudit。

import (
	"context"
	"path/filepath"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decide"
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// warmupTimeout 预热这一趟的总时限。本机冷启动实测 61 s（模型被换出后再加载），
// 所以给得比判定超时宽得多——预热不是判定，它慢一点只耽误一次后台加载。
const warmupTimeout = 3 * time.Minute

// openAudit 打开审计文件（幂等）。必须在确认链装配之前调用：决策模型的判定要落在
// 同一条 audit.log 里，晚于它也写得进去，但「先有日志再有判定」这条顺序更好查。
func (w *wiring) openAudit() error {
	if w.audit != nil {
		return nil
	}
	audit, err := decorate.NewAudit(filepath.Join(w.profileDir, "audit.log"), 0)
	if err != nil {
		return err
	}
	w.audit = audit
	return nil
}

// buildDecider 按 decide 段建客户端。不挡启动：没配全、密钥解析不出、地址写错，
// 都只让这一档退化成 off 并留一行日志——少一个可选能力不该让人进不来。
func (w *wiring) buildDecider() {
	o := w.decide
	if o.Policy.Mode == decorate.DecideOff {
		return
	}
	if o.BaseURL == "" || o.Model == "" {
		w.note(logging.LevelWarn, logging.Perm, "log.perm.decide_unconfigured",
			"log.act.decide_off", "mode", string(o.Policy.Mode))
		return
	}
	var key string
	if o.KeyRef != "" {
		k, err := resolveAPIKey(context.Background(), "decide", o.KeyRef, w.secrets, w.warnPlaintext)
		if err != nil {
			w.note(logging.LevelWarn, logging.Perm, "log.perm.decide_unusable",
				"log.act.decide_off", "mode", string(o.Policy.Mode), "err", err)
			return
		}
		key = k
	}
	client, err := decide.New(decide.Options{
		BaseURL: o.BaseURL, Model: o.Model, Key: key, TaskChars: o.TaskChars,
	})
	if err != nil {
		w.note(logging.LevelWarn, logging.Perm, "log.perm.decide_unusable",
			"log.act.decide_off", "mode", string(o.Policy.Mode), "err", err)
		return
	}
	w.decider = client
	if o.Warmup {
		// 异步：主线程不等模型加载（本机实测 6.6 s，换出后再加载 61 s）。
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), warmupTimeout)
			defer cancel()
			client.Warmup(ctx)
		}()
	}
	w.note(logging.LevelInfo, logging.Perm, "log.perm.decide_ready",
		"log.act.decide_on", "mode", string(o.Policy.Mode), "model", o.Model)
}

// wrapDecide 把决策模型罩到确认器上。没建出客户端时原样返回——`shadow` 与 `off` 的
// 行为必须逐条一致，连「多一层壳」都不该有。
func (w *wiring) wrapDecide(inner port.Confirmer) port.Confirmer {
	if w.decider == nil {
		return inner
	}
	return decorate.Decide(inner, w.decider, w.decide.Policy, auditSink{w: w})
}

// auditSink 把判定写进审计。审计句柄在装配过程中才就位，所以每次现取而不是握着一个
// 可能还是 nil 的指针。
type auditSink struct{ w *wiring }

func (s auditSink) Decide(rec decorate.DecideRecord) {
	if s.w.audit != nil {
		s.w.audit.Decide(rec)
	}
}
