package app

import (
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// wrapSession 给错误加模块包装：`session: <短语>: <原因>`。
//
// 短语走文案表、整句不走：%w 必须原样喂给 fmt，把动词写进表就等于让每份译文各自维护
// 格式串，一处写错就是错误链断掉。短语本身进表是因为这一行会显示给用户——命令失败时
// 界面上的那句话就是它，不是只进调试日志的碎碎念。
func wrapSession(key string, err error) error {
	return fmt.Errorf("session: %s: %w", i18n.T(key), err)
}
