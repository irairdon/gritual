package circles

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

func newRawToken() (raw string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(secret []byte, raw string) []byte {
	sum := sha256.New()
	sum.Write(secret)
	sum.Write([]byte(raw))
	return sum.Sum(nil)
}
