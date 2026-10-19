// Package server 提供接收 reqable 抓包结果的 HTTP 服务.
//
// reqable 抓到小鹅通 query_video_info 的响应后, 由脚本把响应体 POST 到本服务,
// 服务只做解析与入队, 真正的下载交给 downloader 的协程完成.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/azazo1/xestudy-downlaod/internal/downloader"
	"github.com/azazo1/xestudy-downlaod/internal/model"
	"github.com/azazo1/xestudy-downlaod/internal/naming"
	"github.com/azazo1/xestudy-downlaod/internal/sniff"
)

// maxRequestBody 是请求体大小上限, 小鹅通的响应大约几十 KB.
const maxRequestBody = 8 << 20

// Options 是服务构建参数.
type Options struct {
	// SavePath 是保存根目录, 元数据写在其中的 meta 子目录.
	SavePath string
	// Concurrent 是分片下载并发数, 会写入每个下载任务.
	Concurrent int
	// FFmpeg 决定下载任务是否使用 ffmpeg 拼接.
	FFmpeg bool
	// Sniff 为 true 时额外开放抓包诊断接口, 用于定位视频信息来自哪个接口.
	Sniff bool
}

// Server 把收到的抓包数据转换成下载任务.
type Server struct {
	options  Options
	jobs     chan<- downloader.Job
	logger   *slog.Logger
	recorder *sniff.Recorder
}

// response 是与 rust 版一致的响应体.
type response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// New 创建 Server, jobs 是下载任务队列.
func New(options Options, jobs chan<- downloader.Job, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	instance := &Server{options: options, jobs: jobs, logger: logger}
	if options.Sniff {
		instance.recorder = sniff.NewRecorder()
	}
	return instance
}

// Handler 返回注册好路由的 http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload-video-info", s.handleUploadVideoInfo)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/sniff", s.handleSniff)
	mux.HandleFunc("/sniff/report", s.handleSniffReport)
	mux.HandleFunc("/sniff/records", s.handleSniffRecords)
	return mux
}

// handleUploadVideoInfo 接收 reqable 脚本回传的视频信息并加入下载队列.
func (s *Server) handleUploadVideoInfo(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		s.writeResponse(writer, http.StatusMethodNotAllowed, response{Code: 1, Msg: "只支持 POST"})
		return
	}

	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxRequestBody))
	var payload model.UploadVideoRequest
	if err := decoder.Decode(&payload); err != nil {
		s.logger.Warn("解析请求体失败", "来源", request.RemoteAddr, "错误", err)
		s.writeResponse(writer, http.StatusBadRequest, response{Code: 1, Msg: "请求体不是合法的 JSON"})
		return
	}
	if err := payload.Validate(); err != nil {
		s.logger.Warn("请求缺少必要字段", "来源", request.RemoteAddr, "错误", err)
		s.writeResponse(writer, http.StatusBadRequest, response{Code: 1, Msg: err.Error()})
		return
	}

	fileName := naming.BuildVideoFileName(payload.Data.Content.FileName, payload.Data.Content.Title)
	s.logger.Info("收到下载请求",
		"文件", fileName,
		"m3u8", payload.Data.VideoInfo.Src,
		"来源", request.RemoteAddr,
	)

	if err := s.saveMeta(fileName, payload); err != nil {
		// 元数据只用于回溯, 保存失败不影响下载.
		s.logger.Error("保存请求元数据失败", "文件", fileName, "错误", err)
	}

	job := downloader.Job{
		URL:        payload.Data.VideoInfo.Src,
		FileName:   fileName,
		SavePath:   s.options.SavePath,
		Concurrent: s.options.Concurrent,
		UseFFmpeg:  s.options.FFmpeg,
	}

	select {
	case s.jobs <- job:
		s.writeResponse(writer, http.StatusOK, response{Code: 0, Msg: "Added to queue"})
	case <-request.Context().Done():
		s.logger.Warn("任务入队前客户端已断开", "文件", fileName)
	}
}

// handleHealth 提供服务存活探测, 便于确认 reqable 脚本要提交的地址是否可达.
func (s *Server) handleHealth(writer http.ResponseWriter, request *http.Request) {
	s.writeResponse(writer, http.StatusOK, response{Code: 0, Msg: "ok"})
}

// saveMeta 把请求内容原样保存到 {SavePath}/meta/{文件名主干}.json.
func (s *Server) saveMeta(fileName string, payload model.UploadVideoRequest) error {
	metaDir := filepath.Join(s.options.SavePath, "meta")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return fmt.Errorf("创建元数据目录失败: %w", err)
	}

	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	target := filepath.Join(metaDir, naming.Stem(fileName)+".json")
	if err := os.WriteFile(target, append(content, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入元数据失败: %w", err)
	}
	return nil
}

// writeResponse 输出 JSON 响应.
func (s *Server) writeResponse(writer http.ResponseWriter, status int, body response) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)

	if err := json.NewEncoder(writer).Encode(body); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		s.logger.Debug("写入响应失败", "错误", err)
	}
}
