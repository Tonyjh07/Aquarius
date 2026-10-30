package app

import (
	"context"
	"errors"
)

// execCharacter 实现 /character（§5.17）：角色卡的列出、查看、导入与开新会话。
//
// 端口类型先落地，解析与命令行为分批跟上；在此之前如实报「未接入」——
// 这比让命令不存在更好排查：用户看到的是「本版还没有」，而不是「未知命令」。
func (s *Session) execCharacter(_ context.Context, _ []string) (string, error) {
	return "", errors.New("本构建未接入角色卡目录（/character 暂不可用）")
}
