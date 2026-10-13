package logview

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// chunkSize 是反向读取的块大小。
	chunkSize = 32 << 10
	// defaultMaxBytes 是单次尾读允许读入的最大字节数，保证内存占用有界。
	defaultMaxBytes = 512 << 10
)

// File 描述日志目录中的一个文件。
type File struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Tail 返回文件末尾的若干行。无论文件多大，读入内存的数据都不会超过 maxBytes。
func Tail(path string, lines int, maxBytes int64) (string, error) {
	if lines <= 0 {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s 是目录，不是日志文件", path)
	}
	return tailReader(f, fi.Size(), lines, maxBytes)
}

// tailReader 从末尾按块反向读取，累积到行数足够或触达 maxBytes 上限即停止。
func tailReader(r io.ReaderAt, size int64, lines int, maxBytes int64) (string, error) {
	if lines <= 0 || size <= 0 {
		return "", nil
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}

	buf := make([]byte, chunkSize)
	nl := []byte("\n")
	var data []byte
	offset := size
	newlines := 0

	for offset > 0 {
		n := int64(chunkSize)
		if n > offset {
			n = offset
		}
		offset -= n

		read, err := r.ReadAt(buf[:n], offset)
		if err != nil && err != io.EOF {
			return "", err
		}
		if read <= 0 {
			break
		}
		part := buf[:read]
		newlines += bytes.Count(part, nl)

		merged := make([]byte, 0, len(part)+len(data))
		merged = append(merged, part...)
		merged = append(merged, data...)
		data = merged

		if newlines > lines {
			break
		}
		if int64(len(data)) >= maxBytes {
			break
		}
	}
	return lastLines(string(data), lines), nil
}

// lastLines 保留 s 末尾的 lines 行（含文件原有的结尾换行）。
func lastLines(s string, lines int) string {
	if lines <= 0 || s == "" {
		return ""
	}
	scan := len(s)
	if s[scan-1] == '\n' {
		scan--
	}
	seen := 0
	for i := scan - 1; i >= 0; i-- {
		if s[i] != '\n' {
			continue
		}
		seen++
		if seen == lines {
			return s[i+1:]
		}
	}
	return s
}

// ListFiles 列出目录中的日志文件（不含子目录），按修改时间倒序。
func ListFiles(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, File{
			Name:    e.Name(),
			Path:    filepath.Join(dir, e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})
	return files, nil
}
