package main

// logreader.go 运行日志读侧的装配实现（D305 / contracts §7.13）。
//
// 适配器不许 import `internal/logging`（adapter → port 方向，D276 同一条规则），所以设置窗
// 的「日志」页拿到的是 port.LogReader：名字与末尾文本，目录布局留在这一层。

import (
	"path/filepath"

	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// logReader 把 logging 的读侧包成 port.LogReader。
type logReader struct{ dir string }

// Dir 日志目录（界面「打开目录」用）。
func (r logReader) Dir() string { return r.dir }

// List 列日志文件（只认写侧那条命名规则，见 logging.List）。
func (r logReader) List() ([]port.LogFile, error) {
	files, err := logging.List(r.dir)
	if err != nil {
		return nil, err
	}
	out := make([]port.LogFile, len(files))
	for i, f := range files {
		out[i] = port.LogFile{Name: f.Name, Size: f.Size, Mod: f.Mod}
	}
	return out, nil
}

// Tail 读一份的末尾。name 由 List 给出，这里再压一次「只能是目录里的直接子项」——界面
// 不该有能力把 `../config.json` 当成日志打开。
func (r logReader) Tail(name string, lines int) (string, error) {
	return logging.Tail(filepath.Join(r.dir, filepath.Base(name)), lines)
}
