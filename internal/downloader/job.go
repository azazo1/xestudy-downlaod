// Package downloader 负责下载 m3u8 分片, 解密并拼接为完整视频.
package downloader

// Job 描述一次视频下载任务.
type Job struct {
	// URL 是 m3u8 播放列表地址.
	URL string
	// FileName 是输出文件名, 已由上层净化.
	FileName string
	// SavePath 是保存根目录, 分片与成片都放在其下.
	SavePath string
	// Concurrent 是本任务的分片下载并发数.
	Concurrent int
	// UseFFmpeg 为 true 时使用 ffmpeg 拼接, 否则直接拼接字节流.
	UseFFmpeg bool
}
