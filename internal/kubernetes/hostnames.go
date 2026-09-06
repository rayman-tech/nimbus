package kubernetes

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func GenerateRandomChars() (string, error) {
	const numBytes = 8
	randBytes := make([]byte, numBytes)
	_, err := rand.Read(randBytes)
	if err != nil {
		return "", fmt.Errorf("generating random chars: %w", err)
	}
	return hex.EncodeToString(randBytes), nil
}
