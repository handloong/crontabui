package logview

import "os"

// Truncate 清空文件内容，但保留文件本身。
//
// 任务的输出重定向是 `cmd >> <logDir>/<id>.log 2>&1`，`>>` 以 O_APPEND 打开。
// 若改用 os.Remove 删除文件：
//   - 正在运行中的那次任务仍持有已删除文件的 fd，后续输出会全部丢失，
//     磁盘空间也要等进程退出才释放；
//   - 要等下一次触发才会重新创建文件，中间存在「文件不存在」的状态。
//
// 截断到 0 字节则 inode 不变，写入方完全无感知，语义上也正是用户想要的。
func Truncate(path string) error {
	return os.Truncate(path, 0)
}
