package main

// 密钥库管理面装配根侧（S9-6b/D161）：实现 uigui.SecretsAdmin（消费方定义接口）——
// 设置窗「密钥库」页的列出 / 新增覆盖 / 删除。动作由设置窗**同步**调用（本地小文件
// IO，微秒级；工具页那套旁路 goroutine + 结果通道是给 15s 拨号准备的可不必套用），
// 本文件方法只做「改库文件 → 递增 Revision」。库本体 secretsBin 由 bootstrap 填
//（profileDir 一就位就有，设置窗打开时必已就绪）。

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// secretsAdmin uigui.SecretsAdmin 的装配根实现。
//
// 快照修订号存在装配根全局 wiring 上（wiring.secretsRev，同 toolsRev 口径）：写/删后
// 递增，设置窗据此重建条目行；以后若出现别的写入路径，也一起递增同一计数器。
type secretsAdmin struct {
	w *wiring
}

var _ uigui.SecretsAdmin = (*secretsAdmin)(nil)

// Snapshot 原子快照：库条目（按名字排序，只给名字 + 尾 4 位掩码）+ 库文件路径。
// 库解不开（换机拷来 / 未接线）时不报错——返回空条目 + 路径，页面据此显示空态；
// 真正的读错误只影响取密钥链（那里警告 + 环境兜底，D161 落点②）。
func (a *secretsAdmin) Snapshot() uigui.SecretsSnapshot {
	s := uigui.SecretsSnapshot{Revision: a.w.secretsRev.Load()}
	if a.w.secretsBin == nil {
		return s
	}
	s.Path = a.w.secretsBin.Path()
	entries, err := a.w.secretsBin.Entries(context.Background())
	if err != nil {
		return s // 解不开：空条目（不回显错误，免得与「空库」混淆）
	}
	for _, e := range entries {
		s.Entries = append(s.Entries, uigui.SecretEntry{Name: e.Name, Mask: e.Mask})
	}
	return s
}

// Put 写入/覆盖一条密钥；值永不回显，同名即覆盖。写成功后递增 Revision。
func (a *secretsAdmin) Put(name, value string) error {
	if a.w.secretsBin == nil {
		return errors.New("本机密钥库未接线，无法写入")
	}
	if err := a.w.secretsBin.Set(context.Background(), name, value); err != nil {
		return err
	}
	a.w.secretsRev.Add(1)
	return nil
}

// Delete 删除一条；名字不存在报错（不静默成功——UI 据此报「已不在」）。
func (a *secretsAdmin) Delete(name string) error {
	if a.w.secretsBin == nil {
		return errors.New("本机密钥库未接线，无法删除")
	}
	err := a.w.secretsBin.Delete(context.Background(), name)
	if errors.Is(err, secretbin.ErrNotFound) {
		return fmt.Errorf("密钥库中已没有 %q（可能已被删除）", name)
	}
	if err != nil {
		return err
	}
	a.w.secretsRev.Add(1)
	return nil
}
