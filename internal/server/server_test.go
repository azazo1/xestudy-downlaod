package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azazo1/xestudy-downlaod/internal/downloader"
)

const validPayload = `{
  "code": 0,
  "msg": "",
  "data": {
    "0": {
      "file_name": "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4",
      "title": "Uart、DMA、ADC"
    },
    "video_info": {
      "src": "https://encrypt-k-vod.xet.tech/drm/v.f421220.m3u8?sign=abc&t=6972f47e"
    }
  }
}`

func TestUploadVideoInfoEnqueuesJob(t *testing.T) {
	savePath := t.TempDir()
	jobs := make(chan downloader.Job, 4)
	instance := New(Options{SavePath: savePath, Concurrent: 8, FFmpeg: true}, jobs, quietLogger())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/upload-video-info", strings.NewReader(validPayload))
	instance.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码为 %d, 期望 200, 响应体: %s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Code != 0 || body.Msg != "Added to queue" {
		t.Fatalf("响应内容异常: %+v", body)
	}

	var job downloader.Job
	select {
	case job = <-jobs:
	default:
		t.Fatal("任务没有进入队列")
	}

	wantName := "蓝桥杯嵌入式第三讲——Uart、DMA、ADC-Uart、DMA、ADC.mp4"
	if job.FileName != wantName {
		t.Fatalf("文件名为 %q, 期望 %q", job.FileName, wantName)
	}
	if job.URL != "https://encrypt-k-vod.xet.tech/drm/v.f421220.m3u8?sign=abc&t=6972f47e" {
		t.Fatalf("m3u8 地址为 %q", job.URL)
	}
	if job.SavePath != savePath || job.Concurrent != 8 || !job.UseFFmpeg {
		t.Fatalf("任务参数异常: %+v", job)
	}

	metaFile := filepath.Join(savePath, "meta", "蓝桥杯嵌入式第三讲——Uart、DMA、ADC-Uart、DMA、ADC.json")
	content, err := os.ReadFile(metaFile)
	if err != nil {
		t.Fatalf("读取元数据失败: %v", err)
	}
	if !strings.Contains(string(content), "v.f421220.m3u8") {
		t.Fatalf("元数据内容异常: %s", content)
	}
}

func TestUploadVideoInfoRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		payload string
		want    int
	}{
		{
			name:    "缺少 m3u8 地址",
			method:  http.MethodPost,
			payload: `{"data":{"0":{"file_name":"a.mp4","title":"t"},"video_info":{"src":""}}}`,
			want:    http.StatusBadRequest,
		},
		{
			name:    "不是合法 JSON",
			method:  http.MethodPost,
			payload: `{`,
			want:    http.StatusBadRequest,
		},
		{
			name:    "方法不允许",
			method:  http.MethodGet,
			payload: validPayload,
			want:    http.StatusMethodNotAllowed,
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			jobs := make(chan downloader.Job, 1)
			instance := New(Options{SavePath: t.TempDir(), Concurrent: 4}, jobs, quietLogger())

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(item.method, "/upload-video-info", strings.NewReader(item.payload))
			instance.Handler().ServeHTTP(recorder, request)

			if recorder.Code != item.want {
				t.Fatalf("状态码为 %d, 期望 %d", recorder.Code, item.want)
			}
			if len(jobs) != 0 {
				t.Fatal("非法请求不应产生下载任务")
			}
		})
	}
}

// quietLogger 返回丢弃全部日志的 logger.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
