package hls

import (
	"encoding/hex"
	"strings"
	"testing"
)

const encryptedPlaylist = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-KEY:METHOD=AES-128,URI="https://app.xiaoe-tech.com/get_video_key.php?edk=CiCyBHw8&fileId=5145403697684910195&keySource=VodBuildInKMS",IV=0x00000000000000000000000000000000
#EXTINF:2.000000,
v.f421220_0.ts?start=0&end=151727&type=mpegts&sign=abc
#EXTINF:2.000000,
v.f421220_0.ts?start=151728&end=304399&type=mpegts&sign=abc
#EXT-X-ENDLIST
`

func TestParseMediaPlaylistWithKey(t *testing.T) {
	playlist, err := Parse([]byte(encryptedPlaylist))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(playlist.Segments) != 2 {
		t.Fatalf("分片数为 %d, 期望 2", len(playlist.Segments))
	}

	first := playlist.Segments[0]
	if !first.Encrypted() {
		t.Fatal("第一个分片应当被识别为加密分片")
	}
	if !strings.HasPrefix(first.KeyURI, "https://app.xiaoe-tech.com/get_video_key.php") ||
		!strings.Contains(first.KeyURI, "fileId=5145403697684910195") {
		t.Fatalf("密钥地址解析异常: %q", first.KeyURI)
	}
	if first.URI != "v.f421220_0.ts?start=0&end=151727&type=mpegts&sign=abc" {
		t.Fatalf("分片地址应保留 query, 实际为 %q", first.URI)
	}
	if first.SeqID != 0 || playlist.Segments[1].SeqID != 1 {
		t.Fatalf("分片序号异常: %d, %d", first.SeqID, playlist.Segments[1].SeqID)
	}

	wantIV, err := hex.DecodeString("00000000000000000000000000000000")
	if err != nil {
		t.Fatalf("测试向量构造失败: %v", err)
	}
	if got := first.InitVector(); string(got) != string(wantIV) {
		t.Fatalf("初始化向量为 %x, 期望 %x", got, wantIV)
	}
}

func TestParseUsesExplicitInitVector(t *testing.T) {
	payload := `#EXTM3U
#EXT-X-TARGETDURATION:2
#EXT-X-KEY:METHOD=AES-128,URI="/key",IV=0x0102030405060708090a0b0c0d0e0f10
#EXTINF:2.0,
0.ts
#EXT-X-ENDLIST
`

	playlist, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := playlist.Segments[0].InitVector(); hex.EncodeToString(got) != "0102030405060708090a0b0c0d0e0f10" {
		t.Fatalf("初始化向量为 %x", got)
	}
}

func TestParseFallsBackToSequenceWhenIVMissing(t *testing.T) {
	payload := `#EXTM3U
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:7
#EXT-X-KEY:METHOD=AES-128,URI="/key"
#EXTINF:2.0,
0.ts
#EXT-X-ENDLIST
`

	playlist, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	got := playlist.Segments[0].InitVector()
	if hex.EncodeToString(got) != "00000000000000000000000000000007" {
		t.Fatalf("未指定 IV 时应使用分片序号, 实际为 %x", got)
	}
}

func TestParseUnencryptedPlaylist(t *testing.T) {
	payload := `#EXTM3U
#EXT-X-TARGETDURATION:2
#EXTINF:2.0,
0.ts
#EXT-X-ENDLIST
`

	playlist, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if playlist.Segments[0].Encrypted() {
		t.Fatal("未加密播放列表不应标记为加密分片")
	}
}

func TestParseRejectsMasterPlaylist(t *testing.T) {
	payload := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1280000
low/index.m3u8
`

	if _, err := Parse([]byte(payload)); err == nil {
		t.Fatal("Master Playlist 应当被拒绝")
	}
}

func TestParseRejectsUnsupportedMethod(t *testing.T) {
	payload := `#EXTM3U
#EXT-X-TARGETDURATION:2
#EXT-X-KEY:METHOD=SAMPLE-AES,URI="/key"
#EXTINF:2.0,
0.ts
#EXT-X-ENDLIST
`

	if _, err := Parse([]byte(payload)); err == nil || !strings.Contains(err.Error(), "SAMPLE-AES") {
		t.Fatalf("不支持的加密方式应当报错, 实际为 %v", err)
	}
}
