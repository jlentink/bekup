package config

import (
	"strings"
	"time"
	_ "time/tzdata" // the embedded zone database: host independent (D-028)
)

// ValidTimezone reports whether name is an explicit IANA zone of the
// embedded zone database: not empty, not "Local", at most 64 characters
// of letters, digits and / _ + -. It mirrors the scheduler's zone rule
// (scheduler.LoadZone, which the notification service re-checks at
// startup); config cannot import the scheduler.
func ValidTimezone(name string) bool {
	if name == "" || name == "Local" || len(name) > 64 || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
