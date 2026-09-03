package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func newID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

func makeTitle(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if utf8.RuneCountInString(message) <= 36 {
		return message
	}
	runes := []rune(message)
	return string(runes[:36]) + "…"
}
