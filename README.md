# CrontabUI

极简的 **Linux crontab 可视化管理器**：单个静态二进制、**零第三方依赖**、常驻内存约 **15MB**。

浏览器里查看 / 新增 / 编辑 / 删除定时任务，启用停用、立即执行、在线看任务日志与系统 cron 日志，并支持**界面内一键自助更新**。

```
  CrontabUI v1.0.4  ·  Linux crontab 管理器
  ─────────────────────────────────────────────
  访问地址   http://127.0.0.1:8080/
  日志目录   /var/log/crontabui
  备份目录   /var/lib/crontabui/backups
  内存上限   40 MiB
  自助更新   已启用（handloong/crontabui / linux/amd64）
```

## 特性

| 特性 | 说明 |
|---|---|
| 任务管理 | 查看 / 新增 / 编辑 / 删除 / 一键启用停用 / 立即执行 |
| 表达式助手 | 5 字段写法与 `@reboot`、`@daily` 等宏，带**字段级中文错误**与**中文可读描述**（`*/5 * * * *` → 每 5 分钟） |
| 日志查看 | ① 任务输出日志（尾读 + 导出 + 清空）② 系统 cron 日志（`/var/log/cron` → `syslog` → `journalctl` 自动探测） |
| 零侵入 | 未托管内容**逐字节原样保留**，绝不覆盖你手写的 crontab；每次写入前自动备份，保留最近 10 份 |
| 登录鉴权 | `--token` 指定令牌，登录页只填一个 Token；会话存内存、不落盘 |
| 自助更新 | 界面内检查 GitHub Release → 按本机架构下载 → SHA256 校验 → 原子替换 → 自动重启；旧版本留 `.bak` 可回滚 |
| 轻量安全 | 二进制约 **6.1MB**、RSS 约 **15MB**；原生 HTML/CSS/JS 无 CDN 可离线；常量时间令牌比较、登录限流、同源校验、CSP、`HttpOnly` + `SameSite=Strict` |

## 快速开始

从 [Releases](https://github.com/handloong/crontabui/releases/latest) 下载对应架构的包（`checksums.txt` 可校验完整性）：

| 包 | 适用机器 |
|---|---|
| `crontabui_<版本>_linux_amd64.tar.gz` | x86_64（绝大多数 VPS / 云服务器） |
| `crontabui_<版本>_linux_386.tar.gz` | 32 位 x86 老机器 |
| `crontabui_<版本>_linux_arm64.tar.gz` | ARMv8：树莓派 3/4/5、ARM 云主机 |
| `crontabui_<版本>_linux_armv7.tar.gz` | ARMv7 硬浮点：树莓派 2 / Zero 2 |

```bash
tar -xzf crontabui_1.0.4_linux_amd64.tar.gz && cd crontabui_1.0.4_linux_amd64
sudo ./install-corntab.sh --dry-run    # 预演，不改动系统
sudo ./install-corntab.sh              # 正式安装
```

安装脚本会**自动识别本机架构**，并校验包内二进制的 ELF 位宽与机器码是否匹配，不匹配直接拒绝并提示该下哪个包。ARMv6（树莓派 1 / Zero 初代）没有预编译包，需自行用源码以 `GOARM=6` 编译。

### 从源码构建

```bash
./build.sh          # Linux / macOS / WSL，产物在 dist/

make linux-amd64    # x86_64              make linux-arm64   # ARMv8
make linux-386      # 32 位 x86            make linux-arm     # ARMv7（产物名带 v7）
make test           # 跑测试
```

产物为静态链接、无 CGO、不挑发行版。

### 手工运行

```bash
sudo install -m 755 dist/crontabui-linux-amd64 /usr/local/bin/crontabui
sudo /usr/local/bin/crontabui --token '换成你自己的长随机令牌' --port 8080
curl -s http://127.0.0.1:8080/healthz   # {"ok":true,"status":"healthy"}
```

打开 `http://<服务器IP>:8080/`，输入令牌即可登录。

> **强烈建议**保持默认的 `--bind 127.0.0.1`，再通过 Nginx 反向代理加 HTTPS 对外提供服务。直接暴露端口时程序会打印安全警告。

## 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--token` | 无（**必填**） | 登录令牌，至少 8 位；也可用环境变量 `CRONTABUI_TOKEN` |
| `--bind` / `--port` | `127.0.0.1` / `8080` | 监听地址（只填地址不含端口）与端口 |
| `--user` | 当前用户 | 管理指定用户的 crontab，跨用户需以 root 运行 |
| `--log-dir` | `/var/log/crontabui` | 任务日志目录，不可写时自动回退到 `~/.crontabui/logs` |
| `--data-dir` | `~/.crontabui` | 备份目录，实际写 `backups/` 子目录 |
| `--cron-log` | 自动探测 | 显式指定系统 cron 日志路径 |
| `--crontab-bin` | `crontab` | crontab 可执行文件路径 |
| `--mem-limit` | `40` | 内存上限（MiB），`0` 表示不限制 |
| `--session-ttl` | `12h` | 会话滑动有效期 |
| `--self-update` | `true` | `--self-update=false` 关闭界面内自助更新 |
| `--update-repo` | `handloong/crontabui` | 自助更新使用的 GitHub 仓库（`owner/name`） |
| `--version` / `-h` | - | 打印版本 / 查看帮助 |

## 部署

### 一键安装（推荐）

`install-corntab.sh` 一条命令做完：检查环境 → 装 `cron` 包 → 生成随机令牌（写入 `0600` 环境文件）→ 安装并加固 systemd 单元 → 开机自启 → 健康检查。**重复执行即为升级**，令牌与配置保持不变。

```bash
sudo ./install-corntab.sh --dry-run          # 预演，不改动系统
sudo ./install-corntab.sh                    # 安装 / 升级
sudo ./install-corntab.sh --port 9090        # 换端口
sudo ./install-corntab.sh --user alice       # 管理 alice 的 crontab（服务仍以 root 运行）
sudo ./install-corntab.sh --run-as www-data  # 让服务以非 root 账号运行
sudo ./install-corntab.sh --status           # 查看状态、端口、内存占用、令牌
sudo ./install-corntab.sh --uninstall        # 卸载（保留日志与备份；加 --purge 一并删除）
```

安装前的硬性检查：必须 root、必须 Linux、必须命中 4 种架构之一（`x86_64` / `i686` / `aarch64` / `armv7l`）、必须有 systemd、被安装文件必须是 **ELF 且位宽与机器码都与本机架构一致**（amd64=`3e00`、386=`0300`、arm64=`b700`、arm=`2800`）、端口与内存上限范围校验、`--bind 0.0.0.0` 时打印暴露警告。

> 脚本**源文件**在 `deploy/install-corntab.sh`（受版本管理），构建时自动复制一份到 `dist/`。

### systemd / Nginx / 日志轮转

仓库另提供手工部署所需的加固单元与示例配置：

```bash
sudo install -d -m 700 /etc/crontabui
printf 'CRONTABUI_TOKEN=%s\n' "$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')" | sudo tee /etc/crontabui/crontabui.env >/dev/null
sudo chmod 600 /etc/crontabui/crontabui.env && sudo cat /etc/crontabui/crontabui.env   # 记下令牌

sudo mkdir -p /var/log/crontabui /var/lib/crontabui
sudo cp deploy/crontabui.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now crontabui
journalctl -u crontabui -f
```

- **Nginx + HTTPS**：参考 `deploy/nginx.conf.example`。要点是**不**把 `--bind` 改成 `0.0.0.0`，TLS 交给 Nginx。
- **日志轮转**：任务日志会持续增长，建议 `sudo cp deploy/crontabui.logrotate /etc/logrotate.d/crontabui`。

## 自助更新

登录后顶栏显示当前版本，旁边的「检查更新」会去查 GitHub 上的最新 Release；有新版本时点「立即更新」，即可在浏览器里走完**下载 → 校验 → 替换 → 重启**，不用登服务器。

只与你确认过的仓库打交道：默认 `handloong/crontabui`，可 `--update-repo owner/name` 换成自己的 fork；产物按本机架构自动挑选（`amd64` / `386` / `arm64` / `armv7`），与发布工作流的命名严格对应。

安全设计：

- **必须登录 + 手动触发**：接口一律在会话鉴权之后，写入类请求还要求同源；不轮询、不后台自动升级。
- **SHA256 强制校验**：下载后与 Release 里的 `checksums.txt` 逐字节核对，**校验文件缺失或不匹配一律中止**。
- **ELF 头校验**：确认拿到的是本机架构的可执行文件，挡住「下错架构」和「下载到的是错误页」。
- **来源白名单**：只允许从 `github.com` 及其官方 CDN 下载，重定向的每一跳都要过白名单。
- **原子替换 + 自动备份**：新二进制先落在目标目录再 `rename` 覆盖，旧版本留在 `<二进制路径>.bak`。
- **重启方式**：systemd 环境下给自己发 `SIGTERM` 走既有的优雅关闭流程，靠单元的 `Restart=always` 拉起新进程——既不需要 `systemctl` 权限，也不依赖后台子进程存活（systemd 重启时会清掉整个 cgroup）。

要求二进制所在目录对服务账号可写（自带的安装脚本装在 `/usr/local/bin/crontabui` 且服务以 root 运行，满足），单元需保留 `Restart=always`（`deploy/crontabui.service` 已配置）。

> 自助更新本质上等于「以服务身份执行一个下载来的二进制」。在这套工具里它不会带来额外提权——能编辑 crontab 的用户本来就能以同样身份执行任意命令——但如果服务跑在权限较大的账号下，请确保只有可信的人拿得到登录令牌。不需要这个能力就用 `--self-update=false` 关掉。

**回滚**：更新后发现问题时，把备份换回去再重启即可：

```bash
sudo systemctl stop crontabui
sudo mv /usr/local/bin/crontabui.bak /usr/local/bin/crontabui
sudo systemctl start crontabui
```

## 工作原理

**任务如何写入 crontab**：程序直接读写系统 crontab（`crontab -l` 读取、`crontab -` 覆盖写入），保存即生效。为能稳定地识别、编辑和删除任务，每个托管任务会在其**上一行**写一条标记注释：

```crontab
MAILTO=root
0 1 * * * /usr/bin/foo.sh                        ← 你手写的任务，程序原样保留

#CRONTABUI: 5a31e73e 每日备份                      ← 托管标记（ID + 名称）
0 3 * * * /opt/backup.sh >> /var/log/crontabui/5a31e73e.log 2>&1
```

- 标记**独占一行**而非行尾注释：Vixie cron 不支持行尾注释，`cmd # note` 会把 `# note` 当成命令的一部分执行。
- **停用** = 给命令行加 `#` 前缀（标记行保留，随时可恢复）；**删除** = 只移除标记行 + 命令行。
- 每次修改都重新 `crontab -l` 再合并，不会覆盖你在终端里做的编辑；写入前备份到 `<data-dir>/backups/`，保留最近 10 份。

**日志如何产生**：在任务编辑弹窗勾选「捕获输出」后，程序自动在命令末尾追加 `>> <log-dir>/<任务ID>.log 2>&1`；解析时会识别并剥离这段后缀，所以界面上看到的始终是你写的原始命令。

日志读取采用**反向分块**（从文件末尾按 32KB 块往回读，凑够行数或用满 512KB 就停），因此**日志文件多大都不影响内存占用**。日志弹窗的「任务日志」页另有两个操作：

- **导出**：下载**完整原文件**（不受「显示末尾 N 行」影响），文件名形如 `<任务名>-20261013-145702.log`；非 ASCII 任务名走 RFC 6266 的 `filename*`，中文不乱码。
- **删除**：**清空内容但保留文件**——实现是 `truncate` 而非 `unlink`。任务输出用 `>>`（`O_APPEND`）打开，若直接删文件，正在运行中的那次任务仍持有已删除文件的 fd，后续输出会全部丢失、空间也要等它退出才释放；截断则 inode 不变，写入方无感知。

这两个操作**只在「任务日志」标签页出现**：系统 cron 日志由 syslog / journald 维护，本程序只读，不该在界面里删除它。系统日志按 `--cron-log` → `/var/log/cron` → `syslog` → `messages` → `journalctl -u cron` 的顺序探测，读不到时界面会提示需要 root 权限或指定路径。

**未托管内容保护**：解析时把 crontab 拆成托管任务（标记行 + 命令行）与**其它一切**（注释、空行、`MAILTO=`/`SHELL=` 等环境变量、你手写的任务）。后者按原始字节保留、渲染时原样写回，界面底部的「未托管条目」区域会完整展示。

## 安全说明

- **令牌比较**用 `sha256` + `subtle.ConstantTimeCompare`，避免时序侧信道。
- **会话**只存内存（滑动过期 + 定时清理），不落盘。
- **登录限流**：同一来源 5 分钟内失败 10 次触发 429。限流**只惩罚猜错**——正确令牌永远放行，因此不会因为攻击者刷接口而把合法用户锁在门外。
- **CSRF 防护**：`SameSite=Strict` 会话 Cookie + 写入类请求校验 `Origin`/`Referer` 同源（非浏览器客户端不受影响）。
- **响应头**：CSP（`default-src 'self'`，禁止内联脚本）、`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`。
- **输入校验**：拒绝名称 / 周期 / 命令中的换行与控制字符——crontab 以行为单位，不校验就等于允许通过接口注入任意定时任务。
- **上限**：请求体 64KB、请求头 16KB，防止单请求撑爆内存。
- **进程权限**：程序以自身身份调用 `crontab`，不解析 shell 命令；「立即执行」通过 `/bin/sh -c` 拉起（与 cron 行为一致），请仅对可信用户开放。

> 反向代理后所有请求都来自 `127.0.0.1`，限流会退化为「全局失败计数」。由于正确令牌不受限流影响，这不影响正常使用，只是降低了暴力破解成本；需要精确的按 IP 限流请在 Nginx 层做（`limit_req_zone`）。

## 资源占用

| 指标 | 实测值 |
|---|---|
| Linux 二进制体积 | **6.13 MB**（`-trimpath -ldflags "-s -w"`，`CGO_ENABLED=0`） |
| 常驻内存 RSS（空闲 / 300 次 API 请求后） | **13.0 MB** / **14.7 MB** |
| 内存硬上限 | `debug.SetMemoryLimit(40MiB)` + `SetGCPercent(50)` |

内存之所以稳定，靠三件事：日志永远尾读不整读、请求体与请求头有上限、会话与限流表定时清理。

## 常见问题

- **任务日志是空的？** 任务还没到执行时间；或没勾选「捕获输出」；或日志目录不可写（看启动日志里的回退提示）。
- **系统 cron 日志显示「无法定位」？** `/var/log/cron`、`syslog` 通常需要 root 才能读。以 root 运行，或用 `--cron-log` 指定可读路径。
- **命令里的 `%` 报错或被截断？** cron 会把未转义的 `%` 转成换行，写成 `\%` 即可（界面上会给出黄色提醒）。
- **我在终端改了 crontab，界面没更新？** 每次操作都会重新读取 crontab，点一下「刷新」即可；未托管的改动不会被覆盖。
- **忘记令牌了？** 令牌不落盘、无法找回，重启服务时重新指定 `--token`。
- **端口被占用 / 权限不足？** `--port` 换端口；监听 1024 以下端口需要 root 或 `setcap cap_net_bind_service=+ep`。
- **能管理别的用户的 crontab 吗？** 可以，以 root 运行并加 `--user alice`。

## 开发

```
cmd/crontabui/          入口：参数解析、内存上限、信号处理、优雅关闭
internal/config/        CLI 参数校验、日志目录回退
internal/crontab/       表达式校验与中文描述、解析/渲染、读写存储与备份
internal/auth/          令牌校验、内存会话、登录限流
internal/logview/       日志尾读、目录列举、系统日志来源探测
internal/web/           路由、API 处理器
internal/web/static/    内嵌前端（登录页、管理台、CSS、JS）
deploy/                 install-corntab.sh、systemd、Nginx、logrotate 示例
```

```bash
make test   # go test -count=1 ./...
make vet    # go vet ./...
```

测试共 **133 个用例**（97 个顶层 + 子测试）：表达式校验与描述、解析/渲染往返一致性、存储读写与并发、注入防护、令牌与会话、登录限流、日志尾读边界、跨站请求拒绝、请求体上限、版本比较与产物选择、更新流程各失败分支等。全部不依赖真实 `crontab`（通过接口注入假实现），也不访问外网（更新相关测试用本地假 GitHub 服务）。

想验证真实的更新链路（查最新版 → 选产物 → 取校验和 → 真实下载 → SHA256 比对），可以跑这个默认跳过的用例：

```bash
CRONTABUI_LIVE_UPDATE_TEST=1 go test -run TestLiveGitHubProbe -v ./internal/update/
```

## 持续集成与发布

| 工作流 | 触发条件 | 做什么 |
|---|---|---|
| `ci.yml` | 推送 / PR 到 `main` | `go vet`、`go test`、构建校验 |
| `release.yml` | 推送 `v*` tag（或手动触发） | 跑测试 → 矩阵构建 4 个 Linux 架构 → 打包 → 生成 `checksums.txt` → 发布 Release |

发版只需打一个 tag，CI 会自动构建 `linux/amd64`、`linux/386`、`linux/arm64`、`linux/arm`（ARMv7）四个包并附到 Release 上：

```bash
git tag v1.0.4 && git push origin v1.0.4
```

含 `-` 的 tag（如 `v1.0.4-rc1`）会被标记为预发布；也可以到 Actions 页面手动触发 `Release` 并填写一个已存在的 tag。版本号通过 `-ldflags "-X main.version=<tag>"` 注入，可用 `crontabui --version` 核对。

## 非目标

不做 WebSocket 实时推送、多用户账号体系、任务依赖编排、容器内 cron 守护进程管理。保持小而可靠。
