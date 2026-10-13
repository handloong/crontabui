# CrontabUI

极简的 **Linux crontab 可视化管理器**。单个静态二进制、**零第三方依赖**、常驻内存 **约 15MB**。

浏览器里就能查看 / 新增 / 编辑 / 删除 cron 定时任务，启用停用、立即执行、在线查看任务日志与系统 cron 日志。

```
  CrontabUI v1.0.0  ·  Linux crontab 管理器
  ─────────────────────────────────────────────
  访问地址   http://127.0.0.1:8080/
  日志目录   /var/log/crontabui
  备份目录   /var/lib/crontabui/backups
  crontab    crontab
  内存上限   40 MiB
```

## 特性

| 特性 | 说明 |
|---|---|
| 任务管理 | 查看 / 新增 / 编辑 / 删除 / 一键启用停用 / 立即执行 |
| 表达式助手 | 5 字段写法与 `@reboot`、`@daily` 等宏，带**字段级中文错误**与**中文可读描述**（`*/5 * * * *` → 每 5 分钟） |
| 日志查看 | ① 每个任务的输出日志（在线尾读）② 系统 cron 日志（`/var/log/cron` → `syslog` → `journalctl` 自动探测） |
| 零侵入 | 未托管内容**逐字节原样保留**，绝不覆盖你手写的 crontab |
| 自动备份 | 每次写入前把旧 crontab 备份到 `<data-dir>/backups/`，保留最近 10 份 |
| 登录鉴权 | CLI `--token` 指定令牌，登录页只需填一个 Token；会话存内存、不落盘 |
| 安全加固 | 常量时间令牌比较、登录失败限流、跨站请求拒绝、CSP、`HttpOnly` + `SameSite=Strict` |
| 极致轻量 | 二进制约 **6.9MB**，RSS 约 **15MB**，`debug.SetMemoryLimit` 硬上限兜底 |
| 界面美观 | 原生 HTML/CSS/JS，无 CDN、可完全离线，深/浅色随系统自动切换 |

## 快速开始

### 1. 构建 Linux 可执行文件

```bash
# Linux / macOS / WSL
./build.sh

# Windows PowerShell（交叉编译，产出同样可直接部署到 Linux）
powershell -ExecutionPolicy Bypass -File .\build.ps1
```

产物为 `dist/crontabui-linux-amd64`（静态链接，无 CGO，不挑发行版）。

也可以直接用 make：

```bash
make linux-amd64     # 或 make linux-arm64 / make windows / make test
```

### 2. 部署并运行

```bash
scp dist/crontabui-linux-amd64 user@host:/usr/local/bin/crontabui
ssh user@host

sudo chmod +x /usr/local/bin/crontabui
sudo /usr/local/bin/crontabui --token '换成你自己的长随机令牌' --port 8080
```

打开 `http://<服务器IP>:8080/`，输入令牌即可登录。

> **强烈建议** 保持默认的 `--bind 127.0.0.1`，再通过 Nginx 反向代理加 HTTPS 对外提供服务（见下方部署章节）。直接暴露端口时程序会打印安全警告。

### 3. 验证

```bash
curl -s http://127.0.0.1:8080/healthz
# {"ok":true,"status":"healthy"}
```

## 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--token` | 无（**必填**） | 登录令牌，至少 8 位。也可用环境变量 `CRONTABUI_TOKEN` |
| `--bind` | `127.0.0.1` | 监听地址，只填地址不含端口 |
| `--port` | `8080` | 监听端口 |
| `--user` | 当前用户 | 管理指定用户的 crontab，跨用户需以 root 运行 |
| `--log-dir` | `/var/log/crontabui` | 任务日志目录，不可写时自动回退到 `~/.crontabui/logs` |
| `--data-dir` | `~/.crontabui` | 备份目录，实际写 `backups/` 子目录 |
| `--cron-log` | 自动探测 | 显式指定系统 cron 日志路径 |
| `--crontab-bin` | `crontab` | crontab 可执行文件路径 |
| `--mem-limit` | `40` | 内存上限（MiB），`0` 表示不限制 |
| `--session-ttl` | `12h` | 会话滑动有效期 |
| `--version` | - | 打印版本后退出 |

其他：`-h` 查看帮助。

## 部署

### 一键安装（Debian 12）

`dist/install-corntab.sh` 会完成全部工作：检查环境、安装 `cron` 包（若缺失）、生成随机令牌、写入 `0600` 环境文件、安装并加固 systemd 单元、开机自启、健康检查。

```bash
# 1) 从本地把二进制和安装脚本一起传上去
scp dist/crontabui-linux-amd64 dist/install-corntab.sh user@host:/tmp/

# 2) 在服务器上执行（安装前可先加 --dry-run 预演，不会改动系统）
ssh user@host
sudo /tmp/install-corntab.sh --dry-run --binary /tmp/crontabui-linux-amd64
sudo /tmp/install-corntab.sh --binary /tmp/crontabui-linux-amd64
```

安装完成会打印访问地址和登录令牌。常用选项：

```bash
sudo ./install-corntab.sh --port 9090             # 换端口
sudo ./install-corntab.sh --user alice            # 管理 alice 的 crontab（服务仍以 root 运行）
sudo ./install-corntab.sh --run-as www-data       # 让服务以非 root 账号运行
sudo ./install-corntab.sh --token '你的令牌'        # 指定令牌，不用随机生成
sudo ./install-corntab.sh --yes --install-cron    # 非交互（自动装 cron 包）
sudo ./install-corntab.sh                         # 重复执行 = 升级，令牌与配置保持不变
sudo ./install-corntab.sh --status                # 查看状态、端口、内存占用、令牌
sudo ./install-corntab.sh --uninstall             # 卸载（保留日志与备份）
sudo ./install-corntab.sh --uninstall --purge     # 卸载并删除日志与备份
```

脚本做过的安全检查：必须是 root、必须是 Linux x86_64、必须有 systemd、校验被安装文件确实是 **64 位 x86-64 ELF**、端口与内存上限范围校验、令牌长度校验、`--bind 0.0.0.0` 时打印暴露警告、生成的单元文件不允许残留任何未替换占位符。

> 安装脚本的**源文件**在 `deploy/install-corntab.sh`（受版本管理），构建时自动复制一份到 `dist/`。

### systemd

仓库同时提供手工部署用的加固单元文件，可直接使用：

```bash
sudo install -d -m 700 /etc/crontabui
printf 'CRONTABUI_TOKEN=%s\n' "$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
  | sudo tee /etc/crontabui/crontabui.env >/dev/null
sudo chmod 600 /etc/crontabui/crontabui.env
sudo cat /etc/crontabui/crontabui.env     # 记下令牌，登录时要用

sudo mkdir -p /var/log/crontabui /var/lib/crontabui
sudo cp deploy/crontabui.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now crontabui
sudo systemctl status crontabui
journalctl -u crontabui -f
```

### Nginx + HTTPS 反向代理

参考 `deploy/nginx.conf.example`。要点是不把 `--bind` 改成 `0.0.0.0`，让 Nginx 负责 TLS。

### 日志轮转

任务日志会持续增长，建议安装 `deploy/crontabui.logrotate`：

```bash
sudo cp deploy/crontabui.logrotate /etc/logrotate.d/crontabui
sudo logrotate -d /etc/logrotate.d/crontabui   # 演习，不实际执行
```

## 工作原理

### 任务如何写入 crontab

程序**直接读写系统 crontab**（`crontab -l` 读取、`crontab -` 覆盖写入），因此保存即生效。为了能稳定地识别、编辑和删除任务，每个托管任务会在其**上一行**写一条标记注释：

```crontab
MAILTO=root
0 1 * * * /usr/bin/foo.sh                        ← 你手写的任务，程序原样保留

#CRONTABUI: 5a31e73e 每日备份                      ← 托管标记（ID + 名称）
0 3 * * * /opt/backup.sh >> /var/log/crontabui/5a31e73e.log 2>&1
```

- **为什么用独立注释行而不是行尾注释**：Vixie cron 不支持行尾注释，`cmd # note` 会把 `# note` 当成命令的一部分执行。
- **停用任务** = 给命令行加 `#` 前缀（所有 cron 实现都支持），标记行保留，随时可恢复。
- **删除任务** = 只移除标记行 + 命令行，前后内容不动。
- **每次修改**都会重新执行 `crontab -l` 再合并，因此不会覆盖你在终端里做的编辑。
- **写入前自动备份**到 `<data-dir>/backups/crontab-<时间戳>.bak`，保留最近 10 份。

### 日志如何产生与查看

在任务编辑弹窗勾选「捕获输出」后，程序自动在命令末尾追加：

```
>> /var/log/crontabui/<任务ID>.log 2>&1
```

解析时会识别并剥离这段后缀，所以界面上看到的始终是你写的原始命令，不会重复叠加。

日志接口采用**反向分块读取**：从文件末尾按 32KB 块往回读，凑够行数或用满 512KB 就停。因此**日志文件多大都不影响内存占用**。

系统 cron 日志按以下顺序自动探测：`--cron-log` 指定路径 → `/var/log/cron` → `/var/log/syslog` → `/var/log/messages` → `journalctl -u cron`。读不到时界面会明确提示需要 root 权限或指定路径。

### 未托管内容保护

解析时把 crontab 拆成有序条目：托管任务（标记行 + 命令行）与**其它一切**（注释、空行、`MAILTO=`/`SHELL=` 等环境变量、你手写的任务）。后者按原始字节保留，只在渲染时原样写回。界面底部「未托管条目」区域会完整展示这些内容。

## 安全说明

- **令牌比较**用 `sha256` + `subtle.ConstantTimeCompare`，避免时序侧信道。
- **会话**只存内存（`sync.RWMutex` + 滑动过期 + 定时清理），不落盘、不写 Cookie 之外的地方。
- **登录限流**：同一来源 5 分钟内失败 10 次触发 429。限流**只惩罚猜错**——正确令牌永远放行，因此不会因为攻击者刷接口而把合法用户锁在门外（这对反向代理后所有请求同源的场景尤其重要）。
- **CSRF 防护**：`SameSite=Strict` 会话 Cookie + 写入类请求校验 `Origin`/`Referer` 同源（非浏览器客户端不受影响）。
- **响应头**：`CSP`（`default-src 'self'`，禁止内联脚本）、`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`。
- **输入校验**：拒绝名称/周期/命令中的换行与控制字符——crontab 以行为单位，不校验就等于允许通过接口注入任意定时任务。
- **请求体上限** 64KB、请求头上限 16KB，防止单请求撑爆内存。
- **进程权限**：程序以自身身份调用 `crontab`，不解析 shell 命令；「立即执行」通过 `/bin/sh -c` 拉起，与 cron 行为一致，请仅对可信用户开放。

### 反向代理后的来源 IP

登录限流按 `RemoteAddr` 统计。走 Nginx 反代时所有请求都来自 `127.0.0.1`，因此限流在代理场景下退化为「全局失败计数」。由于正确令牌不受限流影响，这只是降低了暴力破解的成本，不会影响正常使用。若需要精确的按 IP 限流，建议在 Nginx 层做（`limit_req_zone`）。

## 资源占用

| 指标 | 实测值 |
|---|---|
| Linux 二进制体积 | **6.88 MB**（`-trimpath -ldflags "-s -w"`，`CGO_ENABLED=0`） |
| 常驻内存 RSS（空闲） | **13.0 MB** |
| 常驻内存 RSS（300 次 API 请求后） | **14.7 MB**（峰值 14.84 MB） |
| 内存硬上限 | `debug.SetMemoryLimit(40MiB)` + `SetGCPercent(50)` |

内存之所以稳定，靠三件事：日志永远尾读不整读、请求体/请求头有上限、会话与限流表定时清理。

## 常见问题

**Q：任务日志是空的？**
任务还没到执行时间；或没勾选「捕获输出」；或日志目录不可写（看启动日志里的回退提示）。

**Q：系统 cron 日志显示“无法定位”？**
`/var/log/cron`、`syslog` 通常需要 root 才能读。以 root 运行，或用 `--cron-log` 指定可读路径。

**Q：命令里的 `%` 报错或被截断？**
cron 会把未转义的 `%` 转成换行。写成 `\%` 即可，界面上会给出黄色提醒。

**Q：我在终端改了 crontab，界面没更新？**
每次操作都会重新读取 crontab，点一下「刷新」即可。未托管的改动不会被覆盖。

**Q：忘记令牌了？**
重启服务时重新指定 `--token`（令牌不落盘，无法找回）。

**Q：端口被占用 / 权限不足？**
`--port` 换端口；监听 1024 以下端口需要 root 或 `setcap cap_net_bind_service=+ep`。

**Q：能管理别的用户的 crontab 吗？**
可以，以 root 运行并加 `--user alice`。

## 开发

```
cmd/crontabui/          程序入口：参数解析、内存上限、信号处理、优雅关闭
internal/config/        CLI 参数校验、日志目录回退
internal/crontab/       表达式校验与中文描述、解析/渲染、读写存储与备份
internal/auth/          令牌校验、内存会话、登录限流
internal/logview/       日志尾读、目录列举、系统日志来源探测
internal/web/           HTTP 路由、API 处理器
internal/web/static/    内嵌前端（登录页、管理台、CSS、JS）
deploy/                 install-corntab.sh（Debian 一键安装）、systemd、Nginx、logrotate 示例
```

```bash
make test     # go test -count=1 ./...
make vet      # go vet ./...
make fmt      # gofmt -s -w .

./build.sh                            # Linux/macOS/WSL 构建
powershell -File .\build.ps1          # Windows 构建
```

测试覆盖：表达式校验与描述、crontab 解析/渲染往返一致性、存储读写与并发、注入防护、令牌与会话、登录限流、日志尾读边界、跨站请求拒绝、请求体上限等，共 **99 个用例**（含子测试），全部不依赖真实 `crontab`（通过接口注入假实现）。

## 非目标

不做 WebSocket 实时推送、多用户账号体系、任务依赖编排、容器内 cron 守护进程管理。保持小而可靠。
