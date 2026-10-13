package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFiles embed.FS

// staticFS 是以 static/ 为根的只读文件系统，前端资源全部内嵌进二进制。
var staticFS = mustSub(staticFiles, "static")

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic("内嵌静态资源初始化失败: " + err.Error())
	}
	return sub
}

func readStatic(name string) ([]byte, error) {
	return fs.ReadFile(staticFS, name)
}
