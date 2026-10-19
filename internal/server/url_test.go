package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azazo1/xestudy-downlaod/internal/downloader"
)

const sampleM3U8 = "https://htv-vod.h5.xed.plus/a8307e36f7a64aeeae086cc157bb356b/app3qiy6sow1137/video/b_u_cs940p9lv5fncchkn4mg/mpd6pt8c06aq/drm/main.m3u8?sign=abc&t=6ad68803"

func TestUploadVideoURLEnqueuesJob(t *testing.T) {
	savePath := t.TempDir()
	jobs := make(chan downloader.Job, 4)
	instance := New(Options{SavePath: savePath, Concurrent: 6}, jobs, quietLogger())

	body := `{"url":"` + sampleM3U8 + `","name":"蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4"}`
	recorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/upload-video-url", strings.NewReader(body)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码为 %d, 期望 200, 响应体: %s", recorder.Code, recorder.Body.String())
	}

	var job downloader.Job
	select {
	case job = <-jobs:
	default:
		t.Fatal("任务没有进入队列")
	}
	if job.URL != sampleM3U8 {
		t.Fatalf("m3u8 地址为 %q", job.URL)
	}
	if job.FileName != "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4" {
		t.Fatalf("文件名为 %q", job.FileName)
	}
	if job.SavePath != savePath || job.Concurrent != 6 {
		t.Fatalf("任务参数异常: %+v", job)
	}

	if _, err := os.Stat(filepath.Join(savePath, "meta", "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.json")); err != nil {
		t.Fatalf("元数据未落盘: %v", err)
	}
}

func TestUploadVideoURLGeneratesName(t *testing.T) {
	jobs := make(chan downloader.Job, 1)
	instance := New(Options{SavePath: t.TempDir(), Concurrent: 4}, jobs, quietLogger())

	body := `{"url":"` + sampleM3U8 + `"}`
	recorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/upload-video-url", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码为 %d, 期望 200", recorder.Code)
	}

	job := <-jobs
	if !strings.HasPrefix(job.FileName, "video-") || !strings.HasSuffix(job.FileName, ".mp4") {
		t.Fatalf("未提供文件名时应生成带时间戳的名字, 实际为 %q", job.FileName)
	}
}

func TestUploadVideoURLAddsExtension(t *testing.T) {
	jobs := make(chan downloader.Job, 1)
	instance := New(Options{SavePath: t.TempDir(), Concurrent: 4}, jobs, quietLogger())

	body := `{"url":"` + sampleM3U8 + `","name":"第三讲 Uart、DMA、ADC"}`
	recorder := httptest.NewRecorder()
	instance.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/upload-video-url", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码为 %d, 期望 200", recorder.Code)
	}

	job := <-jobs
	if job.FileName != "第三讲 Uart、DMA、ADC.mp4" {
		t.Fatalf("无后缀的名称应补上 .mp4, 实际为 %q", job.FileName)
	}
}

func TestUploadVideoURLRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{name: "缺少地址", method: http.MethodPost, body: `{"name":"a.mp4"}`, want: http.StatusBadRequest},
		{name: "不是 http 地址", method: http.MethodPost, body: `{"url":"ftp://a/b.m3u8"}`, want: http.StatusBadRequest},
		{name: "不是播放列表", method: http.MethodPost, body: `{"url":"https://a.com/video.mp4"}`, want: http.StatusBadRequest},
		{name: "不是 JSON", method: http.MethodPost, body: `{`, want: http.StatusBadRequest},
		{name: "方法不允许", method: http.MethodGet, body: `{}`, want: http.StatusMethodNotAllowed},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			jobs := make(chan downloader.Job, 1)
			instance := New(Options{SavePath: t.TempDir(), Concurrent: 4}, jobs, quietLogger())

			recorder := httptest.NewRecorder()
			instance.Handler().ServeHTTP(recorder, httptest.NewRequest(item.method, "/upload-video-url", strings.NewReader(item.body)))
			if recorder.Code != item.want {
				t.Fatalf("状态码为 %d, 期望 %d", recorder.Code, item.want)
			}
			if len(jobs) != 0 {
				t.Fatal("非法请求不应产生下载任务")
			}
		})
	}
}
