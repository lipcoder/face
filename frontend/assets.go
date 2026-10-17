package frontend

import "embed"

// Files 包含展示页与管理页的静态文件。
//
//go:embed display.html display.css display.js manager.html manager.css manager.js
var Files embed.FS
