package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxArtifactBytes 限制单个产物的下载体积，异常响应不会把磁盘写满。
	maxArtifactBytes = 64 << 20
	// maxReleaseJSONBytes 限制 Release 元信息响应体积。
	maxReleaseJSONBytes = 1 << 20
	// checkTimeout 是查询最新版本的超时。
	checkTimeout = 20 * time.Second
	// downloadTimeout 是下载产物的超时。国内直连 GitHub 可能很慢，留足余量。
	downloadTimeout = 10 * time.Minute
	// userAgent 是请求 GitHub 时使用的标识。
	userAgent = "crontabui-self-update"
)

// defaultTrustedHosts 是允许下载产物来源的主机白名单。
// GitHub 的下载链接会从 github.com 重定向到下面这些 CDN 域名，都要放行。
var defaultTrustedHosts = []string{
	"github.com",
	"api.github.com",
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
	"github-releases.githubusercontent.com",
}

// Checker 负责查询 Release 信息并把产物下载到本地。
type Checker struct {
	// Repo 是 owner/name 形式的仓库名，留空则用 DefaultRepo。
	Repo string
	// Current 是当前运行的版本号，用于判断是否有更新。
	Current string
	// APIBase 是 GitHub API 基地址，留空则用 DefaultAPIBase。
	APIBase string
	// Client 覆盖默认 HTTP 客户端（测试用）。
	Client *http.Client
	// trustedHosts 覆盖下载来源白名单（测试用）。
	trustedHosts []string
}

// repo 返回实际使用的仓库名。
func (c *Checker) repo() string {
	if strings.TrimSpace(c.Repo) != "" {
		return strings.TrimSpace(c.Repo)
	}
	return DefaultRepo
}

// apiBase 返回实际使用的 API 基地址。
func (c *Checker) apiBase() string {
	if strings.TrimSpace(c.APIBase) != "" {
		return strings.TrimRight(strings.TrimSpace(c.APIBase), "/")
	}
	return DefaultAPIBase
}

// hostAllowed 判断下载来源主机是否可信。
func (c *Checker) hostAllowed(host string) bool {
	hosts := c.trustedHosts
	if len(hosts) == 0 {
		hosts = defaultTrustedHosts
	}
	for _, h := range hosts {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// httpClient 返回带连接复用与重定向校验的客户端。
// 测试注入的 Client 原样返回，方便指向本地 httptest 服务。
func (c *Checker) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     60 * time.Second,
			MaxIdleConnsPerHost: 2,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("重定向次数过多")
			}
			// 每一跳都要落在白名单内，避免被中间跳转带到任意主机
			if !c.hostAllowed(req.URL.Host) {
				return fmt.Errorf("下载被重定向到不受信任的主机：%s", req.URL.Host)
			}
			return nil
		},
	}
}

// Latest 查询仓库的最新正式版 Release（GitHub 的 latest 接口会自动排除预发布与草稿）。
func (c *Checker) Latest(ctx context.Context) (*Release, error) {
	endpoint := c.apiBase() + "/repos/" + c.repo() + "/releases/latest"

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败：%w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 GitHub 失败（检查网络或代理）：%w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("仓库 %s 不存在或还没有正式发布版本", c.repo())
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub API 访问受限（可能触发了速率限制），请稍后再试")
	default:
		return nil, fmt.Errorf("GitHub API 返回 HTTP %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseJSONBytes)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 Release 信息失败：%w", err)
	}
	if strings.TrimSpace(rel.Tag) == "" {
		return nil, errors.New("Release 信息缺少版本号")
	}
	return &rel, nil
}

// Checksums 下载并解析 Release 里的 checksums.txt，返回「文件名 → sha256 小写十六进制」。
func (c *Checker) Checksums(ctx context.Context, rel *Release) (map[string]string, error) {
	asset, ok := rel.FindAsset(checksumAssetName)
	if !ok {
		return nil, nil
	}
	var buf bytes.Buffer
	if _, err := c.fetch(ctx, asset.URL, &buf, nil); err != nil {
		return nil, err
	}

	sums := make(map[string]string)
	for _, line := range strings.Split(buf.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if len(sum) != 64 {
			continue
		}
		sums[name] = sum
	}
	return sums, nil
}

// fetch 把 rawURL 的内容写入 w，返回写入的字节数。
// onProgress 在下载过程中被回调（已写字节数、总字节数；总长度未知时为 0）。
func (c *Checker) fetch(ctx context.Context, rawURL string, w io.Writer, onProgress func(done, total int64)) (int64, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("产物地址无效：%w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return 0, fmt.Errorf("产物地址协议不受支持：%s", u.Scheme)
	}
	if !c.hostAllowed(u.Host) {
		return 0, fmt.Errorf("产物地址来自不受信任的主机：%s", u.Host)
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("构造下载请求失败：%w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArtifactBytes {
		return 0, fmt.Errorf("产物体积 %s 超过上限，已拒绝下载", humanSize(resp.ContentLength))
	}

	var written int64
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxArtifactBytes {
				return written, fmt.Errorf("产物超过 %s 上限，已中止下载", humanSize(maxArtifactBytes))
			}
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return written, fmt.Errorf("写入下载文件失败：%w", writeErr)
			}
			if onProgress != nil {
				onProgress(written, resp.ContentLength)
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, fmt.Errorf("读取下载内容失败：%w", readErr)
		}
	}
}
