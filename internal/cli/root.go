// Package cli 定义命令行入口: 参数解析, 服务启动与优雅退出.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/azazo1/xestudy-downlaod/internal/buildinfo"
	"github.com/azazo1/xestudy-downlaod/internal/config"
	"github.com/azazo1/xestudy-downlaod/internal/downloader"
	"github.com/azazo1/xestudy-downlaod/internal/logging"
	"github.com/azazo1/xestudy-downlaod/internal/server"
)

// shutdownTimeout 是等待 HTTP 服务处理完在途请求的时间.
const shutdownTimeout = 10 * time.Second

// NewRootCommand 构建根命令.
func NewRootCommand() *cobra.Command {
	cfg := config.Config{
		Bind:       config.DefaultBind,
		SavePath:   config.DefaultSavePath(),
		Concurrent: config.DefaultConcurrent,
		QueueSize:  config.DefaultQueueSize,
	}

	command := &cobra.Command{
		Use:   "xestudy-downlaod",
		Short: "配合 reqable 使用的小鹅通视频下载服务",
		Long: `小鹅通视频下载服务.

服务启动后监听 HTTP 端口, 由 reqable 脚本把 query_video_info 接口的响应
POST 到 /upload-video-info, 服务解析出 m3u8 地址后下载分片, 解密并拼接成完整视频.

使用步骤:
  1. 启动本服务;
  2. 在 reqable 中启用脚本(Addon), 粘贴 assets/reqable/query-video-info.py, 匹配 query_video_info 接口;
  3. 在客户端里正常播放视频, reqable 抓到响应后会自动把下载任务提交给本服务.`,
		Version:      buildinfo.Version(),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cfg)
		},
	}

	// --version 只输出版本号本身, 便于发布流水线直接断言注入结果.
	command.SetVersionTemplate("{{.Version}}\n")

	flags := command.Flags()
	flags.StringVarP(&cfg.Bind, "bind", "b", cfg.Bind, "服务监听地址")
	flags.StringVarP(&cfg.SavePath, "savepath", "s", cfg.SavePath, "视频保存目录, 支持 ~ 开头")
	flags.IntVarP(&cfg.Concurrent, "concurrent", "c", cfg.Concurrent, "单个视频的分片下载并发数")
	flags.IntVar(&cfg.QueueSize, "queue-size", cfg.QueueSize, "等待下载的任务数量上限")
	flags.BoolVar(&cfg.FFmpeg, "ffmpeg", cfg.FFmpeg, "使用 ffmpeg 拼接分片, 默认直接拼接字节流")
	flags.BoolVar(&cfg.Debug, "debug", cfg.Debug, "输出 debug 级别日志")

	return command
}

// Execute 执行根命令.
func Execute() error {
	return NewRootCommand().Execute()
}

// run 启动下载服务, 并等待退出信号.
func run(ctx context.Context, cfg config.Config) error {
	logging.Setup(cfg.Debug)
	logger := slog.Default()

	if err := cfg.Validate(); err != nil {
		return err
	}

	savePath, err := cfg.ResolvedSavePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(savePath, 0o755); err != nil {
		return fmt.Errorf("创建保存目录 %s 失败: %w", savePath, err)
	}

	listener, err := net.Listen("tcp", cfg.Bind)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cfg.Bind, err)
	}
	defer listener.Close()

	logger.Info("下载服务启动",
		"监听地址", listener.Addr().String(),
		"保存目录", savePath,
		"分片并发", cfg.Concurrent,
		"拼接方式", concatenateMethod(cfg.FFmpeg),
		"队列长度", cfg.QueueSize,
	)

	// 下载协程使用独立的 context: 第一次 Ctrl+C 只停止接收新请求, 让在途任务跑完,
	// 第二次 Ctrl+C 才会取消下载.
	downloadCtx, cancelDownloads := context.WithCancel(context.Background())
	defer cancelDownloads()

	jobs := make(chan downloader.Job, cfg.QueueSize)
	worker := downloader.New(os.Stdout, logger)

	var workerGroup sync.WaitGroup
	workerGroup.Add(1)
	go func() {
		defer workerGroup.Done()
		worker.Run(downloadCtx, jobs)
	}()

	service := server.New(server.Options{
		SavePath:   savePath,
		Concurrent: cfg.Concurrent,
		FFmpeg:     cfg.FFmpeg,
	}, jobs, logger)

	httpServer := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("接口已就绪",
			"提交地址", fmt.Sprintf("http://%s/upload-video-info", listener.Addr().String()),
			"健康检查", fmt.Sprintf("http://%s/healthz", listener.Addr().String()),
		)
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
	}()

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case err := <-serveErrors:
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case <-ctx.Done():
		logger.Info("命令上下文结束, 开始退出")
	case <-signals:
		logger.Info("收到退出信号, 停止接收新任务, 等待队列中的任务完成", "提示", "再次按下 Ctrl+C 立即退出")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("关闭 HTTP 服务超时", "错误", err)
	}
	close(jobs)

	finished := make(chan struct{})
	go func() {
		workerGroup.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-signals:
		logger.Warn("再次收到退出信号, 取消进行中的下载")
		cancelDownloads()
		<-finished
	}

	logger.Info("下载服务已退出")
	return nil
}

// concatenateMethod 返回拼接方式描述, 用于启动日志.
func concatenateMethod(useFFmpeg bool) string {
	if useFFmpeg {
		return "ffmpeg"
	}
	return "直接拼接"
}
