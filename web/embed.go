// Package web 负责把 Vite 构建产物嵌进 Go 二进制。
package web

import (
	"embed"
	"io/fs"
)

// go:embed 是编译器指令，不能在“//”和“go:embed”之间插入空格。
// all:dist 会把 dist 下包括点文件在内的内容编译进 assets。
//
//go:embed all:dist
var assets embed.FS

// DistFS 把嵌入文件系统的根目录从 dist 下沉一层，使调用方可直接读取 index.html。
func DistFS() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}
