package update

import (
	"context"
	"os"
	"runtime"
	"testing"
)

// TestLiveGitHubProbe 打通真实链路：查最新版 → 选产物 → 取校验和 → 真实下载 → SHA256 比对。
// 默认跳过，只在显式设置 CRONTABUI_LIVE_UPDATE_TEST=1 时运行（避免 CI 依赖外网）。
func TestLiveGitHubProbe(t *testing.T) {
	if os.Getenv("CRONTABUI_LIVE_UPDATE_TEST") == "" {
		t.Skip("设置 CRONTABUI_LIVE_UPDATE_TEST=1 才会访问真实 GitHub")
	}

	c := &Checker{Repo: DefaultRepo, Current: "v0.0.1"}
	ctx := context.Background()

	rel, err := c.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	t.Logf("最新版本 = %s", rel.Tag)
	t.Logf("页面地址 = %s", rel.HTMLURL)
	t.Logf("更新说明 = %d 字符", len(rel.Notes))
	t.Logf("产物共 %d 个", len(rel.Assets))

	name, ok := AssetName(runtime.GOARCH)
	if !ok {
		t.Skipf("平台 %s 没有对应产物", runtime.GOARCH)
	}
	asset, found := rel.FindAsset(name)
	if !found {
		t.Fatalf("Release 中没有 %s", name)
	}
	t.Logf("选中产物 = %s（%d 字节）", asset.Name, asset.Size)

	sums, err := c.Checksums(ctx, rel)
	if err != nil {
		t.Fatalf("Checksums: %v", err)
	}
	want := sums[name]
	if want == "" {
		t.Fatalf("checksums.txt 中没有 %s", name)
	}
	t.Logf("声明校验和 = %s", want)

	f, err := os.CreateTemp(t.TempDir(), "download-*")
	if err != nil {
		t.Fatalf("创建临时文件：%v", err)
	}
	defer f.Close()

	n, err := c.fetch(ctx, asset.URL, f, nil)
	if err != nil {
		t.Fatalf("下载失败：%v", err)
	}
	got, err := sha256File(f.Name())
	if err != nil {
		t.Fatalf("计算校验和：%v", err)
	}
	if got != want {
		t.Fatalf("校验和不匹配：下载得到 %s，声明为 %s", got, want)
	}
	t.Logf("真实下载 %s，SHA256 校验通过 ✔", humanSize(n))

	// 真实产物必须能通过 replace 阶段之前的 ELF 架构校验
	if err := checkELF(f.Name(), runtime.GOARCH); err != nil {
		t.Fatalf("真实产物未通过 ELF 校验：%v", err)
	}
	t.Logf("ELF 架构校验通过（%s）✔", runtime.GOARCH)
}
