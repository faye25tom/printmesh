package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var rawDistFS embed.FS

// DistFS 返回以 frontend/dist 为根目录的只读文件系统，供 Wails 与 Win7 本地 Web 控制台共用
func DistFS() fs.FS {
	sub, err := fs.Sub(rawDistFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
