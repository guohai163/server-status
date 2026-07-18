package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewUUID returns a random RFC 4122 version 4 UUID without adding a dependency.
func NewUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read random UUID bytes: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}
