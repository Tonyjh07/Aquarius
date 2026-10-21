package platform

// 拖拽路径解析（D187 口径三）：WM_DROPFILES 的 HDROP 句柄只能经 DragQueryFileW 逐项
// 取，路径本身是 UTF-16（含终止 NUL），可能带空格、中文、代理对。把「按项取路径 +
// 去 NUL + 跳过空项」抽成平台无关的纯函数，Win32 调用留在 shell_windows.go——
// 这样路径解析可在任意平台单测。

// collectDropPaths 把 n 个项取成路径列表。raw 返回第 i 项的 UTF-16 码元（含终止 NUL）；
// 取不到或全为空时返回 nil。
//
// n 上限由调用方夹取：真实拖放一次极少超过几十项，过大的 n 只会是异常数据。
func collectDropPaths(n int, raw func(i int) []uint16) []string {
	if n <= 0 || raw == nil {
		return nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s := trimUTF16NUL(raw(i))
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// trimUTF16NUL 去掉尾部 NUL 并转成字符串（保留中间的空格——路径里的空格是内容）。
func trimUTF16NUL(units []uint16) string {
	end := len(units)
	for end > 0 && units[end-1] == 0 {
		end--
	}
	if end == 0 {
		return ""
	}
	return string(runesFromUTF16(units[:end]))
}
