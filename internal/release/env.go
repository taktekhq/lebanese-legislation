package release

import "os"

// Env is the environment variable name's value, or def when unset or empty.
func Env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// DefaultRepo is the dataset releases are published to and restored from.
const DefaultRepo = "taktekhq/lebanese-legislation"
