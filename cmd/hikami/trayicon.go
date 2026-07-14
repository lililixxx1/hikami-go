//go:build windows && systray

package main

import _ "embed"

// iconBytes 是托盘图标的 ICO 文件原始字节，编译时嵌入。
//go:embed trayicon.ico
var iconBytes []byte
