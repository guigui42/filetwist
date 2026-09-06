//go:build !unix

package storage

// FreeSpace reports available bytes. Platforms without a supported statfs call
// report the maximum so the free-space preflight never blocks development
// hosts. Deployment targets Linux, where the unix implementation is used.
func FreeSpace(dir string) (int64, error) {
	_ = dir
	return 1 << 62, nil
}
