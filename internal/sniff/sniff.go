// Package sniff 记录并分析抓包脚本回传的请求与响应.
//
// 它是诊断工具: 小鹅通客户端换接口时, 靠它找出视频信息究竟来自哪个地址.
// 记录只留在内存里, 正文有截断上限, 进程退出即消失.
package sniff

import (
	"net/url"
	"regexp"
	"strings"
)

// Phase 表示这次回传来自请求阶段还是响应阶段.
type Phase string

const (
	// PhaseRequest 表示请求阶段.
	PhaseRequest Phase = "request"
	// PhaseResponse 表示响应阶段.
	PhaseResponse Phase = "response"
)

// Entry 是一条回传记录, 同一会话的请求与响应通过 UID 合并.
type Entry struct {
	UID          string `json:"uid"`
	Phase        Phase  `json:"phase"`
	URL          string `json:"url"`
	Method       string `json:"method"`
	Status       int    `json:"status,omitempty"`
	RequestBody  string `json:"request_body,omitempty"`
	ResponseBody string `json:"response_body,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
}

// Verdict 是一条记录的分析结论.
type Verdict struct {
	Host              string   `json:"host"`
	Path              string   `json:"path"`
	HasVideoInfo      bool     `json:"has_video_info"`
	RequestHasVideoID bool     `json:"request_has_video_id"`
	M3U8URLs          []string `json:"m3u8_urls,omitempty"`
}

// Interesting 表示这条记录值得关注: 响应里有视频信息或 m3u8 地址.
func (v Verdict) Interesting() bool {
	return v.HasVideoInfo || len(v.M3U8URLs) > 0
}

// maxM3U8PerEntry 限制单条记录里抽取的 m3u8 地址数量.
const maxM3U8PerEntry = 5

var (
	// m3u8 地址通常直接出现在 JSON 字符串里, 因此排除引号与反斜杠.
	m3u8Pattern = regexp.MustCompile(`https?://[^\s"'\\<>]+\.m3u8[^\s"'\\<>]*`)
	// video_id 在请求体里可能为空, 只有非空才说明这次请求锁定了具体视频.
	videoIDPattern = regexp.MustCompile(`"video_id"\s*:\s*"([^"]*)"`)
)

// Analyze 分析一条记录, 抽出接口地址, 视频信息与 m3u8 地址.
func Analyze(entry Entry) Verdict {
	verdict := Verdict{}
	if parsed, err := url.Parse(entry.URL); err == nil {
		verdict.Host = parsed.Host
		verdict.Path = parsed.Path
		verdict.RequestHasVideoID = parsed.Query().Get("video_id") != ""
	}

	verdict.HasVideoInfo = strings.Contains(entry.ResponseBody, "video_info")
	verdict.M3U8URLs = matchM3U8(entry.ResponseBody)
	verdict.RequestHasVideoID = verdict.RequestHasVideoID || hasVideoID(entry.RequestBody)
	return verdict
}

// matchM3U8 抽取响应里出现的 m3u8 地址, 去重并限制数量.
func matchM3U8(body string) []string {
	if body == "" {
		return nil
	}

	found := make([]string, 0, maxM3U8PerEntry)
	seen := make(map[string]struct{})
	for _, match := range m3u8Pattern.FindAllString(body, -1) {
		if _, ok := seen[match]; ok {
			continue
		}
		seen[match] = struct{}{}
		found = append(found, match)
		if len(found) >= maxM3U8PerEntry {
			break
		}
	}
	return found
}

// hasVideoID 判断请求体里是否带了非空的 video_id.
func hasVideoID(body string) bool {
	if body == "" {
		return false
	}
	for _, match := range videoIDPattern.FindAllStringSubmatch(body, -1) {
		if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			return true
		}
	}
	return false
}
