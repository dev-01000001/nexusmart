package env

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvString returns the environment variable value or defaultVal when unset or empty.
func EnvString(key, defaultVal string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return defaultVal
}

// EnvInt returns the parsed integer environment variable or defaultVal when unset or invalid.
func EnvInt(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// EnvDuration returns the parsed duration environment variable or defaultVal when unset or invalid.
func EnvDuration(key string, defaultVal time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}

// EnvBool returns the parsed boolean environment variable or defaultVal when unset or invalid.
func EnvBool(key string, defaultVal bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}
