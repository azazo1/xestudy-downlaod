// Package hls 解析 m3u8 媒体播放列表, 抽取分片地址与加密信息.
package hls

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/grafov/m3u8"
)

// blockSize 是 AES 分组长度, 同时也是 HLS 初始化向量长度.
const blockSize = 16

// MethodNone 表示分片未加密.
const MethodNone = "NONE"

// MethodAES128 是小鹅通视频使用的加密方式.
const MethodAES128 = "AES-128"

// Segment 是一个待下载的媒体分片.
type Segment struct {
	// URI 是分片地址, 可能是相对路径.
	URI string
	// SeqID 是分片在播放列表中的序号, 用于未显式给出 IV 时推导初始化向量.
	SeqID uint64
	// KeyMethod 是加密方式, 未加密时为空或 NONE.
	KeyMethod string
	// KeyURI 是密钥地址, 为空表示分片未加密.
	KeyURI string
	// IV 是播放列表中显式指定的初始化向量, 为空表示未指定.
	IV []byte
}

// Encrypted 报告分片是否需要 AES-128 解密.
func (s Segment) Encrypted() bool {
	return s.KeyURI != "" && !strings.EqualFold(s.KeyMethod, MethodNone)
}

// InitVector 返回解密用的初始化向量.
//
// 显式 IV 优先; 未指定时按 HLS 规范使用分片序号作为大端 128 位值,
// 播放列表从头开始时该值与全零 IV 等价.
func (s Segment) InitVector() []byte {
	if len(s.IV) == blockSize {
		return s.IV
	}
	iv := make([]byte, blockSize)
	binary.BigEndian.PutUint64(iv[8:], s.SeqID)
	return iv
}

// Playlist 是解析结果.
type Playlist struct {
	// Segments 是按播放顺序排列的分片列表.
	Segments []Segment
}

// Parse 解析媒体播放列表.
//
// Master Playlist 会被明确拒绝, 因为小鹅通返回的是具体媒体列表,
// 出现 Master 说明拿到的地址不对.
func Parse(data []byte) (*Playlist, error) {
	playlist, listType, err := m3u8.DecodeFrom(bytes.NewReader(data), false)
	if err != nil {
		return nil, fmt.Errorf("解析 m3u8 失败: %w", err)
	}

	switch listType {
	case m3u8.MASTER:
		return nil, errors.New("该地址是 Master Playlist, 需要提供具体的媒体播放列表地址")
	case m3u8.MEDIA:
	default:
		return nil, errors.New("无法识别播放列表类型")
	}

	media, ok := playlist.(*m3u8.MediaPlaylist)
	if !ok {
		return nil, errors.New("播放列表类型断言失败")
	}

	segments := make([]Segment, 0, len(media.Segments))
	for _, item := range media.Segments {
		if item == nil || item.URI == "" {
			continue
		}
		segment := Segment{URI: item.URI, SeqID: item.SeqId}

		key := item.Key
		if key == nil {
			key = media.Key
		}
		if err := fillKey(&segment, key); err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}

	if len(segments) == 0 {
		return nil, errors.New("播放列表中没有可下载的分片")
	}
	return &Playlist{Segments: segments}, nil
}

// fillKey 把播放列表中的 EXT-X-KEY 信息写入分片.
func fillKey(segment *Segment, key *m3u8.Key) error {
	if key == nil {
		return nil
	}

	method := strings.ToUpper(strings.TrimSpace(key.Method))
	switch method {
	case "", MethodNone:
		segment.KeyMethod = MethodNone
	case MethodAES128:
		if key.URI == "" {
			return errors.New("AES-128 加密分片缺少密钥地址")
		}
		segment.KeyMethod = MethodAES128
		segment.KeyURI = key.URI
	default:
		return fmt.Errorf("不支持的加密方式 %q, 目前只支持 AES-128", key.Method)
	}

	iv, err := parseIV(key.IV)
	if err != nil {
		return err
	}
	segment.IV = iv
	return nil
}

// parseIV 解析 IV 属性, 形如 0x00000000000000000000000000000000.
func parseIV(value string) ([]byte, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil, nil
	}
	text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")

	raw, err := hex.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("无法解析 IV %q: %w", value, err)
	}
	return raw, nil
}
