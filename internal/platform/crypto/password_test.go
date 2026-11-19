package crypto_test

import (
	"testing"

	"github.com/alexedwards/argon2id"

	"app.local/internal/platform/crypto"
)

// testParams 用比生产默认值小得多的 Argon2id 参数，让测试跑得够快——
// 生产参数（memory=64MB）本身就是刻意调重的，不适合在单元测试里跑几十次。
var testParams = &argon2id.Params{
	Memory:      8 * 1024,
	Iterations:  1,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

func TestPasswordVerifier_HashAndCompare(t *testing.T) {
	v := crypto.NewPasswordVerifier(testParams)

	hash, err := v.Hash("s3cret-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == "s3cret-password" {
		t.Fatal("Hash() 不能返回明文")
	}

	ok, err := v.Compare(hash, "s3cret-password")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if !ok {
		t.Error("Compare() = false, want true（正确密码应该匹配）")
	}

	ok, err = v.Compare(hash, "wrong-password")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if ok {
		t.Error("Compare() = true, want false（错误密码不应该匹配）")
	}
}

func TestPasswordVerifier_SameSameInputDifferentSalt(t *testing.T) {
	v := crypto.NewPasswordVerifier(testParams)

	h1, err := v.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	h2, err := v.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if h1 == h2 {
		t.Error("两次 Hash() 同一个密码应该因为随机 salt 而得到不同的哈希输出")
	}
}
