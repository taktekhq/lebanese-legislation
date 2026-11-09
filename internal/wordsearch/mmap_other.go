//go:build !unix

package wordsearch

import "os"

// mapFile reads path into memory where there is no mmap.
func mapFile(path string) ([]byte, error) { return os.ReadFile(path) }

func unmapFile([]byte) {}
