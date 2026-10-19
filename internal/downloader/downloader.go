package downloader

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/azazo1/xestudy-downlaod/internal/hls"
	"github.com/azazo1/xestudy-downlaod/internal/naming"
)

// maxFetchBytes 是单次下载内容的大小上限, 防止异常地址拖垮内存.
const maxFetchBytes = 512 << 20

// Downloader 串行消费任务队列, 逐个下载完整视频.
//
// 串行是有意为之: 每个任务内部已经按分片并发下载, 同时跑多个视频会让
// 每个视频的并发数互相挤压, 也会让进度条无法对应到具体任务.
type Downloader struct {
	client   *http.Client
	progress io.Writer
	logger   *slog.Logger
}

// New 创建下载器, progress 为进度条输出目标, logger 为 nil 时使用全局 logger.
func New(progress io.Writer, logger *slog.Logger) *Downloader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Downloader{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost:   32,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
		progress: progress,
		logger:   logger,
	}
}

// Run 持续消费任务队列, 直到 context 结束或队列关闭.
func (d *Downloader) Run(ctx context.Context, jobs <-chan Job) {
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("下载协程退出")
			return
		case job, ok := <-jobs:
			if !ok {
				d.logger.Debug("任务队列已关闭, 下载协程退出")
				return
			}

			start := time.Now()
			if err := d.Process(ctx, job); err != nil {
				d.logger.Error("下载任务失败",
					"文件", job.FileName,
					"错误", err,
					"耗时", time.Since(start).Round(time.Millisecond),
				)
				continue
			}
			d.logger.Info("下载任务完成",
				"文件", job.FileName,
				"耗时", time.Since(start).Round(time.Second),
			)
		}
	}
}

// Process 下载一个视频的全部分片, 解密并拼接为完整视频.
func (d *Downloader) Process(ctx context.Context, job Job) error {
	logger := d.logger.With("文件", job.FileName)

	segmentsDir := filepath.Join(job.SavePath, "segments", naming.Stem(job.FileName))
	videosDir := filepath.Join(job.SavePath, "videos")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		return fmt.Errorf("创建分片目录失败: %w", err)
	}
	if err := os.MkdirAll(videosDir, 0o755); err != nil {
		return fmt.Errorf("创建视频目录失败: %w", err)
	}

	logger.Info("开始下载", "m3u8", job.URL, "保存目录", segmentsDir)

	raw, err := d.fetch(ctx, job.URL)
	if err != nil {
		return fmt.Errorf("下载 m3u8 失败: %w", err)
	}
	playlist, err := hls.Parse(raw)
	if err != nil {
		return err
	}

	base, err := url.Parse(job.URL)
	if err != nil {
		return fmt.Errorf("解析 m3u8 地址失败: %w", err)
	}
	logger.Info("解析播放列表完成", "分片数", len(playlist.Segments))

	keys := newKeyCache(d)
	if err := d.prefetchKeys(ctx, playlist, base, keys); err != nil {
		return err
	}

	failed, err := d.downloadSegments(ctx, job, playlist, base, segmentsDir, keys, logger)
	if err != nil {
		return err
	}
	if failed > 0 {
		// 保留已下载的分片, 重新跑一次相同任务即可续传, 因此这里不做拼接.
		return fmt.Errorf("%d 个分片下载失败, 已保留完成的分片, 重新触发该视频即可续传", failed)
	}

	output := filepath.Join(videosDir, job.FileName)
	logger.Info("开始拼接视频", "输出", output, "方式", concatenateMethod(job.UseFFmpeg))
	if job.UseFFmpeg {
		err = d.concatenateWithFFmpeg(ctx, segmentsDir, output)
	} else {
		err = d.concatenateSimple(ctx, segmentsDir, output)
	}
	if err != nil {
		return fmt.Errorf("拼接视频失败: %w", err)
	}

	logger.Info("视频已保存", "输出", output)
	return nil
}

// downloadSegments 并发下载全部分片, 返回失败数量.
func (d *Downloader) downloadSegments(
	ctx context.Context,
	job Job,
	playlist *hls.Playlist,
	base *url.URL,
	dir string,
	keys *keyCache,
	logger *slog.Logger,
) (int, error) {
	total := len(playlist.Segments)
	concurrency := job.Concurrent
	if concurrency <= 0 {
		concurrency = 1
	}

	bar := newBar(d.progress, total, "下载分片")
	defer bar.Finish()

	var (
		waitGroup sync.WaitGroup
		slots     = make(chan struct{}, concurrency)
		mutex     sync.Mutex
		failures  []error
		reused    int
	)

	for index, segment := range playlist.Segments {
		if err := ctx.Err(); err != nil {
			waitGroup.Wait()
			return len(failures), err
		}

		waitGroup.Add(1)
		go func(index int, segment hls.Segment) {
			defer waitGroup.Done()
			defer func() { _ = bar.Add(1) }()

			slots <- struct{}{}
			defer func() { <-slots }()

			target := filepath.Join(dir, fmt.Sprintf("%05d.ts", index))
			existed := isCompletedSegment(target)
			if err := d.downloadSegment(ctx, base, segment, target, keys); err != nil {
				mutex.Lock()
				failures = append(failures, fmt.Errorf("分片 %d: %w", index, err))
				mutex.Unlock()
				return
			}

			mutex.Lock()
			if existed {
				reused++
			}
			mutex.Unlock()
		}(index, segment)
	}
	waitGroup.Wait()

	if len(failures) > 0 {
		logger.Warn("部分分片下载失败", "失败数", len(failures), "首个错误", failures[0])
	} else {
		logger.Info("全部分片就绪", "分片数", total, "复用已下载", reused)
	}
	return len(failures), nil
}

// downloadSegment 下载单个分片, 必要时解密, 并原子地写入目标文件.
func (d *Downloader) downloadSegment(
	ctx context.Context,
	base *url.URL,
	segment hls.Segment,
	target string,
	keys *keyCache,
) error {
	if isCompletedSegment(target) {
		return nil
	}

	raw, err := d.fetch(ctx, resolveURL(base, segment.URI).String())
	if err != nil {
		return err
	}

	if segment.Encrypted() {
		key, err := keys.get(ctx, resolveURL(base, segment.KeyURI).String())
		if err != nil {
			return err
		}
		raw, err = DecryptAES128CBC(raw, key, segment.InitVector())
		if err != nil {
			return fmt.Errorf("解密失败: %w", err)
		}
	}

	// 先写临时文件再改名, 避免中断留下的半截文件在续传时被误认为已完成.
	temp := target + ".part"
	if err := os.WriteFile(temp, raw, 0o644); err != nil {
		return fmt.Errorf("写入分片失败: %w", err)
	}
	if err := os.Rename(temp, target); err != nil {
		return fmt.Errorf("保存分片失败: %w", err)
	}
	return nil
}

// prefetchKeys 预先下载播放列表中出现的所有密钥, 避免并发分片重复请求.
func (d *Downloader) prefetchKeys(ctx context.Context, playlist *hls.Playlist, base *url.URL, keys *keyCache) error {
	seen := make(map[string]struct{})
	for _, segment := range playlist.Segments {
		if !segment.Encrypted() {
			continue
		}
		keyURL := resolveURL(base, segment.KeyURI).String()
		if _, ok := seen[keyURL]; ok {
			continue
		}
		seen[keyURL] = struct{}{}
		if _, err := keys.get(ctx, keyURL); err != nil {
			return err
		}
	}
	if len(seen) > 0 {
		d.logger.Debug("解密密钥已就绪", "密钥数", len(seen))
	}
	return nil
}

// fetch 下载一个地址的全部内容.
func (d *Downloader) fetch(ctx context.Context, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}

	response, err := d.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("请求 %s 返回状态码 %d", address, response.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxFetchBytes))
	if err != nil {
		return nil, fmt.Errorf("读取响应内容失败: %w", err)
	}
	return raw, nil
}

// isCompletedSegment 判断分片是否已经下载完成, 用于断点续传.
func isCompletedSegment(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// resolveURL 把播放列表中的相对地址补全为绝对地址.
func resolveURL(base *url.URL, reference string) *url.URL {
	parsed, err := url.Parse(reference)
	if err != nil {
		return base
	}
	return base.ResolveReference(parsed)
}

// keyCache 缓存解密密钥, 避免同一个密钥被反复下载.
type keyCache struct {
	downloader *Downloader
	mutex      sync.Mutex
	items      map[string][]byte
}

// newKeyCache 创建密钥缓存.
func newKeyCache(d *Downloader) *keyCache {
	return &keyCache{downloader: d, items: make(map[string][]byte)}
}

// get 返回指定地址的密钥, 命中缓存时不再发起请求.
func (c *keyCache) get(ctx context.Context, keyURL string) ([]byte, error) {
	c.mutex.Lock()
	if key, ok := c.items[keyURL]; ok {
		c.mutex.Unlock()
		return key, nil
	}
	c.mutex.Unlock()

	key, err := c.downloader.fetch(ctx, keyURL)
	if err != nil {
		return nil, fmt.Errorf("下载解密密钥失败: %w", err)
	}
	if len(key) != 16 {
		return nil, fmt.Errorf("不支持的密钥长度 %d, 期望 16 字节", len(key))
	}

	c.mutex.Lock()
	c.items[keyURL] = key
	c.mutex.Unlock()
	return key, nil
}

// concatenateMethod 返回拼接方式的描述, 用于日志.
func concatenateMethod(useFFmpeg bool) string {
	if useFFmpeg {
		return "ffmpeg"
	}
	return "直接拼接"
}
