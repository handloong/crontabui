// Package update 提供 Web 界面内的自助更新能力：
// 检查 GitHub Release → 下载与本机架构匹配的产物 → 校验完整性 → 原地替换二进制 → 重启服务。
//
// 与项目其余部分保持一致：只依赖标准库，不引入任何第三方包。
package update

import (
	"strings"
	"time"
)

const (
	// DefaultRepo 是默认的 GitHub 发布仓库（owner/name）。
	DefaultRepo = "handloong/crontabui"
	// DefaultAPIBase 是 GitHub API 基地址。
	DefaultAPIBase = "https://api.github.com"
	// defaultAssetPrefix 与 release 工作流产出的二进制命名保持一致。
	defaultAssetPrefix = "crontabui-linux-"
	// checksumAssetName 是发布流程生成的校验和文件名。
	checksumAssetName = "checksums.txt"
)

// Asset 是 Release 中的一个产物文件。
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release 是 GitHub Release 的精简视图，只保留自助更新用得到的字段。
type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Notes       string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// FindAsset 按文件名精确查找产物。
func (r *Release) FindAsset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName 返回指定 GOARCH 对应的发布产物名。
//
// 命名与 .github/workflows/release.yml 严格对应：32 位 ARM 的产物带 v7 后缀，
// 用来和 arm64 区分开。第二个返回值表示该架构是否在支持列表内。
func AssetName(goarch string) (string, bool) {
	switch goarch {
	case "amd64", "386", "arm64":
		return defaultAssetPrefix + goarch, true
	case "arm":
		return defaultAssetPrefix + "armv7", true
	default:
		return "", false
	}
}

// CompareVersions 比较两个版本号：a 更旧返回 -1，相同返回 0，a 更新返回 1。
//
// 支持 "v1.2.3" 与 "1.2.3" 两种写法，非数字段按 0 处理，
// 带 "-rc1" 之类后缀的预发布版本低于同号正式版。
func CompareVersions(a, b string) int {
	an, apre := parseVersion(a)
	bn, bpre := parseVersion(b)
	for i := 0; i < len(an); i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1
			}
			return 1
		}
	}
	// 数字部分相同，则比较预发布后缀：无后缀 > 有后缀
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	default:
		return strings.Compare(apre, bpre)
	}
}

// parseVersion 从版本号里拆出三段数字与预发布后缀。
func parseVersion(v string) ([3]int, string) {
	var nums [3]int
	s := strings.TrimSpace(v)
	s = strings.TrimLeft(s, "vV")

	pre := ""
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}

	for i, part := range strings.SplitN(s, ".", 3) {
		if i >= len(nums) {
			break
		}
		nums[i] = leadingInt(part)
	}
	return nums, pre
}

// leadingInt 取出字符串开头连续的数字，遇到非数字即停止。
func leadingInt(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		// 防止畸形版本号把整数撑爆
		if n > 1<<20 {
			return 1 << 20
		}
	}
	return n
}

// ValidRepo 粗略校验 owner/name 形式的仓库名，避免把奇怪的字符串拼进 URL。
func ValidRepo(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return false
	}
	if strings.ContainsAny(repo, " \t\n?#&\\") {
		return false
	}
	// 只允许一个斜杠
	if strings.Count(repo, "/") != 1 {
		return false
	}
	for i := 0; i < len(repo); i++ {
		c := repo[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == '/':
		default:
			return false
		}
	}
	return true
}
