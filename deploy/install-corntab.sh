#!/usr/bin/env bash
#
# CrontabUI 一键安装脚本 —— Debian 12 (bookworm) / systemd
#
# 用法：
#   sudo ./install-corntab.sh                     # 安装（默认 127.0.0.1:8080，管理 root 的 crontab）
#   sudo ./install-corntab.sh --dry-run           # 预演：只打印将要做的事，不改动系统
#   sudo ./install-corntab.sh --port 9090         # 换端口
#   sudo ./install-corntab.sh --user alice        # 管理 alice 的 crontab（服务仍以 root 运行）
#   sudo ./install-corntab.sh --token 你的令牌     # 指定令牌（不指定则自动生成）
#   sudo ./install-corntab.sh                     # 再次执行 = 升级，令牌与配置保持不变
#   sudo ./install-corntab.sh --status            # 查看状态
#   sudo ./install-corntab.sh --uninstall         # 卸载（保留日志与备份）
#   sudo ./install-corntab.sh --uninstall --purge # 卸载并删除日志与备份
#   ./install-corntab.sh --help                   # 查看帮助
#
set -euo pipefail

# ---------------------------------------------------------------------------
# 常量
# ---------------------------------------------------------------------------
APP_NAME="crontabui"
BIN_PATH="/usr/local/bin/${APP_NAME}"
ENV_DIR="/etc/crontabui"
ENV_FILE="${ENV_DIR}/${APP_NAME}.env"
UNIT_FILE="/etc/systemd/system/${APP_NAME}.service"
LOG_DIR="/var/log/crontabui"
DATA_DIR="/var/lib/crontabui"
SERVICE="${APP_NAME}"

# 本机架构探测结果（由 detect_arch 填充）。给默认值，避免 set -u 下引用未定义变量
HOST_GOARCH=""
HOST_ELF_MACHINE=""
HOST_ELF_CLASS=""

# ---------------------------------------------------------------------------
# 默认参数
# ---------------------------------------------------------------------------
PORT=8080
BIND_ADDR="127.0.0.1"
TARGET_USER=""          # --user：要管理哪个用户的 crontab
RUN_AS="root"           # --run-as：服务以哪个账号运行
TOKEN=""
BINARY=""
MEM_LIMIT=40
NO_START=0
ASSUME_YES=0
INSTALL_CRON=0
PURGE=0
DRY_RUN=0
ACTION="install"

# ---------------------------------------------------------------------------
# 输出 helpers
# ---------------------------------------------------------------------------
C_RESET='' C_RED='' C_GREEN='' C_YELLOW='' C_BLUE='' C_BOLD=''
if [ -t 1 ]; then
  C_RESET='\033[0m'; C_RED='\033[31m'; C_GREEN='\033[32m'
  C_YELLOW='\033[33m'; C_BLUE='\033[36m'; C_BOLD='\033[1m'
fi

info() { printf "${C_BLUE}==>${C_RESET} %s\n" "$*"; }
ok()   { printf "${C_GREEN}  ✓${C_RESET} %s\n" "$*"; }
warn() { printf "${C_YELLOW}  !${C_RESET} %s\n" "$*" >&2; }
die()  { printf "${C_RED}错误：${C_RESET}%s\n" "$*" >&2; exit 1; }

usage() {
  sed -n '3,16p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

# 在 dry-run 下只打印将要执行的命令
run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf "  ${C_YELLOW}[预演]${C_RESET} %s\n" "$*"
  else
    "$@"
  fi
}

# ---------------------------------------------------------------------------
# 参数解析
# ---------------------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    # 带取值的参数：先确认后面确实有值，避免 shift 越界导致静默退出
    --port|--bind|--user|--run-as|--token|--binary|--mem-limit)
      [ $# -ge 2 ] || die "参数 $1 缺少取值（用 --help 查看用法）"
      case "$1" in
        --port)      PORT="$2" ;;
        --bind)      BIND_ADDR="$2" ;;
        --user)      TARGET_USER="$2" ;;
        --run-as)    RUN_AS="$2" ;;
        --token)     TOKEN="$2" ;;
        --binary)    BINARY="$2" ;;
        --mem-limit) MEM_LIMIT="$2" ;;
      esac
      shift 2
      ;;
    --no-start)     NO_START=1; shift ;;
    --dry-run|-n)   DRY_RUN=1; shift ;;
    --yes|-y)       ASSUME_YES=1; shift ;;
    --install-cron) INSTALL_CRON=1; shift ;;
    --purge)        PURGE=1; shift ;;
    --uninstall)    ACTION="uninstall"; shift ;;
    --status)       ACTION="status"; shift ;;
    -h|--help)      usage ;;
    *) die "未知参数：$1（用 --help 查看用法）" ;;
  esac
done

# ---------------------------------------------------------------------------
# 检查
# ---------------------------------------------------------------------------
require_root() {
  [ "$(id -u)" -eq 0 ] || die "请用 root 运行：sudo $0 $*"
}

# 识别本机 CPU 架构，推导出期望的 ELF machine 码与位宽，供后面校验二进制是否匹配。
# 覆盖 Go 交叉编译的全部 Linux 目标：amd64 / 386 / arm64 / arm(GOARM=7)
# 结果写入三个全局变量：HOST_GOARCH、HOST_ELF_MACHINE、HOST_ELF_CLASS
detect_arch() {
  local m
  m=$(uname -m)
  case "$m" in
    x86_64|amd64)          HOST_GOARCH="amd64"; HOST_ELF_MACHINE="3e00"; HOST_ELF_CLASS="02" ;;
    i386|i486|i586|i686)   HOST_GOARCH="386";   HOST_ELF_MACHINE="0300"; HOST_ELF_CLASS="01" ;;
    aarch64|arm64)         HOST_GOARCH="arm64"; HOST_ELF_MACHINE="b700"; HOST_ELF_CLASS="02" ;;
    armv7l|armv7)          HOST_GOARCH="arm";   HOST_ELF_MACHINE="2800"; HOST_ELF_CLASS="01" ;;
    # ARMv6（树莓派 1 / Zero）能装但跑不了硬浮点 ARMv7 二进制，提前说清楚，别让人装完才发现
    armv6l|armv6)          HOST_GOARCH="arm";   HOST_ELF_MACHINE="2800"; HOST_ELF_CLASS="01"
                           warn "检测到 ARMv6。Release 只提供 ARMv7(GOARM=7) 二进制，可能报 Illegal instruction；若失败请用源码自行编译（GOARM=6）。" ;;
    *) die "不支持的架构：${m}（本脚本支持 x86_64 / i686 / aarch64 / armv7l）" ;;
  esac
  ok "系统架构：${m}（对应 Go 目标 linux/${HOST_GOARCH}）"
}

check_platform() {
  [ "$(uname -s)" = "Linux" ] || die "本脚本只支持 Linux（当前：$(uname -s)）"
  # 已识别过就不重复打印（do_install 在预演分支前已调用过一次）
  [ -n "${HOST_GOARCH:-}" ] || detect_arch
  [ -d /run/systemd/system ] || die "未检测到 systemd。若是容器环境，请手动用 nohup/supervisor 运行。"
  command -v systemctl >/dev/null 2>&1 || die "找不到 systemctl。"
  if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}" in
      debian|ubuntu|raspbian) ;;
      *) warn "当前系统为 ${PRETTY_NAME:-未知}，脚本按 Debian 12 编写，可能不适用。" ;;
    esac
  fi
}

# 校验二进制：必须是 ELF，且位宽与 machine 码都要与本机架构一致
check_binary_file() {
  local f="$1" magic class machine
  [ -f "$f" ] || die "找不到二进制文件：$f"
  [ -s "$f" ] || die "二进制文件为空：$f"
  magic=$(od -An -N4 -tx1 "$f" | tr -d ' \n')
  [ "$magic" = "7f454c46" ] || die "$f 不是 ELF 可执行文件（magic=$magic）。
请使用 Release 页面或 build.sh 产出的 linux-${HOST_GOARCH} 二进制。"
  class=$(od -An -j4 -N1 -tx1 "$f" | tr -d ' \n')
  [ "$class" = "$HOST_ELF_CLASS" ] || die "$f 位宽与本机不符（class=$class，本机应为 $HOST_ELF_CLASS；64 位=02，32 位=01）。
本机是 linux/${HOST_GOARCH}，请下载对应架构的包。"
  machine=$(od -An -j18 -N2 -tx1 "$f" | tr -d ' \n')
  [ "$machine" = "$HOST_ELF_MACHINE" ] || die "$f 架构与本机不符（machine=$machine，本机应为 $HOST_ELF_MACHINE）。
本机是 linux/${HOST_GOARCH}，请下载对应架构的包。"
}

# 定位要安装的二进制
locate_binary() {
  [ -n "$HOST_GOARCH" ] || detect_arch
  if [ -n "$BINARY" ]; then
    check_binary_file "$BINARY"
    printf '%s\n' "$BINARY"
    return
  fi
  local here c
  here=$(cd "$(dirname "$0")" && pwd)
  # 按本机架构依次尝试常见命名（ARMv7 产物带 v7 后缀，与 arm64 区分）
  for c in \
    "${here}/${APP_NAME}-linux-${HOST_GOARCH}" \
    "${here}/${APP_NAME}-linux-${HOST_GOARCH}v7" \
    "${here}/${APP_NAME}-linux-${HOST_GOARCH}-v7" \
    "${here}/${APP_NAME}" \
    "${here}/${APP_NAME}-linux-x86_64"; do
    if [ -f "$c" ]; then
      check_binary_file "$c"
      printf '%s\n' "$c"
      return
    fi
  done
  die "在 ${here} 里找不到适配本机（linux/${HOST_GOARCH}）的 ${APP_NAME} 二进制，请用 --binary <路径> 指定。"
}

# 检查/安装 cron（crontab 命令来自 Debian 的 cron 包）
ensure_cron() {
  if command -v crontab >/dev/null 2>&1; then
    ok "crontab 已存在：$(command -v crontab)"
    return
  fi
  warn "未检测到 crontab 命令（Debian 12 最小化安装默认不含 cron 包）。"
  if [ "$INSTALL_CRON" -eq 0 ] && [ "$ASSUME_YES" -eq 0 ]; then
    if [ -t 0 ]; then
      printf "  是否现在安装 cron 包？[y/N] "
      local answer
      read -r answer
      case "$answer" in
        y|Y|yes|YES) ;;
        *) die "已取消。请手动执行：apt-get update && apt-get install -y cron" ;;
      esac
    else
      die "非交互环境。请加 --install-cron 或 --yes，或先手动 apt-get install -y cron"
    fi
  fi
  info "安装 cron 包…"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq || die "apt-get update 失败，请检查网络与 /etc/apt/sources.list"
  apt-get install -y -qq cron || die "cron 安装失败，请手动执行：apt-get install -y cron"
  systemctl enable --now cron >/dev/null 2>&1 || warn "cron 服务启动失败，请检查：systemctl status cron"
  ok "cron 已安装并启动"
}

# ---------------------------------------------------------------------------
# 工具函数
# ---------------------------------------------------------------------------
gen_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  else
    head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# 读取已安装的令牌，用于重复执行时保持配置不变
existing_token() {
  [ -f "$ENV_FILE" ] || return 1
  local t
  t=$(sed -n 's/^[[:space:]]*CRONTABUI_TOKEN=[[:space:]]*//p' "$ENV_FILE" | head -n 1)
  t=${t%\"}; t=${t#\"}
  t=${t%\'}; t=${t#\'}
  [ -n "$t" ] && printf '%s' "$t"
}

# 健康检查：优先 curl，其次 wget，最后退回 /dev/tcp
wait_healthy() {
  local host="$BIND_ADDR"
  case "$host" in 0.0.0.0|"") host="127.0.0.1" ;; esac
  local url="http://${host}:${PORT}/healthz"
  local i
  for i in $(seq 1 30); do
    if command -v curl >/dev/null 2>&1; then
      curl -fsS --max-time 2 "$url" >/dev/null 2>&1 && return 0
    elif command -v wget >/dev/null 2>&1; then
      wget -q -T 2 -O /dev/null "$url" >/dev/null 2>&1 && return 0
    elif (exec 3<>"/dev/tcp/${host}/${PORT}") >/dev/null 2>&1; then
      exec 3>&- 2>/dev/null || true
      return 0
    fi
    sleep 1
  done
  return 1
}

# 生成 systemd 单元文件到指定路径
render_unit() {
  local dest="$1" user_arg=""
  [ -n "$TARGET_USER" ] && user_arg="--user ${TARGET_USER}"

  local memhigh=$(( MEM_LIMIT + 8 ))
  local memmax=$(( MEM_LIMIT + 24 ))

  cat > "$dest" <<'UNIT_EOF'
[Unit]
Description=CrontabUI - Linux crontab 可视化管理器
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=@ENV_FILE@
ExecStart=@BIN_PATH@ --token ${CRONTABUI_TOKEN} --bind @BIND@ --port @PORT@ --log-dir @LOG_DIR@ --data-dir @DATA_DIR@ --mem-limit @MEM@ @USER_ARG@

# 界面里的「自助更新」依赖这一项：替换二进制后程序给自己发 SIGTERM 退出，
# 由 systemd 重新拉起新版本。删掉 Restart=always 会导致更新后服务不再自动回来。
Restart=always
RestartSec=3

# 内存双保险：程序内 debug.SetMemoryLimit 兜底，这里再压一层硬上限
MemoryHigh=@MEMHIGH@M
MemoryMax=@MEMMAX@M

# 加固。
# 注意：这里刻意不使用 PrivateTmp= / PrivateDevices= / ProtectSystem=，
# 因为「立即执行」会拉起用户自己的命令，沙箱会改变其行为（例如写 /tmp、写 /etc）。
# 如果不需要「立即执行」，可以自行加上这三项获得更强隔离。
NoNewPrivileges=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
RestrictSUIDSGID=true
RestrictRealtime=true
RestrictNamespaces=true
LockPersonality=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
UNIT_EOF

  sed -i \
    -e "s|@ENV_FILE@|${ENV_FILE}|g" \
    -e "s|@BIN_PATH@|${BIN_PATH}|g" \
    -e "s|@BIND@|${BIND_ADDR}|g" \
    -e "s|@PORT@|${PORT}|g" \
    -e "s|@LOG_DIR@|${LOG_DIR}|g" \
    -e "s|@DATA_DIR@|${DATA_DIR}|g" \
    -e "s|@MEM@|${MEM_LIMIT}|g" \
    -e "s|@MEMHIGH@|${memhigh}|g" \
    -e "s|@MEMMAX@|${memmax}|g" \
    -e "s|@USER_ARG@|${user_arg}|g" \
    "$dest"

  # TARGET_USER 为空时 ExecStart 末尾会多一个空格，顺手清掉
  sed -i '/^ExecStart=/ s/[[:space:]]\+$//' "$dest"

  if [ "$RUN_AS" != "root" ]; then
    sed -i "/^\[Service\]/a User=${RUN_AS}\nGroup=$(id -gn "$RUN_AS" 2>/dev/null || echo "$RUN_AS")" "$dest"
  fi

  # 兜底：不应残留任何占位符
  if grep -q '@[A-Z_]*@' "$dest"; then
    die "单元文件仍有未替换的占位符：$(grep -o '@[A-Z_]*@' "$dest" | tr '\n' ' ')"
  fi
}

# ---------------------------------------------------------------------------
# 安装
# ---------------------------------------------------------------------------
do_install() {
  # 架构识别在预演模式下也要执行：locate_binary 依赖它判断二进制是否匹配本机
  detect_arch
  if [ "$DRY_RUN" -eq 1 ]; then
    info "预演模式：只打印将要执行的操作，不会改动系统"
  else
    require_root "$@"
    check_platform
  fi

  local src
  src=$(locate_binary)
  info "使用二进制：$src（$(du -h "$src" | cut -f1)）"

  case "$PORT" in
    ''|*[!0-9]*) die "--port 必须是数字" ;;
  esac
  [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "--port 必须在 1-65535 之间"
  case "$MEM_LIMIT" in
    ''|*[!0-9]*) die "--mem-limit 必须是非负整数（MiB）" ;;
  esac
  if [ "$BIND_ADDR" = "0.0.0.0" ]; then
    warn "正在监听 0.0.0.0：服务会直接暴露到网络。"
    warn "强烈建议改用默认的 127.0.0.1，再用 Nginx 反向代理提供 HTTPS。"
  fi
  case "$BIND_ADDR" in
    ''|*[[:space:]]*) die "--bind 取值无效：$BIND_ADDR" ;;
  esac

  if [ -n "$TARGET_USER" ] && [ "$DRY_RUN" -eq 0 ]; then
    id "$TARGET_USER" >/dev/null 2>&1 || die "用户 $TARGET_USER 不存在"
    warn "将以 root 身份执行「crontab -u ${TARGET_USER}」，请确认这是你的本意。"
  fi
  if [ "$DRY_RUN" -eq 0 ]; then
    id "$RUN_AS" >/dev/null 2>&1 || die "运行账号 $RUN_AS 不存在"
  fi

  # --- 令牌：已有则沿用，否则生成或使用传入值 ---
  local token_final="" reused=0
  if [ -n "$TOKEN" ]; then
    token_final="$TOKEN"
  elif token_final=$(existing_token); then
    reused=1
  else
    token_final=$(gen_token)
  fi
  [ "${#token_final}" -ge 8 ] || die "令牌至少需要 8 个字符"

  if [ "$DRY_RUN" -eq 0 ]; then
    ensure_cron

    info "安装二进制到 ${BIN_PATH}"
    install -m 0755 "$src" "${BIN_PATH}.new"
    mv -f "${BIN_PATH}.new" "$BIN_PATH"
    ok "二进制就绪"

    install -d -m 0700 "$ENV_DIR"
    install -d -m 0755 "$LOG_DIR"
    install -d -m 0700 "$DATA_DIR"
    if [ "$RUN_AS" != "root" ]; then
      chown -R "${RUN_AS}:$(id -gn "$RUN_AS")" "$LOG_DIR" "$DATA_DIR"
    fi

    umask 077
    cat > "$ENV_FILE" <<EOF
# CrontabUI 运行环境（由 install-corntab.sh 生成）
CRONTABUI_TOKEN=${token_final}
EOF
    chmod 0600 "$ENV_FILE"
    chown root:root "$ENV_FILE"
    umask 022

    if [ "$reused" -eq 1 ]; then
      ok "沿用已有令牌（未改动）"
    else
      ok "已写入新令牌到 ${ENV_FILE}"
    fi

    info "写入 systemd 单元 ${UNIT_FILE}"
    render_unit "$UNIT_FILE"
    chmod 0644 "$UNIT_FILE"
    systemctl daemon-reload
    ok "systemd 单元已加载"
  else
    local tmpunit
    tmpunit="$(mktemp "${TMPDIR:-/tmp}/crontabui-unit-XXXXXX.service")"
    render_unit "$tmpunit"
    info "将写入 ${UNIT_FILE}（内容如下）"
    echo "  ┌────────────────────────────────────────────────────────"
    sed 's/^/  │ /' "$tmpunit"
    echo "  └────────────────────────────────────────────────────────"
    rm -f "$tmpunit"

    info "将创建目录 ${ENV_DIR} ${LOG_DIR} ${DATA_DIR}"
    info "将写入 ${ENV_FILE}（权限 0600，含登录令牌）"
    info "将执行 systemctl daemon-reload && systemctl enable --now ${SERVICE}"
    echo
    ok "预演结束，未做任何改动。去掉 --dry-run 即可真正安装。"
    return
  fi

  if [ "$NO_START" -eq 1 ]; then
    info "已指定 --no-start，跳过启动"
    print_summary "$token_final" "未启动"
    return
  fi

  info "启动服务"
  systemctl enable "$SERVICE" >/dev/null 2>&1 || warn "设置开机自启失败"
  systemctl restart "$SERVICE"

  if wait_healthy; then
    ok "服务已就绪，健康检查通过"
    print_summary "$token_final" "运行中"
  else
    warn "健康检查未通过（30 秒内未响应）"
    echo
    echo "  最近日志："
    journalctl -u "$SERVICE" -n 30 --no-pager 2>/dev/null | sed 's/^/    /' || true
    echo
    die "安装未完成。请检查上面的日志，修复后执行：systemctl restart ${SERVICE}"
  fi
}

print_summary() {
  local token="$1" state="$2" url_host="$BIND_ADDR"
  case "$url_host" in 0.0.0.0|"") url_host="<服务器IP>" ;; esac

  echo
  printf "${C_GREEN}${C_BOLD}安装完成${C_RESET}\n"
  echo "  ─────────────────────────────────────────────"
  echo "  服务状态   ${state}（systemctl status ${SERVICE}）"
  echo "  访问地址   http://${url_host}:${PORT}/"
  echo "  登录令牌   ${C_BOLD}${token}${C_RESET}"
  echo "  日志目录   ${LOG_DIR}"
  echo "  备份目录   ${DATA_DIR}/backups"
  echo "  配置文件   ${ENV_FILE}"
  echo "  单元文件   ${UNIT_FILE}"
  echo "  ─────────────────────────────────────────────"
  echo "  常用命令："
  echo "    systemctl status  ${SERVICE}"
  echo "    systemctl restart ${SERVICE}"
  echo "    journalctl -u ${SERVICE} -f"
  echo "    $0 --status"
  echo "    sudo $0 --uninstall"
  echo
  if [ "$BIND_ADDR" = "0.0.0.0" ]; then
    printf "${C_YELLOW}  注意：当前监听 0.0.0.0，服务直接暴露在网络上。${C_RESET}\n"
    printf "${C_YELLOW}  强烈建议改回 127.0.0.1 并用 Nginx 反向代理提供 HTTPS。${C_RESET}\n\n"
  fi
}

# ---------------------------------------------------------------------------
# 卸载
# ---------------------------------------------------------------------------
do_uninstall() {
  require_root "$@"
  check_platform

  info "停止并禁用服务"
  systemctl disable --now "$SERVICE" >/dev/null 2>&1 || true

  if [ -f "$UNIT_FILE" ]; then
    rm -f "$UNIT_FILE"
    systemctl daemon-reload
    ok "已删除 ${UNIT_FILE}"
  fi

  if [ -f "$BIN_PATH" ]; then
    rm -f "$BIN_PATH"
    ok "已删除 ${BIN_PATH}"
  fi

  if [ -f "$ENV_FILE" ]; then
    rm -f "$ENV_FILE"
    rmdir "$ENV_DIR" 2>/dev/null || true
    ok "已删除 ${ENV_FILE}"
  fi

  if [ "$PURGE" -eq 1 ]; then
    warn "按 --purge 要求删除日志与备份：${LOG_DIR}、${DATA_DIR}"
    rm -rf "$LOG_DIR" "$DATA_DIR"
    ok "已清理数据"
  else
    echo "  已保留日志与备份：${LOG_DIR}、${DATA_DIR}"
    echo "  如需一并删除：sudo $0 --uninstall --purge"
  fi

  echo
  echo "  提醒：卸载不会改动系统 crontab，你通过界面创建的任务仍然有效。"
  echo "  如需清理，请手动执行 crontab -l 查看后 crontab -e 编辑。"
  echo
  ok "卸载完成"
}

# ---------------------------------------------------------------------------
# 状态
# ---------------------------------------------------------------------------
do_status() {
  echo
  printf "${C_BOLD}CrontabUI 状态${C_RESET}\n"
  echo "  ─────────────────────────────────────────────"

  if [ -f "$BIN_PATH" ]; then
    echo "  二进制     ${BIN_PATH}（$(du -h "$BIN_PATH" | cut -f1)）"
  else
    echo "  二进制     未安装"
  fi

  if [ -f "$UNIT_FILE" ]; then
    echo "  单元文件   ${UNIT_FILE}"
    printf "  开机自启   "
    systemctl is-enabled "$SERVICE" 2>/dev/null || echo "未启用"
    printf "  运行状态   "
    systemctl is-active "$SERVICE" 2>/dev/null || echo "未运行"
    local started
    started=$(systemctl show -p ActiveEnterTimestamp --value "$SERVICE" 2>/dev/null || true)
    [ -n "$started" ] && echo "  启动时间   ${started}"
    local mem
    mem=$(systemctl show -p MemoryCurrent --value "$SERVICE" 2>/dev/null || true)
    case "$mem" in
      ''|*[!0-9]*) ;;
      *) echo "  当前内存   $(( mem / 1024 / 1024 )) MB" ;;
    esac
    local port
    port=$(systemctl show -p ExecStart --value "$SERVICE" 2>/dev/null | grep -o -- '--port [0-9]*' | awk '{print $2}' || true)
    [ -n "$port" ] && echo "  监听端口   ${port}"
  else
    echo "  单元文件   未安装"
  fi

  echo "  ─────────────────────────────────────────────"
  local t
  if t=$(existing_token); then
    echo "  登录令牌   ${t}"
  fi
  echo
}

# ---------------------------------------------------------------------------
# 入口
# ---------------------------------------------------------------------------
case "$ACTION" in
  install)   do_install "$@" ;;
  uninstall) do_uninstall "$@" ;;
  status)    do_status ;;
esac
