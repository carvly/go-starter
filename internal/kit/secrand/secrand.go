// Package secrand 提供安全随机串和不可逆摘要。
package secrand

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

func bytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return b
}

func URL(n int) string {
	return base64.RawURLEncoding.EncodeToString(bytes(n))
}

func Hex(n int) string {
	return hex.EncodeToString(bytes(n))
}

func Hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}
