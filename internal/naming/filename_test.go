package naming

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildVideoFileName(t *testing.T) {
	cases := []struct {
		name     string
		fileName string
		title    string
		want     string
	}{
		{
			name:     "文件名主干与标题用短横线拼接",
			fileName: "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4",
			title:    "Uart、DMA、ADC",
			want:     "蓝桥杯嵌入式第三讲——Uart、DMA、ADC-Uart、DMA、ADC.mp4",
		},
		{
			name:     "非法字符被替换",
			fileName: "第1讲:入门*课?.mp4",
			title:    "标题",
			want:     "第1讲_入门_课-标题.mp4",
		},
		{
			name:     "没有后缀",
			fileName: "lecture",
			title:    "第一讲",
			want:     "lecture-第一讲",
		},
		{
			name:     "标题为空时只用文件名",
			fileName: "lecture.mp4",
			title:    "",
			want:     "lecture.mp4",
		},
		{
			name:     "文件名为空时只用标题",
			fileName: "",
			title:    "第一讲",
			want:     "第一讲",
		},
		{
			name:     "目录部分会被去掉",
			fileName: "/tmp/videos/lecture.mp4",
			title:    "第一讲",
			want:     "lecture-第一讲.mp4",
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := BuildVideoFileName(item.fileName, item.title)
			if got != item.want {
				t.Fatalf("BuildVideoFileName(%q, %q) = %q, 期望 %q", item.fileName, item.title, got, item.want)
			}
		})
	}
}

func TestBuildVideoFileNameTruncatesLongName(t *testing.T) {
	got := BuildVideoFileName(strings.Repeat("很长的课程标题", 40)+".mp4", "补充标题")

	if len(got) > maxFileNameBytes {
		t.Fatalf("文件名长度为 %d 字节, 超过上限 %d", len(got), maxFileNameBytes)
	}
	if !strings.HasSuffix(got, ".mp4") {
		t.Fatalf("截断后丢掉了后缀: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("截断后不是合法的 UTF-8: %q", got)
	}
}

func TestStem(t *testing.T) {
	cases := map[string]string{
		"第三讲-Uart.mp4": "第三讲-Uart",
		"/tmp/a/b.ts":  "b",
		"":             "untitled",
	}

	for input, want := range cases {
		if got := Stem(input); got != want {
			t.Fatalf("Stem(%q) = %q, 期望 %q", input, got, want)
		}
	}
}
