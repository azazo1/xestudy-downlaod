package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/azazo1/xestudy-downlaod/internal/sniff"
)

// maxSniffRequest 是单条诊断记录的大小上限.
const maxSniffRequest = 512 << 10

// handleSniff 接收抓包脚本回传的请求与响应记录, 并就地分析.
//
// DELETE 用于清空记录, 便于按一次干净的抓包会话重新分析.
func (s *Server) handleSniff(writer http.ResponseWriter, request *http.Request) {
	if s.recorder == nil {
		s.writeResponse(writer, http.StatusNotFound, response{Code: 1, Msg: "未启用抓包诊断, 请带 --sniff 启动服务"})
		return
	}

	if request.Method == http.MethodDelete {
		s.recorder.Reset()
		s.logger.Info("已清空抓包诊断记录")
		s.writeResponse(writer, http.StatusOK, response{Code: 0, Msg: "已清空"})
		return
	}

	if request.Method != http.MethodPost {
		s.writeResponse(writer, http.StatusMethodNotAllowed, response{Code: 1, Msg: "只支持 POST 与 DELETE"})
		return
	}

	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxSniffRequest))
	var entry sniff.Entry
	if err := decoder.Decode(&entry); err != nil {
		s.logger.Warn("解析抓包记录失败", "来源", request.RemoteAddr, "错误", err)
		s.writeResponse(writer, http.StatusBadRequest, response{Code: 1, Msg: "请求体不是合法的 JSON"})
		return
	}
	if entry.URL == "" {
		s.writeResponse(writer, http.StatusBadRequest, response{Code: 1, Msg: "缺少 url"})
		return
	}

	verdict := s.recorder.Record(entry, time.Now())
	if verdict.Interesting() {
		attributes := []any{
			"接口", verdict.Host + verdict.Path,
			"含视频信息", verdict.HasVideoInfo,
			"m3u8 数量", len(verdict.M3U8URLs),
			"请求带 video_id", verdict.RequestHasVideoID,
		}
		if len(verdict.M3U8URLs) > 0 {
			attributes = append(attributes, "首个 m3u8", verdict.M3U8URLs[0])
		}
		s.logger.Info("发现候选接口", attributes...)
	} else {
		s.logger.Debug("收到抓包记录", "接口", verdict.Host+verdict.Path, "阶段", entry.Phase, "状态码", entry.Status)
	}

	s.writeResponse(writer, http.StatusOK, response{Code: 0, Msg: "recorded"})
}

// handleSniffReport 输出当前的诊断报告.
func (s *Server) handleSniffReport(writer http.ResponseWriter, request *http.Request) {
	if s.recorder == nil {
		s.writeResponse(writer, http.StatusNotFound, response{Code: 1, Msg: "未启用抓包诊断, 请带 --sniff 启动服务"})
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.recorder.Report()); err != nil {
		s.logger.Debug("写入诊断报告失败", "错误", err)
	}
}
