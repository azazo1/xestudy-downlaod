package sniff

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxRecords 是内存里保留的最大会话数, 超出后丢弃最早的记录.
	maxRecords = 500
	// maxBody 是单条记录里正文的保存长度上限.
	maxBody = 64 << 10
	// snippetLength 是报告里正文摘要的长度.
	snippetLength = 512
)

// Record 是一条合并后的记录.
type Record struct {
	Entry   Entry
	Verdict Verdict
	Merged  int
	FirstAt time.Time
	LastAt  time.Time
}

// Endpoint 是按 host 与 path 聚合出的接口画像.
type Endpoint struct {
	Host string
	Path string
	// Count 是该接口被观测到的会话数 (同一次请求与响应只算一次).
	Count             int
	HasVideoInfo      bool
	RequestHasVideoID bool
	M3U8Count         int
	Example           string
}

// Recorder 保存最近的记录并按接口聚合, 供诊断报告使用.
type Recorder struct {
	mutex     sync.Mutex
	order     []string
	records   map[string]*Record
	endpoints map[string]*Endpoint
}

// NewRecorder 创建记录器.
func NewRecorder() *Recorder {
	return &Recorder{
		records:   make(map[string]*Record),
		endpoints: make(map[string]*Endpoint),
	}
}

// Record 合并一条记录并返回分析结论.
//
// UID 相同 (同一次 HTTP 会话) 的请求与响应会合并到同一条记录里, 因此分析用的是
// 请求体加响应体的完整视图.
func (r *Recorder) Record(entry Entry, now time.Time) Verdict {
	entry.RequestBody = truncate(entry.RequestBody, maxBody)
	entry.ResponseBody = truncate(entry.ResponseBody, maxBody)

	r.mutex.Lock()
	defer r.mutex.Unlock()

	key := entry.UID
	if key == "" {
		// 抓包脚本没给 UID 时按阶段与时间各自成条, 避免把无关请求合并到一起.
		key = string(entry.Phase) + "|" + entry.URL + "|" + strconv.FormatInt(now.UnixNano(), 10)
	}

	record, exists := r.records[key]
	isNew := !exists
	if isNew {
		record = &Record{FirstAt: now}
		r.records[key] = record
		r.order = append(r.order, key)
		r.trim()
	}

	mergeEntry(&record.Entry, entry)
	record.Verdict = Analyze(record.Entry)
	record.Merged++
	record.LastAt = now

	r.observe(record.Entry, record.Verdict, isNew)

	return record.Verdict
}

// Reset 清空全部记录, 便于按一次干净的抓包会话重新分析.
func (r *Recorder) Reset() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.order = nil
	r.records = make(map[string]*Record)
	r.endpoints = make(map[string]*Endpoint)
}

// Report 生成当前的分析报告.
func (r *Recorder) Report() Report {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	report := Report{Total: len(r.records), Candidates: []RecordView{}, Endpoints: []EndpointView{}}
	for _, key := range r.order {
		record := r.records[key]
		if !record.Verdict.Interesting() {
			continue
		}
		report.Candidates = append(report.Candidates, viewOf(record))
	}

	for _, endpoint := range r.endpoints {
		report.Endpoints = append(report.Endpoints, endpointViewOf(endpoint))
	}
	sort.Slice(report.Endpoints, func(i, j int) bool {
		left, right := report.Endpoints[i], report.Endpoints[j]
		if left.M3U8Count != right.M3U8Count {
			return left.M3U8Count > right.M3U8Count
		}
		if left.Count != right.Count {
			return left.Count > right.Count
		}
		if left.Host != right.Host {
			return left.Host < right.Host
		}
		return left.Path < right.Path
	})

	report.Hint = hintFor(report.Endpoints)
	return report
}

// Report 是诊断报告的 JSON 结构.
type Report struct {
	// Total 是记录到的会话总数.
	Total int `json:"total"`
	// Hint 给出下一步建议, 例如规则匹配地址该怎么写.
	Hint string `json:"hint,omitempty"`
	// Candidates 是含视频信息或 m3u8 地址的记录.
	Candidates []RecordView `json:"candidates"`
	// Endpoints 是按接口聚合的画像, 按 m3u8 命中次数排序.
	Endpoints []EndpointView `json:"endpoints"`
}

// RecordView 是报告里的单条记录摘要, 正文只保留片段.
type RecordView struct {
	UID             string  `json:"uid"`
	URL             string  `json:"url"`
	Method          string  `json:"method"`
	Status          int     `json:"status,omitempty"`
	Verdict         Verdict `json:"verdict"`
	RequestSnippet  string  `json:"request_snippet,omitempty"`
	ResponseSnippet string  `json:"response_snippet,omitempty"`
}

// EndpointView 是报告里的接口画像.
type EndpointView struct {
	Host              string `json:"host"`
	Path              string `json:"path"`
	Count             int    `json:"count"`
	HasVideoInfo      bool   `json:"has_video_info"`
	RequestHasVideoID bool   `json:"request_has_video_id"`
	M3U8Count         int    `json:"m3u8_count"`
	Example           string `json:"example,omitempty"`
}

// mergeEntry 把新到的阶段数据补进已有记录, 不做覆盖.
func mergeEntry(target *Entry, entry Entry) {
	if entry.UID != "" {
		target.UID = entry.UID
	}
	if entry.URL != "" {
		target.URL = entry.URL
	}
	if entry.Method != "" {
		target.Method = entry.Method
	}
	if entry.Status != 0 {
		target.Status = entry.Status
	}
	if entry.RequestBody != "" {
		target.RequestBody = entry.RequestBody
	}
	if entry.ResponseBody != "" {
		target.ResponseBody = entry.ResponseBody
	}
	if entry.Phase != "" {
		target.Phase = entry.Phase
	}
	target.Truncated = target.Truncated || entry.Truncated
}

// observe 更新接口画像, 只有新会话才增加计数.
func (r *Recorder) observe(entry Entry, verdict Verdict, isNew bool) {
	if verdict.Host == "" && verdict.Path == "" {
		return
	}

	key := verdict.Host + verdict.Path
	endpoint, ok := r.endpoints[key]
	if !ok {
		endpoint = &Endpoint{Host: verdict.Host, Path: verdict.Path, Example: entry.URL}
		r.endpoints[key] = endpoint
	}

	if isNew {
		endpoint.Count++
	}
	endpoint.HasVideoInfo = endpoint.HasVideoInfo || verdict.HasVideoInfo
	endpoint.RequestHasVideoID = endpoint.RequestHasVideoID || verdict.RequestHasVideoID
	endpoint.M3U8Count += len(verdict.M3U8URLs)
}

// trim 丢弃超出上限的旧记录.
func (r *Recorder) trim() {
	for len(r.order) > maxRecords {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.records, oldest)
	}
}

// viewOf 生成报告用的记录摘要.
func viewOf(record *Record) RecordView {
	return RecordView{
		UID:             record.Entry.UID,
		URL:             record.Entry.URL,
		Method:          record.Entry.Method,
		Status:          record.Entry.Status,
		Verdict:         record.Verdict,
		RequestSnippet:  truncate(record.Entry.RequestBody, snippetLength),
		ResponseSnippet: truncate(record.Entry.ResponseBody, snippetLength),
	}
}

// endpointViewOf 生成报告用的接口画像.
func endpointViewOf(endpoint *Endpoint) EndpointView {
	return EndpointView{
		Host:              endpoint.Host,
		Path:              endpoint.Path,
		Count:             endpoint.Count,
		HasVideoInfo:      endpoint.HasVideoInfo,
		RequestHasVideoID: endpoint.RequestHasVideoID,
		M3U8Count:         endpoint.M3U8Count,
		Example:           endpoint.Example,
	}
}

// hintFor 根据画像给出下一步建议.
func hintFor(endpoints []EndpointView) string {
	for _, endpoint := range endpoints {
		if endpoint.M3U8Count == 0 {
			continue
		}
		address := fmt.Sprintf("https://%s%s", endpoint.Host, endpoint.Path)
		return fmt.Sprintf("响应里出现 m3u8 的接口是 %s, 可以把 reqable 规则的匹配地址写成 %s*", address, address)
	}
	return "还没有看到带 m3u8 的响应: 请在客户端里点开视频并播放几秒, 再刷新本报告"
}

// truncate 按字节上限截断正文, 不切断 UTF-8 字符.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}

	cut := limit
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return strings.TrimRight(text[:cut], "\x00") + "...[截断]"
}

// utf8Start 判断该字节是否是 UTF-8 字符的首字节.
func utf8Start(char byte) bool {
	return char&0xC0 != 0x80
}
