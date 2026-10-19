package downloader

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"
)

// segmentFiles 列出分片目录中的 .ts 文件, 按文件名升序, 即分片播放顺序.
func segmentFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取分片目录失败: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".ts") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)

	if len(files) == 0 {
		return nil, fmt.Errorf("分片目录 %s 中没有 .ts 文件", dir)
	}
	return files, nil
}

// concatenateSimple 按顺序把分片字节流直接拼接到输出文件.
//
// 分片本身是连续的 MPEG-TS, 直接拼接即可播放, 也是原实现的默认方式.
func (d *Downloader) concatenateSimple(ctx context.Context, dir, output string) error {
	files, err := segmentFiles(dir)
	if err != nil {
		return err
	}

	target, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer target.Close()

	bar := newBar(d.progress, len(files), "拼接分片")
	defer bar.Finish()

	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}

		source, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("打开分片 %s 失败: %w", file, err)
		}
		_, copyErr := io.Copy(target, source)
		source.Close()
		if copyErr != nil {
			return fmt.Errorf("写入分片 %s 失败: %w", file, copyErr)
		}
		_ = bar.Add(1)
	}

	if err := target.Sync(); err != nil {
		return fmt.Errorf("刷新输出文件失败: %w", err)
	}
	return nil
}

// concatenateWithFFmpeg 生成 concat 列表后调用 ffmpeg 拼接, 适合需要重新封装容器的场景.
func (d *Downloader) concatenateWithFFmpeg(ctx context.Context, dir, output string) error {
	files, err := segmentFiles(dir)
	if err != nil {
		return err
	}

	lines := make([]string, 0, len(files))
	for _, file := range files {
		lines = append(lines, fmt.Sprintf("file '%s'", filepath.Base(file)))
	}
	listFile := filepath.Join(dir, "concat_list.txt")
	if err := os.WriteFile(listFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("写入 ffmpeg 列表文件失败: %w", err)
	}
	d.logger.Debug("已生成 ffmpeg 列表文件", "路径", listFile, "分片数", len(files))

	command := exec.CommandContext(ctx, "ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", listFile,
		"-c", "copy",
		"-y", output,
	)
	combined, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg 拼接失败: %w, 输出: %s", err, strings.TrimSpace(string(combined)))
	}
	return nil
}

// newBar 创建统一风格的进度条, writer 为 nil 时丢弃输出.
func newBar(writer io.Writer, total int, description string) *progressbar.ProgressBar {
	if writer == nil {
		writer = io.Discard
	}
	return progressbar.NewOptions(total,
		progressbar.OptionSetWriter(writer),
		progressbar.OptionSetDescription(description),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionSetElapsedTime(true),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionClearOnFinish(),
	)
}
