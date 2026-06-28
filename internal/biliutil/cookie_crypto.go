package biliutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
)

const cookieMagic = "HIKAMI_V1"

var (
	cookieEncryptionKey []byte
	ErrInvalidKey       = errors.New("cookie encryption key must be 32 bytes (64 hex chars)")
)

// SetCookieEncryptionKey 设置 Cookie 加密密钥（AES-256 需要 32 字节）。
// 传入空字符串则禁用加密（向后兼容）。
func SetCookieEncryptionKey(hexKey string) error {
	if hexKey == "" {
		cookieEncryptionKey = nil
		return nil
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return errors.New("cookie encryption key must be valid hex")
	}
	if len(key) != 32 {
		return ErrInvalidKey
	}
	cookieEncryptionKey = key
	return nil
}

// CookieEncryptionEnabled 返回是否启用了 Cookie 加密。
func CookieEncryptionEnabled() bool {
	return len(cookieEncryptionKey) == 32
}

// encryptCookieFile 使用 AES-256-GCM 加密 cookie 文件内容。
// 格式: HIKAMI_V1(8B) + nonce(12B) + ciphertext+tag
func encryptCookieFile(plaintext []byte) ([]byte, error) {
	if !CookieEncryptionEnabled() {
		return plaintext, nil
	}

	block, err := aes.NewCipher(cookieEncryptionKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// HIKAMI_V1 + nonce + ciphertext
	result := make([]byte, 0, len(cookieMagic)+len(nonce)+len(ciphertext))
	result = append(result, cookieMagic...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	return result, nil
}

// decryptCookieFile 检测并解密 cookie 文件内容。
// 如果不是加密格式（无 HIKAMI_V1 魔数），原样返回。
func decryptCookieFile(data []byte) ([]byte, error) {
	if len(data) < len(cookieMagic) || string(data[:len(cookieMagic)]) != cookieMagic {
		return data, nil
	}
	if !CookieEncryptionEnabled() {
		return nil, errors.New("cookie file is encrypted but no encryption key configured")
	}

	data = data[len(cookieMagic):]
	if len(data) < 12 {
		return nil, errors.New("encrypted cookie file too short")
	}

	nonce := data[:12]
	ciphertext := data[12:]

	block, err := aes.NewCipher(cookieEncryptionKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("cookie file decryption failed: authentication error")
	}
	return plaintext, nil
}
