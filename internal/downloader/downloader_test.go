package downloader

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDecryptAES128CBC(t *testing.T) {
	key := bytes.Repeat([]byte{0x2b}, 16)
	iv := make([]byte, 16)

	plain := bytes.Repeat([]byte("A"), 100)
	encrypted := encryptForTest(t, plain, key, iv)

	decrypted, err := DecryptAES128CBC(encrypted, key, iv)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("解密结果与原文不一致")
	}
}

func TestDecryptAES128CBCToleratesUnpaddedData(t *testing.T) {
	key := bytes.Repeat([]byte{0x2b}, 16)
	iv := make([]byte, 16)

	// 末字节为 0 时 PKCS#7 填充不成立, 此时不应截断任何内容.
	plain := append(bytes.Repeat([]byte("B"), 31), 0x00)
	encrypted := encryptForTest(t, plain, key, iv)

	decrypted, err := DecryptAES128CBC(encrypted, key, iv)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("无填充数据被改动, 长度为 %d", len(decrypted))
	}
}

func TestDecryptAES128CBCRejectsBadInput(t *testing.T) {
	key := bytes.Repeat([]byte{0x2b}, 16)
	iv := make([]byte, 16)

	if _, err := DecryptAES128CBC(bytes.Repeat([]byte{0x01}, 17), key, iv); err == nil {
		t.Fatal("长度不是分块整数倍时应当报错")
	}
	if _, err := DecryptAES128CBC(bytes.Repeat([]byte{0x01}, 32), key[:8], iv); err == nil {
		t.Fatal("密钥长度错误时应当报错")
	}
}

func TestProcessDownloadsDecryptsAndConcatenates(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	iv := make([]byte, 16)

	firstPlain := bytes.Repeat([]byte("A"), 188*3)
	secondPlain := bytes.Repeat([]byte("B"), 188*2)

	var segmentRequests atomic.Int32
	var keyRequests atomic.Int32

	router := http.NewServeMux()
	router.HandleFunc("/key", func(writer http.ResponseWriter, request *http.Request) {
		keyRequests.Add(1)
		_, _ = writer.Write(key)
	})
	router.HandleFunc("/seg0.ts", func(writer http.ResponseWriter, request *http.Request) {
		segmentRequests.Add(1)
		_, _ = writer.Write(encryptForTest(t, firstPlain, key, iv))
	})
	router.HandleFunc("/seg1.ts", func(writer http.ResponseWriter, request *http.Request) {
		segmentRequests.Add(1)
		_, _ = writer.Write(encryptForTest(t, secondPlain, key, iv))
	})

	var playlistURL string
	router.HandleFunc("/playlist.m3u8", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintf(writer, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-KEY:METHOD=AES-128,URI="%s/key",IV=0x%s
#EXTINF:2.000000,
seg0.ts
#EXTINF:2.000000,
seg1.ts
#EXT-X-ENDLIST
`, playlistURL, hex.EncodeToString(iv))
	})

	server := httptest.NewServer(router)
	defer server.Close()
	playlistURL = server.URL

	savePath := t.TempDir()
	instance := New(io.Discard, discardLogger())
	job := Job{
		URL:        server.URL + "/playlist.m3u8",
		FileName:   "测试课程-第一讲.mp4",
		SavePath:   savePath,
		Concurrent: 4,
	}

	if err := instance.Process(context.Background(), job); err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	output := filepath.Join(savePath, "videos", "测试课程-第一讲.mp4")
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("读取成片失败: %v", err)
	}
	want := append(append([]byte{}, firstPlain...), secondPlain...)
	if !bytes.Equal(content, want) {
		t.Fatalf("成片内容与解密结果不一致, 长度为 %d, 期望 %d", len(content), len(want))
	}

	if got := keyRequests.Load(); got != 1 {
		t.Fatalf("密钥被请求了 %d 次, 期望复用为 1 次", got)
	}
	if got := segmentRequests.Load(); got != 2 {
		t.Fatalf("分片被请求了 %d 次, 期望 2 次", got)
	}

	// 分片目录应按序号命名, 便于续传与排查.
	segmentsDir := filepath.Join(savePath, "segments", "测试课程-第一讲")
	entries, err := os.ReadDir(segmentsDir)
	if err != nil {
		t.Fatalf("读取分片目录失败: %v", err)
	}
	if len(entries) != 2 || entries[0].Name() != "00000.ts" || entries[1].Name() != "00001.ts" {
		t.Fatalf("分片目录内容异常: %v", entries)
	}

	// 第二次执行相同任务应当直接复用已下载的分片.
	if err := instance.Process(context.Background(), job); err != nil {
		t.Fatalf("续传失败: %v", err)
	}
	if got := segmentRequests.Load(); got != 2 {
		t.Fatalf("续传时重复下载了分片, 请求次数为 %d", got)
	}
}

func TestProcessFailsWhenSegmentMissing(t *testing.T) {
	router := http.NewServeMux()
	router.HandleFunc("/playlist.m3u8", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `#EXTM3U
#EXT-X-TARGETDURATION:2
#EXTINF:2.0,
missing.ts
#EXT-X-ENDLIST
`)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	savePath := t.TempDir()
	instance := New(io.Discard, discardLogger())
	job := Job{
		URL:        server.URL + "/playlist.m3u8",
		FileName:   "缺失分片.mp4",
		SavePath:   savePath,
		Concurrent: 2,
	}

	err := instance.Process(context.Background(), job)
	if err == nil {
		t.Fatal("分片缺失时应当报错")
	}
	if !strings.Contains(err.Error(), "分片下载失败") {
		t.Fatalf("错误信息应说明分片失败, 实际为 %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(savePath, "videos", "缺失分片.mp4")); statErr == nil {
		t.Fatal("分片失败时不应生成成片")
	}
}

// encryptForTest 用 AES-128-CBC 加密测试数据, 并补齐 PKCS#7 填充.
func encryptForTest(t *testing.T, plain, key, iv []byte) []byte {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("创建加密器失败: %v", err)
	}

	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)

	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
	return encrypted
}

// discardLogger 返回丢弃全部日志的 logger.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
