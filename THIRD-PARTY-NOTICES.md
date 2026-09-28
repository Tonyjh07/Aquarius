# 第三方组件声明（Third-Party Notices）

本文件列出 Aquarius 单二进制交付物（`go build ./cmd/aquarius`）所链接的全部第三方开源组件、版本、许可证与版权声明，供再分发时履行各许可证的署名与告知义务。Aquarius 自身以 MPL-2.0 发布，见根目录 `LICENSE`。

## 一、范围与维护

- **范围**：`go.mod` 中全部 `require`（直接 + 间接），与 `go list -deps ./cmd/aquarius` 的模块集合一致，共 44 个模块。
  - `spike/` 下各目录是独立 `go.mod` 的试验工程，不属于交付物，不在本清单内。
  - `assets/` 下图标为本项目自制素材，不含第三方组件。
- **依赖升级后请重新核对并同步更新本文件**：

  ```bash
  go list -deps -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' ./cmd/aquarius | sort -u
  ```

  对每个模块检查其模块缓存（或上游仓库）中的 `LICENSE`/`COPYING` 文件。
- **文本来源**：下列全文逐字取自各组件对应版本的许可证文件；如本文件与上游存在任何差异，以上游为准。

## 二、组件清单

| 组件 | 版本 | 许可证 | 版权声明（逐字摘自其 LICENSE） |
| --- | --- | --- | --- |
| `gioui.org` | `v0.10.2` | Unlicense OR MIT | `Copyright (c) 2019 The Gio authors` |
| `gioui.org/shader` | `v1.0.9` | Unlicense OR MIT | `Copyright (c) 2019 The Gio authors` |
| `github.com/alecthomas/chroma/v2` | `v2.20.0` | MIT | `Copyright (C) 2017 Alec Thomas` |
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
| `github.com/go-text/typesetting` | `v0.3.4` | Unlicense OR BSD-3-Clause | `Copyright 2021 The go-text authors` |
| `github.com/google/jsonschema-go` | `v0.4.3` | MIT | `Copyright (c) 2025 JSON Schema Go Project Authors` |
| `github.com/gorilla/css` | `v1.0.1` | BSD-3-Clause | `Copyright (c) 2023 The Gorilla Authors. All rights reserved.` |
| `github.com/lucasb-eyer/go-colorful` | `v1.3.0` | MIT | `Copyright (c) 2013 Lucas Beyer` |
| `github.com/mattn/go-isatty` | `v0.0.20` | MIT | `Copyright (c) Yasuhiro MATSUMOTO <mattn.jp@gmail.com>` |
| `github.com/mattn/go-localereader` | `v0.0.1` | MIT（README 声明，模块内无 LICENSE 文件） | README「License: MIT」，作者 Yasuhiro Matsumoto |
| `github.com/mattn/go-runewidth` | `v0.0.17` | MIT | `Copyright (c) 2016 Yasuhiro Matsumoto` |
| `github.com/microcosm-cc/bluemonday` | `v1.0.27` | BSD-3-Clause | `Copyright (c) 2014, David Kitchen <david@buro9.com>` |
| `github.com/modelcontextprotocol/go-sdk` | `v1.8.0` | Apache-2.0 / MIT（过渡期，见 §3.5） | `Copyright (c) 2024-2025 Model Context Protocol a Series of LF Projects, LLC.` |
| `github.com/muesli/ansi` | `v0.0.0-20230316100256-276c6243b2f6` | MIT | `Copyright (c) 2021 Christian Muehlhaeuser` |
| `github.com/muesli/cancelreader` | `v0.2.2` | MIT | `Copyright (c) 2022 Erik Geiser and Christian Muehlhaeuser` |
| `github.com/muesli/reflow` | `v0.3.0` | MIT | `Copyright (c) 2019 Christian Muehlhaeuser` |
| `github.com/muesli/termenv` | `v0.16.0` | MIT | `Copyright (c) 2019 Christian Muehlhaeuser` |
| `github.com/rivo/uniseg` | `v0.4.7` | MIT | `Copyright (c) 2019 Oliver Kuederle` |
| `github.com/segmentio/asm` | `v1.1.3` | MIT | `Copyright (c) 2021 Segment` |
| `github.com/segmentio/encoding` | `v0.5.4` | MIT | `Copyright (c) 2019 Segment.io, Inc.` |
| `github.com/xo/terminfo` | `v0.0.0-20220910002029-abceb7e1c41e` | MIT | `Copyright (c) 2016 Anmol Sethi` |
| `github.com/yosida95/uritemplate/v3` | `v3.0.2` | BSD-3-Clause | `Copyright (C) 2016, Kohei YOSHIDA <https://yosida95.com/>. All rights reserved.` |
| `github.com/yuin/goldmark` | `v1.7.13` | MIT | `Copyright (c) 2019 Yusuke Inuzuka` |
| `github.com/yuin/goldmark-emoji` | `v1.0.6` | MIT | `Copyright (c) 2020 Yusuke Inuzuka` |
| `golang.org/x/exp/shiny` | `v0.0.0-20250408133849-7e4ce0ab07d0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/image` | `v0.26.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/net` | `v0.48.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/oauth2` | `v0.35.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/sync` | `v0.20.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/sys` | `v0.41.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/term` | `v0.38.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/text` | `v0.32.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |
| `golang.org/x/time` | `v0.15.0` | BSD-3-Clause | `Copyright 2009 The Go Authors.` |

许可证分布：MIT 28 个、BSD-3-Clause 12 个、Unlicense OR MIT 2 个、Unlicense OR BSD-3-Clause 1 个、Apache-2.0 / MIT 过渡 1 个。

## 三、许可证全文

### 3.1 MIT License（28 个组件）

适用组件：

`github.com/alecthomas/chroma/v2`、`github.com/aymanbagabas/go-osc52/v2`、`github.com/aymerick/douceur`、`github.com/charmbracelet/bubbletea`、`github.com/charmbracelet/colorprofile`、`github.com/charmbracelet/glamour`、`github.com/charmbracelet/lipgloss`、`github.com/charmbracelet/x/ansi`、`github.com/charmbracelet/x/cellbuf`、`github.com/charmbracelet/x/exp/slice`、`github.com/charmbracelet/x/term`、`github.com/dlclark/regexp2`、`github.com/erikgeiser/coninput`、`github.com/google/jsonschema-go`、`github.com/lucasb-eyer/go-colorful`、`github.com/mattn/go-isatty`、`github.com/mattn/go-localereader`（依其 README 声明）、`github.com/mattn/go-runewidth`、`github.com/muesli/ansi`、`github.com/muesli/cancelreader`、`github.com/muesli/reflow`、`github.com/muesli/termenv`、`github.com/rivo/uniseg`、`github.com/segmentio/asm`、`github.com/segmentio/encoding`、`github.com/xo/terminfo`、`github.com/yuin/goldmark`、`github.com/yuin/goldmark-emoji`。

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

### 3.2 BSD-3-Clause（12 个组件）

适用组件：

`github.com/gorilla/css`、`github.com/microcosm-cc/bluemonday`、`github.com/yosida95/uritemplate/v3`、`golang.org/x/exp/shiny`、`golang.org/x/image`、`golang.org/x/net`、`golang.org/x/oauth2`、`golang.org/x/sync`、`golang.org/x/sys`、`golang.org/x/term`、`golang.org/x/text`、`golang.org/x/time`。

各组件的版权行见上表（逐字）。许可声明正文取自 `golang.org/x/sys@v0.41.0/LICENSE`（逐字，省去其版权行 `Copyright 2009 The Go Authors.`）；`golang.org/x/*` 九个模块文本完全一致，其余三个组件的第三条（名称背书条款）措辞以其上游原文为准：

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

### 3.5 Apache-2.0 / MIT 过渡期（1 个组件：`github.com/modelcontextprotocol/go-sdk`）

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
      submitted to Licensor for inclusion in the Work by the copyright
      owner or by an individual or Legal Entity authorized to submit on behalf
      of the copyright owner. For the purposes of this definition, "submitted"
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

## 四、其他说明

- **字体**：GUI 回落字体为 Go Fonts，经 `gioui.org/font/gofont` → `golang.org/x/image/font/gofont/*` 引入，字体数据随 `golang.org/x/image`（BSD-3-Clause，见 §3.2）覆盖；中文界面在运行时读取本机系统字体（如 `msyh.ttc`），系统字体不随二进制分发，故不在本清单内。
- **运行时外部资源**：MCP server、LLM 服务等由用户自行配置，不随本项目分发。
- 本文件随依赖变更维护；依赖升级的提交应在 PR 说明中注明已同步更新本文件。
