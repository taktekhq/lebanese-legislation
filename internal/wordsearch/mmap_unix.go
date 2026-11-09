//go:build unix

package wordsearch

import (
	"os"
	"syscall"
)

// mapFile maps path read-only: its pages are read when touched and may be
// dropped again by the kernel, like any file's.
func mapFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 {
		return nil, errBadFile
	}
	return syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
}

func unmapFile(b []byte) { syscall.Munmap(b) }
