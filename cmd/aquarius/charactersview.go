package main

// charactersview.go 设置窗「角色」页的只读数据面（§5.17）。
//
// 页面动作（导入 / 删除 / 开始对话）走 `/character` 命令通道，这一份只提供**清单快照**：
// 读角色卡目录（characterfs.Store.List）并做一次短 TTL 缓存。设置窗在「角色」页逐帧问，
// 若每帧都读盘就是热路径；缓存让「命令在那头落地」后最多 TTL 之内反映出来，同时避免
// 逐帧读盘。线程安全（设置窗 goroutine 调用；store 在开窗前已就绪）。

import (
	"context"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/characterfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// charactersViewTTL 快照缓存寿命：命令落地到设置窗刷新之间的最大延迟。
const charactersViewTTL = 400 * time.Millisecond

// charactersView 角色卡清单快照源。store 是取值函数而不是指针——角色卡目录晚于 UI 构造
// 就绪（装配段先起 UI、再装工具与目录），故在**调用时**现取。
type charactersView struct {
	store func() *characterfs.Store

	mu       sync.Mutex
	cached   uigui.CharactersSnapshot
	cachedAt time.Time
}

func newCharactersView(store func() *characterfs.Store) *charactersView {
	return &charactersView{store: store}
}

// snapshot 取清单快照（TTL 内复用上一份）。读目录失败如实带出原因（用户面）。
func (v *charactersView) snapshot() uigui.CharactersSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.cachedAt) < charactersViewTTL {
		return v.cached
	}
	v.cachedAt = time.Now()
	store := v.store()
	if store == nil {
		v.cached = uigui.CharactersSnapshot{Err: "角色卡目录未就绪"}
		return v.cached
	}
	items, err := store.List(context.Background())
	if err != nil {
		v.cached = uigui.CharactersSnapshot{Err: err.Error()}
		return v.cached
	}
	ss := make([]uigui.CharacterState, 0, len(items))
	for _, it := range items {
		ss = append(ss, characterState(it))
	}
	v.cached = uigui.CharactersSnapshot{Items: ss}
	return v.cached
}

// characterState 把一张卡的事实映射成页面状态（只展示字段原样带出；导入时间格式化为短日期）。
func characterState(it port.CharacterInfo) uigui.CharacterState {
	st := uigui.CharacterState{
		Slug:        it.Slug,
		Display:     it.Display,
		Source:      it.Source,
		Spec:        it.Spec,
		BookEntries: it.BookEntries,
		Greetings:   it.Greetings,
		Assets:      it.Assets,
		HasAvatar:   it.HasAvatar,
		AvatarPath:  it.AvatarPath,
		Warnings:    it.Warnings,
		Unsupported: it.Unsupported,
	}
	if !it.ImportedAt.IsZero() {
		st.ImportedAt = it.ImportedAt.Format("2006-01-02")
	}
	return st
}
