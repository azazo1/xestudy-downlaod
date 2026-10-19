package server

import (
	"encoding/json"
	"net/http"
	"strconv"
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

// defaultSniffRecords 与 maxSniffRecords 限制一次导出的记录条数.
const (
	defaultSniffRecords = 50
	maxSniffRecords     = 500
)

// handleSniffRecords 输出完整记录, 便于直接查看某个接口的请求与响应正文.
//
// 支持 match (按 URL 子串过滤) 与 limit (条数) 两个查询参数.
func (s *Server) handleSniffRecords(writer http.ResponseWriter, request *http.Request) {
	if s.recorder == nil {
		s.writeResponse(writer, http.StatusNotFound, response{Code: 1, Msg: "未启用抓包诊断, 请带 --sniff 启动服务"})
		return
	}

	query := request.URL.Query()
	limit := defaultSniffRecords
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			s.writeResponse(writer, http.StatusBadRequest, response{Code: 1, Msg: "limit 必须是正整数"})
			return
		}
		limit = min(parsed, maxSniffRecords)
	}

	records := s.recorder.Records(query.Get("match"), limit, defaultSniffRecords)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"count": len(records), "records": records}); err != nil {
		s.logger.Debug("写入抓包记录失败", "错误", err)
	}
}
