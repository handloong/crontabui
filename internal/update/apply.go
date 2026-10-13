package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

// elfExpect 描述某个 GOARCH 对应的 ELF 头部特征。
type elfExpect struct {
	class   byte
	machine uint16
}

// expectedELF 返回 GOARCH 对应的 ELF 位宽与机器码。
// 取值与 deploy/install-corntab.sh 里的校验逻辑保持一致。
func expectedELF(goarch string) (elfExpect, bool) {
	switch goarch {
	case "amd64":
		return elfExpect{class: 2, machine: 0x3e}, true
	case "386":
		return elfExpect{class: 1, machine: 0x03}, true
	case "arm64":
		return elfExpect{class: 2, machine: 0xb7}, true
	case "arm":
		return elfExpect{class: 1, machine: 0x28}, true
	default:
		return elfExpect{}, false
	}
}

// Applier 负责把新二进制替换到目标位置，并让服务用新版本重新跑起来。
type Applier struct {
	// ExecPath 是要替换的可执行文件路径，留空则使用 os.Executable()。
	ExecPath string
	// Logger 记录替换过程。
	Logger *slog.Logger
	// RestartFunc 覆盖重启实现，仅供测试使用。
	RestartFunc func() error
}

// TargetPath 返回最终会被替换的文件路径（已解析软链与相对路径）。
func (a *Applier) TargetPath() (string, error) {
	path := a.ExecPath
	if path == "" {
		p, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("无法定位当前可执行文件：%w", err)
		}
		path = p
	}
	// os.Executable 在 Linux 上通常已是真实路径，这里再兜一次软链
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("解析可执行文件绝对路径失败：%w", err)
		}
		path = abs
	}
	return path, nil
}

// NewStagingFile 在目标二进制所在目录下创建临时文件。
// 同目录能保证随后的 os.Rename 是同一文件系统内的原子操作。
func (a *Applier) NewStagingFile() (*os.File, error) {
	target, err := a.TargetPath()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".crontabui-update-*")
	if err != nil {
		return nil, fmt.Errorf("在 %s 下创建临时文件失败（该目录是否可写？）：%w", dir, err)
	}
	return f, nil
}

// Install 校验并把 src 处的可执行文件原子替换到目标路径。
// 返回旧版本的备份路径（未备份时为空字符串）。
func (a *Applier) Install(src string) (string, error) {
	target, err := a.TargetPath()
	if err != nil {
		return "", err
	}
	if err := checkELF(src, runtime.GOARCH); err != nil {
		return "", err
	}

	// 沿用旧文件的权限位，默认 0755
	mode := os.FileMode(0o755)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(src, mode); err != nil {
		return "", fmt.Errorf("设置可执行权限失败：%w", err)
	}

	// 备份旧版本，出问题可以手工回滚
	backup := ""
	if _, err := os.Stat(target); err == nil {
		backup = target + ".bak"
		if err := copyFile(target, backup, mode); err != nil {
			a.logf().Warn("备份旧版本失败（不影响本次更新）", "err", err)
			backup = ""
		}
	}

	if err := os.Rename(src, target); err != nil {
		return backup, fmt.Errorf("替换二进制失败：%w", err)
	}
	// 尽力把目录项刷盘，避免掉电后出现空文件
	if d, err := os.Open(filepath.Dir(target)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return backup, nil
}

// Restart 让服务以新二进制重新运行。
func (a *Applier) Restart() error {
	if a.RestartFunc != nil {
		return a.RestartFunc()
	}
	if runningUnderSystemd() {
		// systemd 单元配置了 Restart=always：给自己发 SIGTERM 优雅退出后会被自动拉起，
		// 新进程加载的就是刚替换好的二进制。
		// 这样做不需要 systemctl 权限，也不依赖子进程存活 —— systemd 重启时会把整个
		// cgroup 里的进程一起杀掉，靠派生后台子进程去执行 systemctl 反而不可靠。
		if err := terminateSelf(); err != nil {
			return fmt.Errorf("发送重启信号失败：%w", err)
		}
		return nil
	}
	return a.reexec()
}

// runningUnderSystemd 通过 INVOCATION_ID 判断进程是否由 systemd 拉起
// （systemd 会为每个服务进程注入该变量）。
func runningUnderSystemd() bool { return os.Getenv("INVOCATION_ID") != "" }

// terminateSelf 给自己发 SIGTERM，走 main 里既有的优雅关闭流程；
// 进程退出后由 systemd 的 Restart=always 重新拉起。
func terminateSelf() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// reexec 在非 systemd 环境下用新二进制重新拉起自身。
// 派发一个后台 sh，等在 1 秒（让父进程把 HTTP 响应发完、把监听端口释放掉）后 exec 新程序。
func (a *Applier) reexec() error {
	exe, err := a.TargetPath()
	if err != nil {
		return err
	}
	// sh -c 'sleep 1; exec "$0" "$@"' <exe> <原参数...>
	argv := append([]string{"-c", `sleep 1; exec "$0" "$@"`, exe}, os.Args[1:]...)
	cmd := exec.Command("sh", argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("重新拉起服务失败，请手动重启：%w", err)
	}
	a.logf().Info("已派发后台重启进程", "pid", cmd.Process.Pid)
	return nil
}

func (a *Applier) logf() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// checkELF 确认文件是 ELF 可执行文件，且架构与本机一致。
// 这一步能挡住「下错架构」「下载到的是错误页」这类会把服务搞坏的情况。
func checkELF(path, goarch string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开下载文件失败：%w", err)
	}
	defer f.Close()

	head := make([]byte, 20)
	if _, err := io.ReadFull(f, head); err != nil {
		return fmt.Errorf("读取文件头失败（下载可能不完整）：%w", err)
	}
	if !bytes.Equal(head[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return errors.New("下载的内容不是 ELF 可执行文件，已放弃更新")
	}

	want, ok := expectedELF(goarch)
	if !ok {
		return fmt.Errorf("不支持的平台 %s，已放弃更新", goarch)
	}
	if head[4] != want.class {
		return fmt.Errorf("下载的二进制位宽与本机不符（class=%#02x，期望 %#02x）", head[4], want.class)
	}
	if machine := binary.LittleEndian.Uint16(head[18:20]); machine != want.machine {
		return fmt.Errorf("下载的二进制架构与本机不符（machine=%#04x，期望 %#04x）", machine, want.machine)
	}
	// e_type：2=ET_EXEC，3=ET_DYN（PIE），其它都不是可执行文件
	if etype := binary.LittleEndian.Uint16(head[16:18]); etype != 2 && etype != 3 {
		return fmt.Errorf("下载的内容不是可执行文件（e_type=%d）", etype)
	}
	return nil
}

// copyFile 复制文件并设置权限。
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// sha256File 计算文件的 SHA256，返回小写十六进制。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
