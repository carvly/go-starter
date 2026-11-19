package archtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var modulePath = func() string {
	cmd := exec.Command("go", "list", "-m")
	cmd.Dir = repoRoot()
	out, err := cmd.Output()
	if err != nil {
		panic(fmt.Sprintf("go list -m: %v", err))
	}
	return strings.TrimSpace(string(out))
}()

type pkg struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// trackSources 让 go test 的缓存随源码变化失效：go test 只记录测试进程自己
// 访问过的文件，看不到 go list 子进程读了什么，不这样做会拿到过期的通过结果。
func trackSources(t *testing.T) {
	t.Helper()
	root := repoRoot()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				_, err = os.Stat(path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func listPackages(t *testing.T) []pkg {
	t.Helper()
	trackSources(t)
	// 带上 integration 标签，集成测试和 pgtest 的导入同样受检查。
	cmd := exec.Command("go", "list", "-tags=integration", "-json", modulePath+"/...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// location 描述一个仓库内的包：layer 是 internal 下的第一级目录（modules、
// platform、kit、app、config），模块内的包再带上 module 和 sub
// （sub 为空表示模块根包，即 module.go 所在的包）。
type location struct {
	internal bool
	layer    string
	module   string
	sub      string
}

func locate(importPath string) location {
	rest, ok := strings.CutPrefix(importPath, modulePath+"/internal/")
	if !ok {
		return location{}
	}
	parts := strings.Split(rest, "/")
	loc := location{internal: true, layer: parts[0]}
	if loc.layer == "modules" && len(parts) > 1 {
		loc.module = parts[1]
		loc.sub = strings.Join(parts[2:], "/")
	}
	return loc
}

func isStdlib(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

func isTestHelper(importPath string) bool {
	last := path.Base(importPath)
	return strings.HasSuffix(last, "test") && last != "archtest"
}

// entrySubs 是模块的接入包：把 HTTP 请求、后台请求、队列任务转成对 Service 的调用。
var entrySubs = []string{"httpapi", "adminapi", "jobs"}

func isEntrySub(sub string) bool {
	first, _, _ := strings.Cut(sub, "/")
	return slices.Contains(entrySubs, first)
}

func trim(p string) string { return strings.TrimPrefix(p, modulePath+"/") }

// check 返回违规说明；空字符串表示允许。说明的格式固定为"为什么不允许 + 应该怎么做"。
func check(from, to string, testOnly bool) string {
	src, dst := locate(from), locate(to)

	if !testOnly && isTestHelper(to) && strings.HasPrefix(to, modulePath+"/") {
		return "测试替身包只能在 _test.go 里导入。生产代码需要这个能力时，把它做成正式实现放进对应的包"
	}

	if dst.layer == "modules" && src.layer != "app" && src.module != dst.module {
		isEvents := dst.sub == "events" || strings.HasPrefix(dst.sub, "events/")
		if src.layer == "modules" && !isEvents {
			return fmt.Sprintf("模块之间不能互相 import（只有 events 包是公开契约）。请在 %s 里定义只包含所需方法的窄接口"+
				"（参考 internal/modules/beta/domain/alpha_lookup.go），由 internal/app/adapters.go 用 %s 的能力实现它", src.module, dst.module)
		}
		if src.layer != "modules" && src.layer != "platform" {
			return "只有组合根 internal/app 可以导入业务模块。需要模块能力时，在 internal/app 里装配并注入"
		}
	}

	if src.layer == "modules" && isEntrySub(src.sub) && dst.module == src.module &&
		(dst.sub == "postgres" || strings.HasPrefix(dst.sub, "postgres/")) {
		return fmt.Sprintf("接入包（httpapi、adminapi、jobs）只能经本模块的 Service 读写数据，绕过它就绕过了校验、缓存失效和 AfterCommit。"+
			"需要的能力加到 %s/domain 的 Service 上，Repository 由 module.go 组装进 Service", src.module)
	}

	// 分层纯度规则只约束生产代码；测试可以用 goleak 这类测试库。
	switch {
	case testOnly:
	case src.layer == "kit":
		if !isStdlib(to) && (!dst.internal || dst.layer != "kit") {
			return "kit 只能依赖标准库，否则 domain 会经由 kit 间接依赖第三方库。需要第三方能力就放进 internal/platform"
		}

	case src.layer == "modules" && (src.sub == "events" || strings.HasPrefix(src.sub, "events/")):
		if !isStdlib(to) {
			return "events 是对外公开的事件契约，只能包含纯数据类型、只依赖标准库，这样其它模块导入它不会被带进任何基础设施"
		}

	case src.layer == "modules" && (src.sub == "domain" || strings.HasPrefix(src.sub, "domain/")):
		allowed := isStdlib(to) ||
			dst.internal && dst.layer == "kit" ||
			dst.module == src.module && (dst.sub == "events" || dst.sub == "domain" || strings.HasPrefix(dst.sub, "domain/"))
		if !allowed {
			return fmt.Sprintf("domain 只能依赖标准库、internal/kit 和本模块的 events。"+
				"需要基础设施能力就在 %s/domain 里定义窄接口，由本模块的 postgres/ 等子包或 internal/platform 实现，在 module.go 里注入", src.module)
		}

	case src.layer == "platform":
		if dst.layer == "modules" || dst.layer == "app" {
			return "platform 不认识业务模块。业务类型放在 modules/<m>，platform 只提供与业务无关的能力（泛型、接口、通用参数）"
		}
	}

	if dst.layer == "app" && src.internal && src.layer != "app" {
		return "internal/app 是组合根，只能被 cmd/* 导入"
	}
	return ""
}

func TestBoundaries(t *testing.T) {
	for _, p := range listPackages(t) {
		type edge struct {
			to       string
			testOnly bool
		}
		var edges []edge
		for _, imp := range p.Imports {
			edges = append(edges, edge{imp, false})
		}
		for _, imp := range slices.Concat(p.TestImports, p.XTestImports) {
			edges = append(edges, edge{imp, true})
		}
		for _, e := range edges {
			if e.to == p.ImportPath {
				continue
			}
			why := check(p.ImportPath, e.to, e.testOnly)
			if why == "" || excepted(p.ImportPath, e.to) {
				continue
			}
			t.Errorf("%s 导入了 %s：%s。", trim(p.ImportPath), trim(e.to), why)
		}
	}
}

func excepted(from, to string) bool {
	for _, e := range exceptions {
		if trim(from) == e.From && trim(to) == e.To {
			return true
		}
	}
	return false
}

func TestExceptions_AreJustifiedAndNotExpired(t *testing.T) {
	for _, e := range exceptions {
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("例外 %s → %s 没有写 Reason", e.From, e.To)
		}
		if e.Until.IsZero() || time.Now().After(e.Until) {
			t.Errorf("例外 %s → %s 已过期或没有 Until：消除这条依赖，或在 PR 中说明理由后延期", e.From, e.To)
		}
	}
}
