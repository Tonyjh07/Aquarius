# 第三方组件声明（Third-Party Notices）

本文件列出 Aquarius 单二进制交付物（`go build ./cmd/aquarius`）所链接的全部第三方开源组件、版本、许可证与版权声明，供再分发时履行各许可证的署名与告知义务。Aquarius 自身以 MPL-2.0 发布，见根目录 `LICENSE`。

## 一、范围与维护

- **范围**：`go.mod` 中全部 `require`（直接 + 间接），与 `go list -deps ./cmd/aquarius` 的模块集合一致，共 64 个模块。
  - `spike/` 下各目录是独立 `go.mod` 的试验工程，不属于交付物，不在本清单内。
  - `assets/` 下图标为本项目自制素材，不含第三方组件。
  - 随源码内嵌、但不是 Go 模块的数据（繁简转换词典）单列在 §二末尾。
- **依赖升级后请重新核对并同步更新本文件**：

  ```bash
  go list -deps -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' ./cmd/aquarius | sort -u
  ```

  对每个模块检查其模块缓存（或上游仓库）中的 `LICENSE`/`COPYING` 文件。
- **文本来源**：下列全文逐字取自各组件对应版本的许可证文件；如本文件与上游存在任何差异，以上游为准。

## 二、组件清单

| 组件 | 版本 | 许可证 | 版权声明（逐字摘自其 LICENSE） |
| --- | --- | --- | --- |
| `codeberg.org/readeck/go-readability/v2` | `v2.1.3` | MIT | `Copyright (c) 2019 Radhi Fadlillah` |
| `gioui.org` | `v0.10.2` | Unlicense OR MIT | `Copyright (c) 2019 The Gio authors` |
| `gioui.org/shader` | `v1.0.9` | Unlicense OR MIT | `Copyright (c) 2019 The Gio authors` |
| `github.com/JohannesKaufmann/dom` | `v0.3.1` | MIT | `Copyright (c) 2024 Johannes Kaufmann` |
| `github.com/JohannesKaufmann/html-to-markdown/v2` | `v2.5.2` | MIT | `Copyright (c) 2018 Johannes Kaufmann` |
| `github.com/alecthomas/chroma/v2` | `v2.20.0` | MIT | `Copyright (C) 2017 Alec Thomas` |
| `github.com/andybalholm/cascadia` | `v1.3.4` | BSD-3-Clause | `Copyright (c) 2011 Andy Balholm. All rights reserved.` |
| `github.com/aymanbagabas/go-osc52/v2` | `v2.0.1` | MIT | `Copyright (c) 2022 Ayman Bagabas` |
| `github.com/aymerick/douceur` | `v0.2.0` | MIT | `Copyright (c) 2015 Aymerick JEHANNE` |
| `github.com/charmbracelet/bubbletea` | `v1.3.10` | MIT | `Copyright (c) 2020-2025 Charmbracelet, Inc` |
| `github.com/charmbracelet/colorprofile` | `v0.2.3-0.20250311203215-f60798e515dc` | MIT | `Copyright (c) 2020-2024 Charmbracelet, Inc` |
| `github.com/charmbracelet/glamour` | `v1.0.0` | MIT | `Copyright (c) 2019-2023 Charmbracelet, Inc` |
| `github.com/charmbracelet/lipgloss` | `v1.1.1-0.20250404203927-76690c660834` | MIT | `Copyright (c) 2021-2023 Charmbracelet, Inc` |
| `github.com/charmbracelet/x/ansi` | `v0.10.2` | MIT | `Copyright (c) 2023 Charmbracelet, Inc.` |
| `github.com/charmbracelet/x/cellbuf` | `v0.0.13` | MIT | `Copyright (c) 2023 Charmbracelet, Inc.` |
| `github.com/charmbracelet/x/exp/slice` | `v0.0.0-20250327172914-2fdc97757edf` | MIT | `Copyright (c) 2023 Charmbracelet, Inc.` |
| `github.com/charmbracelet/x/term` | `v0.2.1` | MIT | `Copyright (c) 2023 Charmbracelet, Inc.` |
| `github.com/dlclark/regexp2` | `v1.12.0` | MIT | `Copyright (c) Doug Clark` |
| `github.com/erikgeiser/coninput` | `v0.0.0-20211004153227-1c3628e74d0f` | MIT | `Copyright (c) 2021 Erik G.` |
| `github.com/go-shiori/dom` | `v0.0.0-20230515143342-73569d674e1c` | MIT | `Copyright (c) 2019-present Radhi Fadlillah` |
| `github.com/go-text/typesetting` | `v0.3.4` | Unlicense OR BSD-3-Clause | `Copyright 2021 The go-text authors` |
| `github.com/gogs/chardet` | `v0.0.0-20211120154057-b7413eaefb8f` | MIT | `Copyright (c) 2012 chardet Authors` |
| `github.com/gomutex/godocx` | `v0.1.5` | MIT | `Copyright (c) 2024 gomutex` |
| `github.com/google/jsonschema-go` | `v0.4.3` | MIT | `Copyright (c) 2025 JSON Schema Go Project Authors` |
| `github.com/gorilla/css` | `v1.0.1` | BSD-3-Clause | `Copyright (c) 2023 The Gorilla Authors. All rights reserved.` |
| `github.com/itlightning/dateparse` | `v0.2.1` | MIT | `Copyright (c) 2015-2017 Aaron Raddon` |
| `github.com/ledongthuc/pdf` | `v0.0.0-20260907135840-6c8c28e0e8a0` | BSD-3-Clause | `Copyright (c) 2009 The Go Authors. All rights reserved.` |
| `github.com/lucasb-eyer/go-colorful` | `v1.4.0` | MIT | `Copyright (c) 2013 Lucas Beyer` |
| `github.com/mattn/go-isatty` | `v0.0.22` | MIT | `Copyright (c) Yasuhiro MATSUMOTO <mattn.jp@gmail.com>` |
| `github.com/mattn/go-localereader` | `v0.0.1` | MIT（README 声明，模块内无 LICENSE 文件） | README「License: MIT」，作者 Yasuhiro Matsumoto |
| `github.com/mattn/go-runewidth` | `v0.0.17` | MIT | `Copyright (c) 2016 Yasuhiro Matsumoto` |
| `github.com/microcosm-cc/bluemonday` | `v1.0.27` | BSD-3-Clause | `Copyright (c) 2014, David Kitchen <david@buro9.com>` |
| `github.com/modelcontextprotocol/go-sdk` | `v1.8.0` | Apache-2.0 / MIT（过渡期，见 §3.6） | `Copyright (c) 2024-2025 Model Context Protocol a Series of LF Projects, LLC.` |
| `github.com/mpyw/whisper.go` | `v0.1.1` | MIT | `Copyright (c) 2026 mpyw` |
| `github.com/mpyw/whisper.go-wasm` | `v0.1.1` | MIT（另含内嵌运行时，见 §3.8） | `Copyright (c) 2026 mpyw` |
| `github.com/muesli/ansi` | `v0.0.0-20230316100256-276c6243b2f6` | MIT | `Copyright (c) 2021 Christian Muehlhaeuser` |
| `github.com/muesli/cancelreader` | `v0.2.2` | MIT | `Copyright (c) 2022 Erik Geiser and Christian Muehlhaeuser` |
| `github.com/muesli/reflow` | `v0.3.0` | MIT | `Copyright (c) 2019 Christian Muehlhaeuser` |
| `github.com/muesli/termenv` | `v0.16.0` | MIT | `Copyright (c) 2019 Christian Muehlhaeuser` |
| `github.com/richardlehane/mscfb` | `v1.0.8` | Apache-2.0（见 §3.5） | `Copyright 2013 Richard Lehane. All rights reserved.`（源码头，`LICENSE.txt` 内无版权行） |
| `github.com/richardlehane/msoleps` | `v1.0.6` | Apache-2.0（见 §3.5） | `Copyright 2014 Richard Lehane. All rights reserved.`（源码头，`LICENSE.txt` 内无版权行） |
| `github.com/rivo/uniseg` | `v0.4.7` | MIT | `Copyright (c) 2019 Oliver Kuederle` |
| `github.com/segmentio/asm` | `v1.1.3` | MIT | `Copyright (c) 2021 Segment` |
| `github.com/segmentio/encoding` | `v0.5.4` | MIT | `Copyright (c) 2019 Segment.io, Inc.` |
| `github.com/temoto/robotstxt` | `v1.1.2` | MIT | `Copyright (c) 2010 Sergey Shepelev <temotor@gmail.com>` |
| `github.com/tiendc/go-deepcopy` | `v1.7.2` | MIT | `Copyright (c) 2023 tiendc` |
| `github.com/xo/terminfo` | `v0.0.0-20220910002029-abceb7e1c41e` | MIT | `Copyright (c) 2016 Anmol Sethi` |
| `github.com/xuri/efp` | `v0.0.2` | BSD-3-Clause | `Copyright (c) 2017 - 2026 Ri Xu All rights reserved.` |
| `github.com/xuri/excelize/v2` | `v2.11.1-0.20260930021559-01a9ff32fb3c` | BSD-3-Clause | `Copyright (c) 2016-2026 The excelize Authors.` / `Copyright (c) 2011-2017 Geoffrey J. Teale` |
| `github.com/xuri/nfp` | `v0.0.2-0.20250530014748-2ddeb826f9a9` | BSD-3-Clause | `Copyright (c) 2022-2025 Ri Xu All rights reserved.` |
| `github.com/yosida95/uritemplate/v3` | `v3.0.2` | BSD-3-Clause | `Copyright (C) 2016, Kohei YOSHIDA <https://yosida95.com/>. All rights reserved.` |
| `github.com/yuin/goldmark` | `v1.8.2` | MIT | `Copyright (c) 2019 Yusuke Inuzuka` |
| `github.com/yuin/goldmark-emoji` | `v1.0.6` | MIT | `Copyright (c) 2020 Yusuke Inuzuka` |
| `golang.org/x/crypto` | `v0.57.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/exp/shiny` | `v0.0.0-20250408133849-7e4ce0ab07d0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/image` | `v0.45.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/net` | `v0.60.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/oauth2` | `v0.35.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/sync` | `v0.23.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/sys` | `v0.49.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/term` | `v0.46.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/text` | `v0.42.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/time` | `v0.15.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `gopkg.in/yaml.v3` | `v3.0.1` | MIT / Apache-2.0（混合，见 §3.7） | `Copyright (c) 2006-2010 Kirill Simonov` / `Copyright (c) 2011-2019 Canonical Ltd` |

许可证分布：MIT 39 个、BSD-3-Clause 18 个、Unlicense OR MIT 2 个、Unlicense OR BSD-3-Clause 1 个、Apache-2.0 2 个、Apache-2.0 / MIT 过渡 1 个、MIT / Apache-2.0 混合 1 个，合计 64 个。

### 附：随源码内嵌、但不是 Go 模块的第三方数据

| 数据 | 位置 | 许可证 | 版权声明 |
| --- | --- | --- | --- |
| OpenCC 繁简转换词典（5 个 `.txt`，约 50 KB） | `internal/adapter/zhconv/dict/` | Apache-2.0（见 §3.5） | 上游为 [BYVoid/OpenCC](https://github.com/BYVoid/OpenCC) |

## 三、许可证全文

### 3.1 MIT License（39 个组件）

适用组件：

`codeberg.org/readeck/go-readability/v2`、`github.com/JohannesKaufmann/dom`、`github.com/JohannesKaufmann/html-to-markdown/v2`、`github.com/alecthomas/chroma/v2`、`github.com/aymanbagabas/go-osc52/v2`、`github.com/aymerick/douceur`、`github.com/charmbracelet/bubbletea`、`github.com/charmbracelet/colorprofile`、`github.com/charmbracelet/glamour`、`github.com/charmbracelet/lipgloss`、`github.com/charmbracelet/x/ansi`、`github.com/charmbracelet/x/cellbuf`、`github.com/charmbracelet/x/exp/slice`、`github.com/charmbracelet/x/term`、`github.com/dlclark/regexp2`、`github.com/erikgeiser/coninput`、`github.com/go-shiori/dom`、`github.com/gogs/chardet`、`github.com/gomutex/godocx`、`github.com/google/jsonschema-go`、`github.com/itlightning/dateparse`、`github.com/lucasb-eyer/go-colorful`、`github.com/mattn/go-isatty`、`github.com/mattn/go-localereader`（依其 README 声明）、`github.com/mattn/go-runewidth`、`github.com/mpyw/whisper.go`、`github.com/mpyw/whisper.go-wasm`（其内嵌运行时的许可另见 §3.8）、`github.com/muesli/ansi`、`github.com/muesli/cancelreader`、`github.com/muesli/reflow`、`github.com/muesli/termenv`、`github.com/rivo/uniseg`、`github.com/segmentio/asm`、`github.com/segmentio/encoding`、`github.com/temoto/robotstxt`、`github.com/tiendc/go-deepcopy`、`github.com/xo/terminfo`、`github.com/yuin/goldmark`、`github.com/yuin/goldmark-emoji`。

各组件的版权行见上表（逐字），与下列许可声明合并即构成其完整的 MIT 告知。许可声明正文取自 `github.com/charmbracelet/bubbletea@v1.3.10/LICENSE`（逐字，省去其版权行）；个别组件的行文排版略有差异，均属同一许可证，以上游原文为准：

```text
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### 3.2 BSD-3-Clause（18 个组件）

适用组件：

`github.com/andybalholm/cascadia`、`github.com/gorilla/css`、`github.com/ledongthuc/pdf`、`github.com/microcosm-cc/bluemonday`、`github.com/xuri/efp`、`github.com/xuri/excelize/v2`、`github.com/xuri/nfp`、`github.com/yosida95/uritemplate/v3`、`golang.org/x/crypto`、`golang.org/x/exp/shiny`、`golang.org/x/image`、`golang.org/x/net`、`golang.org/x/oauth2`、`golang.org/x/sync`、`golang.org/x/sys`、`golang.org/x/term`、`golang.org/x/text`、`golang.org/x/time`。

各组件的版权行见上表（逐字）。许可声明正文取自 `golang.org/x/sys@v0.49.0/LICENSE`（逐字，省去其版权行 `Copyright 2009 The Go Authors.`）；`golang.org/x/*` 十个模块文本完全一致，其余组件的第三条（名称背书条款）措辞以其上游原文为准：

```text
Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### 3.3 Unlicense OR MIT（2 个组件：`gioui.org`、`gioui.org/shader`）

可任选其一许可。两个模块的 `LICENSE` 文件内容一致，逐字如下（`gioui.org@v0.10.2/LICENSE`）：

```text
This project is provided under the terms of the UNLICENSE or
the MIT license denoted by the following SPDX identifier:

SPDX-License-Identifier: Unlicense OR MIT

You may use the project under the terms of either license.

Both licenses are reproduced below.

----
The MIT License (MIT)

Copyright (c) 2019 The Gio authors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
---



---
The UNLICENSE

This is free and unencumbered software released into the public domain.

Anyone is free to copy, modify, publish, use, compile, sell, or
distribute this software, either in source code form or as a compiled
binary, for any purpose, commercial or non-commercial, and by any
means.

In jurisdictions that recognize copyright laws, the author or authors
of this software dedicate any and all copyright interest in the
software to the public domain. We make this dedication for the benefit
of the public at large and to the detriment of our heirs and
successors. We intend this dedication to be an overt act of
relinquishment in perpetuity of all present and future rights to this
software under copyright law.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS BE LIABLE FOR ANY CLAIM, DAMAGES OR
OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.

For more information, please refer to <https://unlicense.org/>
---
```

### 3.4 Unlicense OR BSD-3-Clause（1 个组件：`github.com/go-text/typesetting`）

可任选其一许可。`github.com/go-text/typesetting@v0.3.4/LICENSE` 逐字如下：

```text
This project is provided under the terms of the UNLICENSE or
the BSD license denoted by the following SPDX identifier:

SPDX-License-Identifier: Unlicense OR BSD-3-Clause

You may use the project under the terms of either license.

Both licenses are reproduced below.

----
The BSD 3 Clause License

Copyright 2021 The go-text authors

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
---



---
The UNLICENSE

This is free and unencumbered software released into the public domain.

Anyone is free to copy, modify, publish, use, compile, sell, or
distribute this software, either in source code form or as a compiled
binary, for any purpose, commercial or non-commercial, and by any
means.

In jurisdictions that recognize copyright laws, the author or authors
of this software dedicate any and all copyright interest in the
software to the public domain. We make this dedication for the benefit
of the public at large and to the detriment of our heirs and
successors. We intend this dedication to be an overt act of
relinquishment in perpetuity of all present and future rights to this
software under copyright law.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS BE LIABLE FOR ANY CLAIM, DAMAGES OR
OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.

For more information, please refer to <https://unlicense.org/>
---
```

### 3.5 Apache-2.0（2 个组件 + 内嵌词典）

适用对象：

- `github.com/richardlehane/mscfb@v1.0.8`——`Copyright 2013 Richard Lehane. All rights reserved.`（版权行在源码头，其 `LICENSE.txt` 是未填写的 Apache-2.0 模板）
- `github.com/richardlehane/msoleps@v1.0.6`——`Copyright 2014 Richard Lehane. All rights reserved.`（同上）
- **OpenCC 繁简转换词典**（`internal/adapter/zhconv/dict/` 下 5 个 `.txt`，内容未改，仅规范化为 LF、无 BOM）——上游 [BYVoid/OpenCC](https://github.com/BYVoid/OpenCC) 以 Apache-2.0 发布，`Copyright (c) 2010-2024 BYVoid and contributors`。
- **`gopkg.in/yaml.v3` 的 Apache 部分**另见 §3.7；**`github.com/modelcontextprotocol/go-sdk`** 另见 §3.6。

许可正文与 §3.6 中的 Apache License 2.0 部分逐字相同（同一版本、同一文本），此处不重复。按 Apache-2.0 第 4 条，本文件已提供许可证副本、保留各组件版权与署名声明；本项目未修改上述组件的源文件（OpenCC 词典仅做行尾与编码规范化，未改内容）。

### 3.6 Apache-2.0 / MIT 过渡期（1 个组件：`github.com/modelcontextprotocol/go-sdk`）

该组件的 `LICENSE` 载有过渡声明：项目正从 MIT 迁移至 Apache License 2.0——已取得再许可同意的贡献与新贡献按 Apache-2.0；未明确授权的早期贡献仍按 MIT；文档（规范除外）按 CC-BY-4.0。`github.com/modelcontextprotocol/go-sdk@v1.8.0/LICENSE` 逐字如下：

```text
The MCP project is undergoing a licensing transition from the MIT License to the Apache License, Version 2.0 ("Apache-2.0"). All new code and specification contributions to the project are licensed under Apache-2.0. Documentation contributions (excluding specifications) are licensed under CC-BY-4.0.

Contributions for which relicensing consent has been obtained are licensed under Apache-2.0. Contributions made by authors who originally licensed their work under the MIT License and who have not yet granted explicit permission to relicense remain licensed under the MIT License.

No rights beyond those granted by the applicable original license are conveyed for such contributions.

---

                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS

---

MIT License

Copyright (c) 2024-2025 Model Context Protocol a Series of LF Projects, LLC.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

---

Creative Commons Attribution 4.0 International (CC-BY-4.0)

Documentation in this project (excluding specifications) is licensed under
CC-BY-4.0. See https://creativecommons.org/licenses/by/4.0/legalcode for
the full license text.
```

### 3.7 MIT / Apache-2.0 混合（1 个组件：`gopkg.in/yaml.v3`）

该组件按文件分别许可：从 libyaml 的 C 文件移植过来的若干文件沿用 MIT，其余文件按 Apache-2.0。`gopkg.in/yaml.v3@v3.0.1/LICENSE` 逐字如下：

```text
This project is covered by two different licenses: MIT and Apache.

#### MIT License ####

The following files were ported to Go from C files of libyaml, and thus
are still covered by their original MIT license, with the additional
copyright staring in 2011 when the project was ported over:

    apic.go emitterc.go parserc.go readerc.go scannerc.go
    writerc.go yamlh.go yamlprivateh.go

Copyright (c) 2006-2010 Kirill Simonov
Copyright (c) 2006-2011 Kirill Simonov

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

### Apache License ###

All the remaining project files are covered by the Apache license:

Copyright (c) 2011-2019 Canonical Ltd

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

Apache License 2.0 正文见 §3.6。

### 3.8 `github.com/mpyw/whisper.go-wasm` 内嵌运行时

该模块把 whisper.cpp 编译成 WebAssembly、再翻译成 Go 源码，从而让本项目**不依赖 cgo** 就能跑本地语音识别。它的 Go 代码按 MIT（`Copyright (c) 2026 mpyw`，正文见 §3.1）许可，同时把被编译进去的第三方运行时的许可原文放在模块内 `licenses/` 目录。下列各节逐字取自该目录。

#### 3.8.1 whisper.cpp / ggml（MIT）

```text
MIT License

Copyright (c) 2023-2026 The ggml authors
```

许可正文与 §3.1 逐字相同。

#### 3.8.2 YaRN RoPE（MIT）

`ggml/src/ggml-cpu/ops.cpp` 中的 YaRN RoPE 函数：

```text
MIT licensed. Copyright (c) 2023 Jeffrey Quesnelle and Bowen Peng.
```

许可正文与 §3.1 逐字相同。

#### 3.8.3 wasm2go（MIT）

```text
MIT License

Copyright (c) 2026 Nuno Cruces
```

许可正文与 §3.1 逐字相同。

#### 3.8.4 musl（MIT）

```text
musl as a whole is licensed under the following standard MIT license:

----------------------------------------------------------------------
Copyright © 2005-2020 Rich Felker, et al.

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
----------------------------------------------------------------------
```

该文件余下部分是贡献者名单与「部分代码源自第三方」的来源说明（TRE 正则、数学库、memcpy/memset、crypt、smoothsort 等，均为 MIT/BSD 系宽松许可），全文随模块发布（`licenses/musl.txt`），此处不重复。

#### 3.8.5 cloudlibc（BSD-2-Clause）

```text
Copyright (c) 2015-2017 Nuxi (https://nuxi.nl/) and contributors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions
are met:
1. Redistributions of source code must retain the above copyright
   notice, this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright
   notice, this list of conditions and the following disclaimer in the
   documentation and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE AUTHOR AND CONTRIBUTORS ``AS IS'' AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED.  IN NO EVENT SHALL THE AUTHOR OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS
OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT
LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY
OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF
SUCH DAMAGE.
```

#### 3.8.6 wasi-libc（Apache-2.0 WITH LLVM-exception / Apache-2.0 / MIT 三选）

```text
wasi-libc as a whole is multi-licensed under the
Apache License v2.0 with LLVM Exceptions, the Apache License v2.0, and
the MIT License. See the LICENSE-APACHE-LLVM, LICENSE-APACHE and LICENSE-MIT
files, respectively, for details.

Portions of this software are derived from third-party works covered by
their own licenses:

dlmalloc/                     - CC0; see the notice in malloc.c for details
emmalloc/                     - MIT; see the notice in emmalloc.c for details
libc-bottom-half/cloudlibc/   - BSD-2-Clause; see the LICENSE file for details
libc-top-half/musl/           - MIT; see the COPYRIGHT file for details
fts/musl-fts/                 - BSD-3-Clause; see the COPYING file for details

wasi-libc's changes to these files are multi-licensed under the
Apache License v2.0 with LLVM Exceptions, the Apache License v2.0,
the MIT License, and the original licenses of the third-party works.
```

MIT 分支正文见 §3.1（该文件不带独立版权行）；Apache-2.0 分支正文见 §3.6，其 LLVM 例外条款见 §3.8.8。

#### 3.8.7 dlmalloc（CC0）

```text
dlmalloc, wasi-libc's malloc, is CC0. See the notice in https://raw.githubusercontent.com/WebAssembly/wasi-libc/2e6fb9d8ee0cdf9e431fbcabe8af3115de000a13/dlmalloc/src/malloc.c
```

#### 3.8.8 LLVM（Apache-2.0 WITH LLVM-exception）

`licenses/llvm.txt` 的开头与例外条款、以及其附带的历史许可（libc++ 的双许可）：

```text
==============================================================================
The LLVM Project is under the Apache License v2.0 with LLVM Exceptions:
==============================================================================

                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/
```

Apache License 2.0 正文见 §3.6（同一版本、同一文本）。其后紧跟的例外条款逐字如下：

```text
---- LLVM Exceptions to the Apache 2.0 License ----

As an exception, if, as a result of your compiling your source code, portions
of this Software are embedded into an Object form of such source code, you
may redistribute such embedded portions in such Object form without complying
with the conditions of Sections 4(a), 4(b) and 4(d) of the License.

In addition, if you combine or link compiled forms of this Software with
software that is licensed under the GPLv2 ("Combined Software") and if a
court of competent jurisdiction determines that the patent provision (Section
3), the indemnity provision (Section 9) or other Section of the License
conflicts with the conditions of the GPLv2, you may retroactively and
prospectively choose to deem waived or otherwise exclude such Section(s) of
the License, but only in their entirety and only with respect to the Combined
Software.
```

同一文件还附有 libc++ 的双许可（University of Illinois/NCSA 与 MIT），逐字如下：

```text
The libc++ library is dual licensed under both the University of Illinois
"BSD-Like" license and the MIT license.  As a user of this code you may choose
to use it under either license.  As a contributor, you agree to allow your code
to be used under both.

==============================================================================

University of Illinois/NCSA
Open Source License

Copyright (c) 2009-2019 by the contributors listed in CREDITS.TXT

All rights reserved.

Developed by:

    LLVM Team

    University of Illinois at Urbana-Champaign

    http://llvm.org

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal with
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

    * Redistributions of source code must retain the above copyright notice,
      this list of conditions and the following disclaimers.

    * Redistributions in binary form must reproduce the above copyright notice,
      this list of conditions and the following disclaimers in the
      documentation and/or other materials provided with the distribution.

    * Neither the names of the LLVM Team, University of Illinois at
      Urbana-Champaign, nor the names of its contributors may be used to
      endorse or promote products derived from this Software without specific
      prior written permission.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.  IN NO EVENT SHALL THE
CONTRIBUTORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS WITH THE
SOFTWARE.

==============================================================================

Copyright (c) 2009-2014 by the contributors listed in CREDITS.TXT

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

## 四、其他说明

- **无 GPL / AGPL 组件**：交付物不链接任何 GPL、LGPL 或 AGPL 代码。语音合成的参考服务端（VITS 的 `Artrajz/vits-simple-api`，AGPL-3.0）只经 HTTP 调用，不复制、不链接其实现。繁简转换曾考虑 `github.com/liuzl/gocc`，因其传递依赖 `github.com/liuzl/cedar-go` 为 GPL-2.0 而改为自写算法 + 内嵌 OpenCC 词典（Apache-2.0，见 §3.5）。
- **字体**：GUI 回落字体为 Go Fonts，经 `gioui.org/font/gofont` → `golang.org/x/image/font/gofont/*` 引入，字体数据随 `golang.org/x/image`（BSD-3-Clause，见 §3.2）覆盖；中文界面在运行时读取本机系统字体（如 `msyh.ttc`），系统字体不随二进制分发，故不在本清单内。
- **运行时外部资源**：MCP server、LLM 服务、语音服务等由用户自行配置，不随本项目分发。本地语音识别所需的 ggml 模型文件（如 `ggml-base-q5_1.bin`）由用户自行下载、放在磁盘上，不随二进制分发，其许可由模型发布方（whisper.cpp 模型仓库）决定。
- 本文件随依赖变更维护；依赖升级的提交应在 PR 说明中注明已同步更新本文件。
