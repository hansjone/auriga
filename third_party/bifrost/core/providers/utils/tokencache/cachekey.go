package tokencache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
)

func CacheKey(version string, fields ...string) string {
	var stack [512]byte
	buf := stack[:0]
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(version)))
	buf = append(buf, version...)
	for _, f := range fields {
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(f)))
		buf = append(buf, f...)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func NormalizePEM(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "\n") && strings.Contains(s, `\n`) {
		s = strings.ReplaceAll(s, `\n`, "\n")
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}
