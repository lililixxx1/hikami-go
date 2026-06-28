package biliutil

import (
	"bytes"
	"testing"
)

func TestSetCookieEncryptionKey_EmptyDisables(t *testing.T) {
	if err := SetCookieEncryptionKey(""); err != nil {
		t.Fatalf("empty key should not error: %v", err)
	}
	if CookieEncryptionEnabled() {
		t.Fatal("empty key should disable encryption")
	}
}

func TestSetCookieEncryptionKey_Valid32Bytes(t *testing.T) {
	hexKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := SetCookieEncryptionKey(hexKey); err != nil {
		t.Fatalf("valid 32-byte key should not error: %v", err)
	}
	if !CookieEncryptionEnabled() {
		t.Fatal("valid key should enable encryption")
	}
	// cleanup
	SetCookieEncryptionKey("")
}

func TestSetCookieEncryptionKey_InvalidHex(t *testing.T) {
	if err := SetCookieEncryptionKey("not-hex"); err == nil {
		t.Fatal("invalid hex should error")
	}
}

func TestSetCookieEncryptionKey_WrongLength(t *testing.T) {
	if err := SetCookieEncryptionKey("0123456789abcdef"); err == nil {
		t.Fatal("16-byte key should error (need 32)")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	hexKey := "aabbccdd11223344aabbccdd11223344aabbccdd11223344aabbccdd11223344"
	SetCookieEncryptionKey(hexKey)
	defer SetCookieEncryptionKey("")

	plaintext := []byte("# Netscape HTTP Cookie File\n.bilibili.com\tTRUE\t/\tFALSE\t0\tSESSDATA\tabc123\n")
	encrypted, err := encryptCookieFile(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.HasPrefix(encrypted, []byte("# Netscape")) {
		t.Fatal("encrypted output should not start with plaintext header")
	}
	if !bytes.HasPrefix(encrypted, []byte(cookieMagic)) {
		t.Fatal("encrypted output should start with magic bytes")
	}

	decrypted, err := decryptCookieFile(encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("round-trip mismatch:\ngot  %q\nwant %q", decrypted, plaintext)
	}
}

func TestEncryptPassthrough_NoKey(t *testing.T) {
	SetCookieEncryptionKey("")
	plaintext := []byte("hello world")
	out, err := encryptCookieFile(plaintext)
	if err != nil {
		t.Fatalf("passthrough encrypt: %v", err)
	}
	if !bytes.Equal(out, plaintext) {
		t.Fatal("without key, encrypt should return plaintext unchanged")
	}
}

func TestDecryptPassthrough_NoMagic(t *testing.T) {
	SetCookieEncryptionKey("")
	data := []byte("# Netscape HTTP Cookie File\nsome line\n")
	out, err := decryptCookieFile(data)
	if err != nil {
		t.Fatalf("passthrough decrypt: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Fatal("without magic header, decrypt should return data unchanged")
	}
}

func TestDecryptEncryptedFile_NoKeyConfigured(t *testing.T) {
	hexKey := "1111111111111111111111111111111111111111111111111111111111111111"
	SetCookieEncryptionKey(hexKey)
	encrypted, _ := encryptCookieFile([]byte("secret"))

	SetCookieEncryptionKey("")
	_, err := decryptCookieFile(encrypted)
	if err == nil {
		t.Fatal("decrypting encrypted file without key should error")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := "1111111111111111111111111111111111111111111111111111111111111111"
	key2 := "2222222222222222222222222222222222222222222222222222222222222222"
	SetCookieEncryptionKey(key1)
	encrypted, _ := encryptCookieFile([]byte("secret data"))

	SetCookieEncryptionKey(key2)
	_, err := decryptCookieFile(encrypted)
	if err == nil {
		t.Fatal("decrypting with wrong key should fail (GCM auth)")
	}
}

func TestDecryptTruncatedEncrypted(t *testing.T) {
	hexKey := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	SetCookieEncryptionKey(hexKey)
	defer SetCookieEncryptionKey("")

	// Magic + less than 12 bytes nonce
	truncated := append([]byte(cookieMagic), make([]byte, 5)...)
	_, err := decryptCookieFile(truncated)
	if err == nil {
		t.Fatal("truncated encrypted data should error")
	}
}
