package domain

import (
	"strings"
	"time"
)

func NowUTC() time.Time {
	return time.Now().UTC()
}

func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func ParseTime(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
}

func ParseTimeUnix(raw string) (int64, bool) {
	parsed, err := ParseTime(raw)
	if err != nil {
		return 0, false
	}
	return parsed.Unix(), true
}
