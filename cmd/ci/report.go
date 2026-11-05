package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// evidence 一次门禁的证据。留痕要解决的只有一个问题：让「我跑过了」这句话
// 对得上具体提交与具体工作区状态，而不是靠记忆。所以记的是提交号、是否脏、
// 哪个档位、什么时候、每条 job 的结论。
type evidence struct {
	SHA        string      `json:"sha"`
	Dirty      bool        `json:"dirty"`
	Profile    string      `json:"profile"`
	StartedAt  time.Time   `json:"started_at"`
	DurationMS int64       `json:"duration_ms"`
	GoVersion  string      `json:"go_version"`
	Platform   string      `json:"platform"`
	Passed     int         `json:"passed"`
	Failed     int         `json:"failed"`
	Skipped    int         `json:"skipped"`
	Jobs       []jobResult `json:"jobs"`
}

// recordFile 是 .ci/last.json 的形状：每个档位留最近一次，互不覆盖。
// 只留最近一次是刻意的——留历史会诱使人拿三天前的结果当今天的。
type recordFile struct {
	Profiles map[string]evidence `json:"profiles"`
}

// recordPath 证据文件。放在 .ci/ 而不是 .git/：同目录还要放 JSON 报告与日后的
// 产物，一眼能看见，也方便加进 .gitignore。
func recordPath(root string) string {
	return filepath.Join(root, ".ci", "last.json")
}

func readRecord(root string) (map[string]evidence, error) {
	b, err := os.ReadFile(recordPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]evidence{}, nil
		}
		return nil, err
	}
	var f recordFile
	if err := json.Unmarshal(b, &f); err != nil {
		// 文件坏了不该拦住门禁：当作没有历史。
		return map[string]evidence{}, nil
	}
	if f.Profiles == nil {
		f.Profiles = map[string]evidence{}
	}
	return f.Profiles, nil
}

func writeRecord(root string, ev evidence) error {
	profiles, _ := readRecord(root)
	profiles[ev.Profile] = ev
	b, err := json.MarshalIndent(recordFile{Profiles: profiles}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(recordPath(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// 先写临时文件再改名：并行跑两档时不会读到写了一半的内容。
	tmp := recordPath(root) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, recordPath(root))
}

func writeJSON(path string, ev evidence) error {
	b, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, b, 0o644)
}

// gitHead 取当前提交与工作区是否脏。取不到提交号不算错误中断——门禁照跑，
// 只是不写证据（在还没首次提交的仓库里跑也是合法用法）。
func gitHead(root string) (string, bool, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false, err
	}
	sha := strings.TrimSpace(string(out))

	st, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		return sha, false, err
	}
	return sha, strings.TrimSpace(string(st)) != "", nil
}

// repoRoot 从当前目录往上找仓库根。门禁在仓库根跑，路径才与文档里的命令对得上。
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("不在 git 仓库内")
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}
