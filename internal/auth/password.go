package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, uint8(argonThreads), argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

func verifyPassword(password, encoded string) bool {
	salt, want, time, memory, threads, keyLen, err := parsePHC(encoded)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func parsePHC(encoded string) (salt, hash []byte, time, memory uint32, threads uint8, keyLen uint32, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid phc")
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	var p int
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid phc params")
		}
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			return nil, nil, 0, 0, 0, 0, convErr
		}
		switch k {
		case "m":
			memory = uint32(n)
		case "t":
			time = uint32(n)
		case "p":
			p = n
		}
	}
	if p < 1 || p > 255 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid parallelism")
	}
	threads = uint8(p)
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	if len(hash) == 0 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("empty hash")
	}
	return salt, hash, time, memory, threads, uint32(len(hash)), nil
}
