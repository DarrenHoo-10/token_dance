package crypto

import "testing"

func BenchmarkArgon2idPasswordHash(b *testing.B) {
	params := DefaultArgon2Params
	for i := 0; i < b.N; i++ {
		if _, err := HashPassword("benchmark-password-123!", params); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHMACSHA256(b *testing.B) {
	key := []byte("benchmark-domain-key-32-bytes!!")
	payload := []byte("tokendance-benchmark-payload")
	for i := 0; i < b.N; i++ {
		_ = HMACSHA256(key, payload)
	}
}
