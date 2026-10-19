// Package model 描述抓包脚本回传的 JSON 数据结构.
//
// 数据来自小鹅通 https://app.xiaoe-tech.com/query_video_info 接口的响应:
// 视频详情被放在 data 下以 "0" 为键的对象里, m3u8 播放地址在 data.video_info.src.
package model

import "errors"

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
