# 繁简转换词典

来自 [OpenCC](https://github.com/BYVoid/OpenCC)（Apache-2.0），内容未改，仅做了
「LF 行尾、无 BOM、结尾补一个换行」的规范化。

| 文件 | 用途 |
|---|---|
| `TSPhrases.txt` | 繁 → 简的词组表（多字词优先于单字） |
| `TSCharacters.txt` | 繁 → 简的单字表 |
| `TWVariantsRevPhrases.txt` | 台湾用词 → 通用繁体的词组表（第一趟） |
| `TWVariantsRev.txt` | 台湾异体 → 通用繁体的单字表（第一趟） |
| `TWPhrasesRev.txt` | 台湾用词 → 大陆用词的词组表（第二趟） |

`tw2sp` 档的转换链（与 OpenCC 的 `config/tw2sp.json` 一致）：

1. `TWVariantsRevPhrases` + `TWVariantsRev`
2. `TWPhrasesRev`
3. `TSPhrases` + `TSCharacters`

`t2s` 档只有第 3 趟。

格式：每行「键 `<TAB>` 值」，值可给多个候选（空格分隔，取第一个）。以 `#` 开头的行为注释。
空行跳过。

**为什么不用现成的 Go 移植**（`github.com/liuzl/gocc`）：它依赖 `github.com/liuzl/da`
→ `github.com/liuzl/cedar-go`，后者是 GPL-2.0，静态链进单二进制会把整份交付物拖进 GPL。
词典是 Apache-2.0，算法（最长前缀匹配）自己写二十行就够。

**更新词典**：从 OpenCC 仓库 `data/dictionary/` 取同名文件覆盖，跑
`go test ./internal/adapter/zhconv/` 确认词条数没掉、既有断言仍通过。

许可全文见仓库根目录 `THIRD-PARTY-NOTICES.md` 的 Apache-2.0 一节。
