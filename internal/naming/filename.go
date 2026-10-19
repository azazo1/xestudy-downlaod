// Package naming 负责由抓包数据组合出安全的视频文件名.
package naming

import (
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// illegalChars 是不能出现在文件名中的字符, 包含路径分隔符, Windows 保留字符与控制字符.
var illegalChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f\x7f]`)

// maxFileNameBytes 是输出文件名的字节上限, 为 .part 临时文件后缀预留空间.
const maxFileNameBytes = 200

// BuildVideoFileName 组合出 "{原文件名主干}-{标题}.{后缀}", 与 rust 版命名规则一致.
//
// 例如 "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4" 与标题 "Uart、DMA、ADC"
// 会得到 "蓝桥杯嵌入式第三讲——Uart、DMA、ADC-Uart、DMA、ADC.mp4".
func BuildVideoFileName(fileName, title string) string {
	base := path.Base(strings.ReplaceAll(fileName, `\`, "/"))
	extension := strings.TrimPrefix(path.Ext(base), ".")
	stem := strings.TrimSuffix(base, path.Ext(base))

	var name string
	switch {
	case strings.TrimSpace(stem) == "" && strings.TrimSpace(title) != "":
		// 抓包数据里没有文件名时, 退化成只用标题命名.
		name = Sanitize(title)
	default:
		name = Sanitize(stem)
		if strings.TrimSpace(title) != "" {
			name += "-" + Sanitize(title)
		}
	}

	limit := maxFileNameBytes
	if extension != "" {
		limit -= len(extension) + 1
	}
	name = truncate(name, limit)

	if extension == "" {
		return name
	}
	return name + "." + Sanitize(extension)
}

// Stem 返回文件名的去后缀主干, 同时做净化, 用于分片目录与元数据文件名.
func Stem(fileName string) string {
	base := path.Base(strings.ReplaceAll(fileName, `\`, "/"))
	return Sanitize(strings.TrimSuffix(base, path.Ext(base)))
}

// Sanitize 把任意字符串净化为可用的文件名片段.
func Sanitize(name string) string {
	cleaned := illegalChars.ReplaceAllString(name, "_")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.Trim(cleaned, " ._")
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "untitled"
	}
	return cleaned
}

// truncate 按字节上限截断字符串, 不会截断半个 UTF-8 字符.
func truncate(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	cut := 0
	for index := range text {
		if index > limit {
			break
		}
		cut = index
	}
	if cut == 0 {
		return ""
	}
	trimmed := strings.TrimRight(text[:cut], " .")
	if utf8.ValidString(trimmed) {
		return trimmed
	}
	return strings.TrimRight(text[:cut-1], " .")
}
