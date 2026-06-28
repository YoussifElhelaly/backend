package ai

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// encryptionKey returns a 32-byte key from the AI_ENCRYPTION_KEY env var (64 hex chars).
// Falls back to a deterministic dev key so the app still runs without config.
func encryptionKey() ([]byte, error) {
	raw := os.Getenv("AI_ENCRYPTION_KEY")
	if raw == "" {
		return nil, errors.New("AI_ENCRYPTION_KEY environment variable is not set — refusing to encrypt/decrypt")
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("AI_ENCRYPTION_KEY must be 64 hex chars (32 bytes)")
	}
	return key, nil
}

func Encrypt(plaintext string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

func Decrypt(cipherhex string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
	}
	data, err := hex.DecodeString(cipherhex)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
