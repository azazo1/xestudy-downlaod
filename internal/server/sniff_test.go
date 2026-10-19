package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azazo1/xestudy-downlaod/internal/downloader"
	"github.com/azazo1/xestudy-downlaod/internal/sniff"
)

func TestSniffRecordsAndReports(t *testing.T) {
	jobs := make(chan downloader.Job, 1)
	instance := New(Options{SavePath: t.TempDir(), Concurrent: 4, Sniff: true}, jobs, quietLogger())

	post := func(entry sniff.Entry) *httptest.ResponseRecorder {
		body, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("构造请求失败: %v", err)
		}
		recorder := httptest.NewRecorder()
		instance.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/sniff", strings.NewReader(string(body))))
		return recorder
	}

	requestRecord := post(sniff.Entry{
		UID:         "1-2-3",
		Phase:       sniff.PhaseRequest,
		URL:         "https://app.xiaoe-tech.com/video_info_v2?app_id=x",
		Method:      http.MethodPost,
		RequestBody: `{"buz_data":{"video_id":"v_66defb75e4b023c0611f6047"}}`,
	})
	if requestRecord.Code != http.StatusOK {
		t.Fatalf("记录请求阶段失败: %d %s", requestRecord.Code, requestRecord.Body.String())
	}

	responseRecord := post(sniff.Entry{
		UID:          "1-2-3",
		Phase:        sniff.PhaseResponse,
		URL:          "https://app.xiaoe-tech.com/video_info_v2?app_id=x",
		Status:       http.StatusOK,
		ResponseBody: `{"code":0,"data":{"video_info":{"src":"https://encrypt-k-vod.xet.tech/drm/v.f421220.m3u8?sign=abc"}}}`,
	})
	if responseRecord.Code != http.StatusOK {
		t.Fatalf("记录响应阶段失败: %d %s", responseRecord.Code, responseRecord.Body.String())
	}

	// 非法请求要被拒绝, 且不污染记录.
	if code := post(sniff.Entry{Phase: sniff.PhaseRequest}).Code; code != http.StatusBadRequest {
		t.Fatalf("缺少 url 时状态码为 %d, 期望 400", code)
	}
	badJSON := httptest.NewRecorder()
	instance.Handler().ServeHTTP(badJSON, httptest.NewRequest(http.MethodPost, "/sniff", strings.NewReader("{")))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 时状态码为 %d, 期望 400", badJSON.Code)
	}

	var report sniff.Report
	reportRecorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(reportRecorder, httptest.NewRequest(http.MethodGet, "/sniff/report", nil))
	if reportRecorder.Code != http.StatusOK {
		t.Fatalf("读取报告失败: %d", reportRecorder.Code)
	}
	if err := json.Unmarshal(reportRecorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("解析报告失败: %v", err)
	}

	if report.Total != 1 || len(report.Candidates) != 1 {
		t.Fatalf("报告内容异常: total=%d candidates=%d", report.Total, len(report.Candidates))
	}
	candidate := report.Candidates[0]
	if !candidate.Verdict.HasVideoInfo || len(candidate.Verdict.M3U8URLs) != 1 || !candidate.Verdict.RequestHasVideoID {
		t.Fatalf("候选记录分析不完整: %+v", candidate)
	}
	if !strings.Contains(report.Hint, "video_info_v2") {
		t.Fatalf("报告建议异常: %s", report.Hint)
	}

	deleteRecorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/sniff", nil))
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("清空记录失败: %d", deleteRecorder.Code)
	}

	reportRecorder = httptest.NewRecorder()
	instance.Handler().ServeHTTP(reportRecorder, httptest.NewRequest(http.MethodGet, "/sniff/report", nil))
	if err := json.Unmarshal(reportRecorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("解析报告失败: %v", err)
	}
	if report.Total != 0 {
		t.Fatalf("清空后仍有 %d 条记录", report.Total)
	}
}

func TestSniffRecordsEndpoint(t *testing.T) {
	instance := New(Options{SavePath: t.TempDir(), Concurrent: 4, Sniff: true}, make(chan downloader.Job, 1), quietLogger())

	body, err := json.Marshal(sniff.Entry{
		UID:          "detail",
		Phase:        sniff.PhaseResponse,
		URL:          "https://xestudy.xiaoeknow.com/xe.course.business.video.detail_info.get/2.0.0",
		Status:       http.StatusOK,
		ResponseBody: `{"data":{"video_info":{"src":"https:\/\/encrypt-k-vod.xet.tech\/drm\/v.f421220.m3u8?sign=abc"}}}`,
	})
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	postRecorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(postRecorder, httptest.NewRequest(http.MethodPost, "/sniff", strings.NewReader(string(body))))
	if postRecorder.Code != http.StatusOK {
		t.Fatalf("记录失败: %d", postRecorder.Code)
	}

	recorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sniff/records?match=detail_info", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("导出记录失败: %d", recorder.Code)
	}

	var payload struct {
		Count   int                `json:"count"`
		Records []sniff.RecordDump `json:"records"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析记录失败: %v", err)
	}
	if payload.Count != 1 || len(payload.Records) != 1 {
		t.Fatalf("导出条数异常: %+v", payload)
	}
	if len(payload.Records[0].Verdict.M3U8URLs) != 1 {
		t.Fatalf("转义的 m3u8 未被识别: %+v", payload.Records[0].Verdict)
	}

	badLimit := httptest.NewRecorder()
	instance.Handler().ServeHTTP(badLimit, httptest.NewRequest(http.MethodGet, "/sniff/records?limit=abc", nil))
	if badLimit.Code != http.StatusBadRequest {
		t.Fatalf("非法 limit 时状态码为 %d, 期望 400", badLimit.Code)
	}
}

func TestSniffDisabledByDefault(t *testing.T) {
	instance := New(Options{SavePath: t.TempDir(), Concurrent: 4}, make(chan downloader.Job, 1), quietLogger())

	recorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sniff/report", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("未开启诊断时状态码为 %d, 期望 404", recorder.Code)
	}
}
