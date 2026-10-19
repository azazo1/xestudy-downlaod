package sniff

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const sampleVideoResponse = `{
  "code": 0,
  "msg": "",
  "data": {
    "0": {"file_name": "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4", "title": "Uart、DMA、ADC"},
    "video_info": {"src": "https://encrypt-k-vod.xet.tech/drm/v.f421220.m3u8?sign=abc&t=6972f47e"}
  }
}`

func TestAnalyzeFindsVideoInfoAndM3U8(t *testing.T) {
	verdict := Analyze(Entry{
		URL:          "https://app.xiaoe-tech.com/some_new_api?app_id=x",
		RequestBody:  `{"buz_data":{"video_id":"v_66defb75e4b023c0611f6047"}}`,
		ResponseBody: sampleVideoResponse,
	})

	if verdict.Host != "app.xiaoe-tech.com" || verdict.Path != "/some_new_api" {
		t.Fatalf("接口解析异常: %s%s", verdict.Host, verdict.Path)
	}
	if !verdict.HasVideoInfo {
		t.Fatal("应当识别出 video_info")
	}
	if len(verdict.M3U8URLs) != 1 || !strings.HasPrefix(verdict.M3U8URLs[0], "https://encrypt-k-vod.xet.tech/drm/v.f421220.m3u8") {
		t.Fatalf("m3u8 抽取异常: %v", verdict.M3U8URLs)
	}
	if !verdict.RequestHasVideoID {
		t.Fatal("请求体里带了非空 video_id, 应当识别出来")
	}
	if !verdict.Interesting() {
		t.Fatal("含 m3u8 的记录应当算候选")
	}
}

func TestAnalyzeIgnoresEmptyVideoIDAndPlainResponse(t *testing.T) {
	verdict := Analyze(Entry{
		URL:          "https://app.xiaoe-tech.com/query_video_info",
		RequestBody:  `{"buz_data":{"video_id":"","content_app_id":""}}`,
		ResponseBody: `{"code": 6, "msg": "未找到", "data": ""}`,
	})

	if verdict.RequestHasVideoID {
		t.Fatal("video_id 为空时不应算作命中")
	}
	if verdict.Interesting() {
		t.Fatal("普通响应不应算候选")
	}
}

func TestAnalyzeFindsVideoIDInQuery(t *testing.T) {
	verdict := Analyze(Entry{URL: "https://app.xiaoe-tech.com/query_video_info?video_id=v_123"})
	if !verdict.RequestHasVideoID {
		t.Fatal("video_id 出现在 query 里也应当识别出来")
	}
}

func TestRecorderMergesByUIDAndReports(t *testing.T) {
	recorder := NewRecorder()
	now := time.Now()

	recorder.Record(Entry{
		UID:         "1-2-3",
		Phase:       PhaseRequest,
		URL:         "https://app.xiaoe-tech.com/video_info_v2",
		Method:      "POST",
		RequestBody: `{"buz_data":{"video_id":"v_66defb75e4b023c0611f6047"}}`,
	}, now)
	recorder.Record(Entry{
		UID:          "1-2-3",
		Phase:        PhaseResponse,
		URL:          "https://app.xiaoe-tech.com/video_info_v2",
		Status:       200,
		ResponseBody: sampleVideoResponse,
	}, now)

	report := recorder.Report()
	if report.Total != 1 {
		t.Fatalf("同一 UID 应当合并为一条记录, 实际 %d 条", report.Total)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("候选记录数为 %d, 期望 1", len(report.Candidates))
	}

	candidate := report.Candidates[0]
	if candidate.Status != 200 || !candidate.Verdict.RequestHasVideoID || !candidate.Verdict.HasVideoInfo {
		t.Fatalf("合并后的记录不完整: %+v", candidate)
	}
	if !strings.Contains(candidate.RequestSnippet, "video_id") {
		t.Fatalf("请求体摘要缺失: %q", candidate.RequestSnippet)
	}

	if len(report.Endpoints) != 1 || report.Endpoints[0].M3U8Count != 1 || report.Endpoints[0].Count != 1 {
		t.Fatalf("接口画像异常: %+v", report.Endpoints)
	}
	if !strings.Contains(report.Hint, "https://app.xiaoe-tech.com/video_info_v2") {
		t.Fatalf("建议里应当给出该接口地址, 实际: %s", report.Hint)
	}

	if encoded, err := json.Marshal(report); err != nil || !strings.Contains(string(encoded), "m3u8") {
		t.Fatalf("报告无法序列化或内容异常: %v %s", err, encoded)
	}

	recorder.Reset()
	if report := recorder.Report(); report.Total != 0 || len(report.Endpoints) != 0 {
		t.Fatalf("清空后仍有余留: %+v", report)
	}
}

func TestRecorderHintWhenNothingFound(t *testing.T) {
	recorder := NewRecorder()
	recorder.Record(Entry{
		UID:          "9-9-9",
		Phase:        PhaseResponse,
		URL:          "https://app.xiaoe-tech.com/query_video_info",
		Status:       200,
		ResponseBody: `{"code": 6, "msg": "未找到", "data": ""}`,
	}, time.Now())

	report := recorder.Report()
	if len(report.Candidates) != 0 {
		t.Fatalf("不该有候选记录: %+v", report.Candidates)
	}
	if !strings.Contains(report.Hint, "还没有看到带 m3u8 的响应") {
		t.Fatalf("提示信息异常: %s", report.Hint)
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	text := strings.Repeat("中", 100)
	cut := truncate(text, 10)

	if !strings.HasSuffix(cut, "...[截断]") {
		t.Fatalf("截断标记缺失: %q", cut)
	}
	if strings.ContainsRune(cut, '\uFFFD') {
		t.Fatalf("截断产生了非法 UTF-8: %q", cut)
	}
}
