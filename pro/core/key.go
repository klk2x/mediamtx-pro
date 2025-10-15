package core

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// Go implements several hash functions in various
// `crypto/*` packages.

var CoreBytes = []byte{35, 46, 57, 24, 85, 35, 24, 74, 87, 35, 88, 98, 66, 32, 14, 05}

// This should be in an env file in production
const CoreSecret string = "TseNttHWNvZcV.)o%n>%Vk}8"

func Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
func Decode(s string) []byte {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return data
}

// Encrypt method is to encrypt or hide any classified text
func Encrypt(text, MySecret string) (string, error) {
	block, err := aes.NewCipher([]byte(MySecret))
	if err != nil {
		return "", err
	}
	plainText := []byte(text)
	cfb := cipher.NewCFBEncrypter(block, CoreBytes)
	cipherText := make([]byte, len(plainText))
	cfb.XORKeyStream(cipherText, plainText)
	return Encode(cipherText), nil
}

// Decrypt method is to extract back the encrypted text
func Decrypt(text, MySecret string) (string, error) {
	block, err := aes.NewCipher([]byte(MySecret))
	if err != nil {
		return "", err
	}
	cipherText := Decode(text)
	cfb := cipher.NewCFBDecrypter(block, CoreBytes)
	plainText := make([]byte, len(cipherText))
	cfb.XORKeyStream(plainText, cipherText)
	return string(plainText), nil
}
func mainkey() {
	macAddress := strings.ToUpper(os.Args[1])
	key := "sh@021"
	expireDate := "21071212"
	domain := os.Args[2]
	if len(os.Args) > 3 {
		expireDate = os.Args[3]
	}

	StringToEncrypt := macAddress + "#" + expireDate + "#" + domain + "#" + key
	// To encrypt the StringToEncrypt
	encText, err := Encrypt(StringToEncrypt, CoreSecret)
	if err != nil {
		fmt.Println("error encrypting your classified text: ", err)
	}
	fmt.Printf("%s %s ->\n%s\n", os.Args[1], domain, encText)
	// fmt.Println(encText)

	// To decrypt the original StringToEncrypt
	// decText, err := Decrypt(encText, CoreSecret)
	// if err != nil {
	// 	fmt.Errorf("error decrypting your encrypted text: ", err)
	// }
	// fmt.Println(decText)
}
