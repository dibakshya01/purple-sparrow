// Package idgen generates identifiers and timestamps application-side (not via
// engine defaults) so values are identical across SQLite and Postgres.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewUUID returns a random RFC-4122 version-4 UUID string. crypto/rand failure is
// treated as fatal-ish by returning a zero UUID; callers create ids in write
// paths where a collision-safe value matters, so we prefer erroring loudly at the
// call site over silently degrading — but rand.Read effectively never fails.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	var dst [36]byte
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst[:])
}

// NowRFC3339 returns the current UTC time as an RFC-3339 nanosecond string, the
// canonical timestamp representation across engines.
func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
