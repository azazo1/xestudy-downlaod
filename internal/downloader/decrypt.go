package downloader

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// DecryptAES128CBC 使用 AES-128-CBC 解密 HLS 分片内容.
//
// 分片通常带 PKCS#7 填充, 但也有不带填充的流, 因此填充不合法时保留原文而不是报错.
func DecryptAES128CBC(data, key, iv []byte) ([]byte, error) {
	if len(key) != aes.BlockSize {
		return nil, fmt.Errorf("密钥长度必须为 %d 字节, 当前为 %d", aes.BlockSize, len(key))
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("初始化向量长度必须为 %d 字节, 当前为 %d", aes.BlockSize, len(iv))
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("密文长度为 0")
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度 %d 不是 %d 的整数倍", len(data), aes.BlockSize)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("创建 AES 解密器失败: %w", err)
	}

	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	return pkcs7Unpad(plain), nil
}

// pkcs7Unpad 去除 PKCS#7 填充, 填充不合法时原样返回.
func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	padding := int(data[len(data)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(data) {
		return data
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return data
		}
	}
	return data[:len(data)-padding]
}
