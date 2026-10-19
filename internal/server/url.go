package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/azazo1/xestudy-downlaod/internal/downloader"
	"github.com/azazo1/xestudy-downlaod/internal/model"
	"github.com/azazo1/xestudy-downlaod/internal/naming"
)

// fallbackNameLayout 是未提供文件名时使用的时间戳格式.
const fallbackNameLayout = "20060102-150405"

// handleUploadVideoURL 接收抓包脚本抽出的 m3u8 地址并加入下载队列.
//
// 小鹅通客户端换接口后, 视频地址可能出现在结构完全不同的响应里, 因此这里提供一条
// 不依赖响应结构的通道: 脚本只管把地址交过来, 文件名由脚本或服务端补上.
func (s *Server) handleUploadVideoURL(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		s.writeResponse(writer, http.StatusMethodNotAllowed, response{Code: 1, Msg: "只支持 POST"})
		return
	}

	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxRequestBody))
	var payload model.UploadVideoURLRequest
	if err := decoder.Decode(&payload); err != nil {
		s.logger.Warn("解析请求体失败", "来源", request.RemoteAddr, "错误", err)
		s.writeResponse(writer, http.StatusBadRequest, response{Code: 1, Msg: "请求体不是合法的 JSON"})
		return
	}
	if err := payload.Validate(); err != nil {
		s.logger.Warn("视频地址不可用", "来源", request.RemoteAddr, "错误", err)
		s.writeResponse(writer, http.StatusBadRequest, response{Code: 1, Msg: err.Error()})
		return
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = fmt.Sprintf("video-%s.mp4", time.Now().Format(fallbackNameLayout))
	}
	fileName := naming.BuildVideoFileName(name, "")

	s.logger.Info("收到下载请求",
		"文件", fileName,
		"m3u8", payload.URL,
		"来源", request.RemoteAddr,
	)

	if err := s.saveMeta(fileName, payload); err != nil {
		s.logger.Error("保存请求元数据失败", "文件", fileName, "错误", err)
	}

	job := downloader.Job{
		URL:        strings.TrimSpace(payload.URL),
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
