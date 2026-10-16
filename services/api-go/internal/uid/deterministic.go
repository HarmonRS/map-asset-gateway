package uid

import (
	"crypto/sha1"
	"fmt"
	"strings"
)

// Deterministic returns a stable UUID-like identifier derived from the input.
func Deterministic(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "::")))
	b := sum[:16]

	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	)
}
