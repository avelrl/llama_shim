package domain

import "time"

func NowUTC() time.Time {
	return time.Now().UTC()
}

func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
