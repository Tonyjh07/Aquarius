package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDomainPortImportStdlibOnly domain 与 port 两个内核层只认标准库与本模块内的包。
//
// 为什么单立一条：分层方向测试（TestDependencyDirection）把非本模块的 import 归入
// layerOther 放行——那条测试管「层与层之间谁指谁」，外部依赖不在那条测试的判定范围里。
// 于是 domain 哪天 import 了一个三方 SDK，守门测试不会变红，而领域层混进三方 SDK
// 正是硬性规则 1 要挡的头号入口（三方 SDK 自带的类型会顺着 import 渗进领域签名）。
//
// 口径：stdlib 的 import 路径首段不含点号；本模块的 import 以模块路径开头。
// domain 现在引用的 i18n 是叶子文案包，属于「本模块内」，不在禁止之列。
func TestDomainPortImportStdlibOnly(t *testing.T) {
	root, modPath := moduleRoot(t)

	var bad []string
	scanned := 0
	walkProd(t, root, modPath, func(file, pkgPath string, imports []string) {
		l := classify(modPath, pkgPath)
		if l != layerDomain && l != layerPort {
			return
		}
		scanned++
		for _, imp := range imports {
			if strings.HasPrefix(imp, modPath) {
				continue
			}
			if !strings.Contains(imp, ".") { // 首段无点号 = 标准库
				continue
			}
			bad = append(bad, file+": 内核层引用了三方依赖 "+imp)
		}
	})
	if scanned == 0 {
		t.Fatal("没有扫到 domain/port 的生产文件，守门测试等于没跑")
	}
	sort.Strings(bad)
	for _, msg := range bad {
		t.Error(msg)
	}
}

// TestDomainHasNoStreamingSurface 领域层不出现流式词汇的导出符号。
//
// 硬性规则 3 的完整语义（增量只流经 UI、一段生成结束一次性 Commit）没法纯静态判定，
// 这条守门抓的是该规则的主要失败模式：领域层长出 Stream/Delta/Chunk/Increment 形状的
// 导出 API。领域节点只存定稿内容，流式增量一旦在领域里有了类型，就会有人开始把
// 半截文本挂到树上。现有命名里唯一的 Delta 是 domain/conversation 的 Usage——那是
// 服务端回传的用量统计，落在注释里，不是符号名，不在此列。
//
// 这是黑名单口径：拦的是已知词汇，不是证明无流式。真要往领域里加流式概念，
// 这条测试会拦住，逼着先回文档改规则。
func TestDomainHasNoStreamingSurface(t *testing.T) {
	root, modPath := moduleRoot(t)

	terms := []string{"Stream", "Delta", "Chunk", "Increment"}
	var bad []string
	scanned := 0
	walkProd(t, root, modPath, func(file, pkgPath string, _ []string) {
		if classify(modPath, pkgPath) != layerDomain {
			return
		}
		scanned++
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, file), nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", file, err)
		}
		for _, decl := range f.Decls {
			for _, name := range declaredNames(decl) {
				if !ast.IsExported(name) {
					continue
				}
				for _, term := range terms {
					if strings.Contains(name, term) {
						bad = append(bad, file+": 领域层导出符号 "+name+" 含流式词汇 "+term)
					}
				}
			}
		}
	})
	if scanned == 0 {
		t.Fatal("没有扫到 domain 的生产文件，守门测试等于没跑")
	}
	for _, msg := range bad {
		t.Error(msg)
	}
}

// declaredNames 取一个顶层声明里的导出标识符（类型/函数/变量/常量的名字）。
func declaredNames(decl ast.Decl) []string {
	var names []string
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil { // 方法跟着接收者类型走，不重复计数
			names = append(names, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					names = append(names, n.Name)
				}
			}
		}
	}
	return names
}

// TestPortInterfacesStaySmall 端口接口保持消费方侧的小接口（1-3 个方法）。
//
// 「接口由消费方定义、保持小」写在 AGENTS.md 代码风格节，此前只靠 review 人守；
// 数一数就知道已经飘出去了：树视图、待发清单都是 6 个方法。这条守门把存量超标者
// 登记进白名单，只拦**新增**的超标接口与白名单条目的继续膨胀——存量收敛（把
// TreeView 按消费方切开）留给后续重构，不在这条测试里一起做。
//
// 豁免两种形态：
//   - 纯嵌入接口（一个显式方法都没有）：JobManager 嵌五个小接口、Delegate 嵌两个，
//     这是「消费方侧小接口 + 聚合面」的组合用法，本身正是要保住的形状；
//   - 白名单存量：删一个方法要动消费方，按各自的节奏收。
func TestPortInterfacesStaySmall(t *testing.T) {
	root, _ := moduleRoot(t)
	dir := filepath.Join(root, "internal", "port")

	// 白名单：存量超 3 方法的接口，登记时写一句「为什么还小不了」。
	oversized := map[string]string{
		"TreeView":          "树视图反向端口：app 与两个前端各自消费不同侧面（D102/D150/D272），收敛要等消费方齐",
		"Pending":           "待发清单：条目增删改查加容量元信息，前端（GUI/TUI）整面直接读写（D272）",
		"MemoryStore":       "记忆文档的读/写/删/列/存在性五步，memory_* 工具与 MCP 投影整面消费",
		"ConversationStore": "会话持久化：存/取/列/删四面，cmd 与 app 各取所需",
		"AttachmentStore":   "附件存储：取引用/读流/写/删，摄取与 blob 两方消费",
		"JobReader":         "任务日志读取：状态、日志、剩余量、等待，job_* 工具整面消费",
	}

	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("列 internal/port 失败: %v", err)
	}
	count := 0 // 解析到的接口总数，用来证明扫描没落空
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", path, err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				count++
				named := 0
				embedded := 0
				for _, field := range it.Methods.List {
					if len(field.Names) == 0 {
						embedded++
						continue
					}
					named += len(field.Names)
				}
				if named == 0 && embedded > 0 {
					continue // 纯聚合面
				}
				if named <= 3 {
					continue
				}
				if _, listed := oversized[ts.Name.Name]; listed {
					continue
				}
				t.Errorf("port.%s 有 %d 个方法（上限 3）：要么按消费方切开，要么进白名单并写明理由",
					ts.Name.Name, named)
			}
		}
	}
	if count < 20 {
		t.Fatalf("只解析到 %d 个接口，远低于现状——扫描范围或解析方式出了问题", count)
	}
}

// TestAdapterPeersAreSharedInfra 适配器之间只许引用登记在册的共用件。
//
// 分层测试允许 adapter → adapter 全量互引（注释里写明的理由：下层共用件正是被同层
// 复用的），但「允许」不等于「随便」：适配器 A 直接 import 适配器 B 的具体实现，
// B 就从「端口的一个实现」变成了公共库，端口契约被绕开。这条守门把现状里真实存在
// 的互引逐个登记，新增互引必须先来这里登记——登记本身就是一个 review 关口。
func TestAdapterPeersAreSharedInfra(t *testing.T) {
	root, modPath := moduleRoot(t)
	adapterPrefix := modPath + "/internal/adapter/"

	// sharedPeers 允许被别的适配器引用的共用件，逐个写明身份。
	sharedPeers := map[string]string{
		"audiohttp":  "语音 HTTP 共享层（base_url 校验/状态码报因/音频读取/WAV 判定/落库），8 个语音适配器共用",
		"atomicfile": "原子写原语（同目录临时文件 + rename 重试），存储类适配器的公共地基",
		"storejson":  "会话 JSON 存储门面（含 .bak 轮换），exchange 借该门面做导入导出的落盘",
		"pathres":    "路径真实落点解析，toolrun 与 acpgate 共用的权限判据单一事实源",
		"uicommon":   "多前端共用件（输入切分/命令分派/文本视图），repl/uitui/uigui/notify 消费",
		"micwin":     "Windows 录音平台原语，ingestmic 借该原语拿麦克风流",
	}

	var bad []string
	scanned := 0
	walkProd(t, root, modPath, func(file, pkgPath string, imports []string) {
		rel := strings.TrimPrefix(pkgPath, adapterPrefix)
		if rel == pkgPath { // 不是 adapter 包
			return
		}
		own := strings.SplitN(rel, "/", 2)[0]
		scanned++
		for _, imp := range imports {
			if !strings.HasPrefix(imp, adapterPrefix) {
				continue
			}
			peer := strings.TrimPrefix(imp, adapterPrefix)
			peer = strings.SplitN(peer, "/", 2)[0]
			if peer == own {
				continue // 自己包内的子包（如 uigui/platform）
			}
			if _, ok := sharedPeers[peer]; ok {
				continue
			}
			bad = append(bad, file+": 适配器引用了未登记的同层实现 "+peer+
				"（要么该实现该抽成共用件，要么这条引用该走端口）")
		}
	})
	if scanned < 30 {
		t.Fatalf("只扫到 %d 个 adapter 包，远低于现状——扫描范围出了问题", scanned)
	}
	sort.Strings(bad)
	for _, msg := range bad {
		t.Error(msg)
	}
}
