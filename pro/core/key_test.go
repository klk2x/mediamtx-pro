package core

// Go implements several hash functions in various
// `crypto/*` packages.
import (
	"strings"
	"testing"
)

// go test pro/core/key_test.go -v
// go test pro/core/key.go  pro/core/key_test.go  -v
func TestKey(t *testing.T) {
	// 使用你的实际 MAC 地址（en0）
	macAddress := strings.ToUpper("A4:FC:14:05:F7:65")
	expireDate := "20261231" // 设置为2026年12月31日
	key := "sh@021"
	domain := "http://localhost:9997"

	StringToEncrypt := macAddress + "#" + expireDate + "#" + domain + "#" + key
	// To encrypt the StringToEncrypt
	encText, err := Encrypt(StringToEncrypt, CoreSecret)
	if err != nil {
		t.Error("error encrypting your classified text: ", err)
	}
	t.Log(encText)
	// To decrypt the original StringToEncrypt
	decText, err := Decrypt(encText, CoreSecret)
	if err != nil {
		t.Error("error decrypting your encrypted text: ", err)
	}
	t.Log(decText)

}
