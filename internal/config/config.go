// Package config 定义运行参数, 默认值与参数校验.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultBind 是下载服务默认监听地址, 与 reqable 脚本中的地址保持一致.
	DefaultBind = "0.0.0.0:8010"
	// DefaultConcurrent 是分片下载默认并发数.
	DefaultConcurrent = 12
	// DefaultQueueSize 是待下载任务队列长度.
	DefaultQueueSize = 100
)

// Config 保存一次运行用到的全部配置.
type Config struct {
	// Bind 是 HTTP 服务监听地址.
	Bind string
	// SavePath 是视频保存根目录, 支持 ~ 开头的路径.
	SavePath string
	// Concurrent 是单个视频的分片下载并发数.
	Concurrent int
	// QueueSize 是等待下载的任务数量上限.
	QueueSize int
	// FFmpeg 为 true 时使用 ffmpeg 拼接分片, 否则直接拼接字节流.
	FFmpeg bool
	// Debug 为 true 时输出 debug 级别日志.
	Debug bool
}

// Validate 检查配置是否可用.
func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.Bind); err != nil {
		return fmt.Errorf("监听地址 %q 无效: %w", c.Bind, err)
	}
	if c.Concurrent <= 0 {
		return fmt.Errorf("分片并发数必须为正整数, 当前为 %d", c.Concurrent)
	}
	if c.QueueSize <= 0 {
		return fmt.Errorf("队列长度必须为正整数, 当前为 %d", c.QueueSize)
	}
	if strings.TrimSpace(c.SavePath) == "" {
		return fmt.Errorf("保存目录不能为空")
	}
	return nil
}

// ResolvedSavePath 返回展开 ~ 之后的保存目录绝对路径.
func (c Config) ResolvedSavePath() (string, error) {
	path, err := expandHome(c.SavePath)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("无法解析保存目录 %q: %w", c.SavePath, err)
	}
	return abs, nil
}

// DefaultSavePath 返回默认保存目录, 即用户下载目录, 取不到时退回 ./downloads.
func DefaultSavePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./downloads"
	}
	downloads := filepath.Join(home, "Downloads")
	if info, err := os.Stat(downloads); err == nil && info.IsDir() {
		return downloads
	}
	return "./downloads"
}

// expandHome 把 ~ 与 ~/xxx 展开为用户主目录.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法解析用户主目录: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}
