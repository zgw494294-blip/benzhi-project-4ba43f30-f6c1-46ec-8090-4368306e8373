package service

import (
	"crypto/rand"
	"encoding/hex"
)

func newID(prefix string) string {
	data := make([]byte, 8)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(data)
}
