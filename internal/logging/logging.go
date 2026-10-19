// Package logging 负责初始化全局日志.
//
// 日志统一使用标准库 log/slog, 输出到 stderr. 默认 info 级别,
// 打开 debug 后输出 debug 级别日志并附带代码位置, 便于定位下载流程中的问题.
package logging

import (
	"log/slog"
	"os"
)

// Setup 初始化全局 slog logger, debug 为 true 时输出更详细的日志.
func Setup(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     level,
		AddSource: debug,
	})
	slog.SetDefault(slog.New(handler))
}
