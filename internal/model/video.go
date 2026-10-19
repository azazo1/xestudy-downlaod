// Package model 描述抓包脚本回传的 JSON 数据结构.
//
// 数据来自小鹅通 https://app.xiaoe-tech.com/query_video_info 接口的响应:
// 视频详情被放在 data 下以 "0" 为键的对象里, m3u8 播放地址在 data.video_info.src.
package model

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// UploadVideoRequest 是 POST /upload-video-info 的请求体.
type UploadVideoRequest struct {
	Data Payload `json:"data"`
}

// Payload 对应响应中的 data 字段.
type Payload struct {
	// Content 对应 data."0", 保存课程视频的文件名与标题.
	Content Content `json:"0"`
	// VideoInfo 对应 data.video_info.
	VideoInfo VideoInfo `json:"video_info"`
}

// Content 对应 data."0".
type Content struct {
	FileName string `json:"file_name"`
	Title    string `json:"title"`
}

// VideoInfo 对应 data.video_info, 其中 src 是 m3u8 播放地址.
type VideoInfo struct {
	Src string `json:"src"`
}

// UploadVideoURLRequest 是 POST /upload-video-url 的请求体.
//
// 客户端换接口后, 抓包脚本可以直接提交抽出来的 m3u8 地址, 不必依赖固定的响应结构.
type UploadVideoURLRequest struct {
	// URL 是 m3u8 播放列表地址.
	URL string `json:"url"`
	// Name 是期望的输出文件名, 可以带后缀; 留空时由服务端生成.
	Name string `json:"name"`
}

// Validate 检查地址是否是可下载的播放列表.
func (r UploadVideoURLRequest) Validate() error {
	parsed, err := url.Parse(strings.TrimSpace(r.URL))
	if err != nil {
		return fmt.Errorf("url 无法解析: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("url 必须是 http 或 https 地址")
	}
	if !strings.HasSuffix(parsed.Path, ".m3u8") && !strings.HasSuffix(parsed.Path, ".m3u") {
		return errors.New("url 看起来不是 m3u8 播放列表")
	}
	return nil
}

// Validate 检查下载所必需的字段.
func (r UploadVideoRequest) Validate() error {
	if r.Data.VideoInfo.Src == "" {
		return errors.New("缺少 data.video_info.src, 无法确定视频地址")
	}
	if r.Data.Content.FileName == "" && r.Data.Content.Title == "" {
		return errors.New(`缺少 data."0".file_name 与 title, 无法确定文件名`)
	}
	return nil
}
