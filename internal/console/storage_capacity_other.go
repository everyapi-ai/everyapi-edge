//go:build !darwin && !linux

package console

// Edge bundles target Linux and macOS. Keep status inspection available on any other development platform even when its filesystem API has no Statfs.
func storageCapacity(string) (total, available int64, err error) {
	return 0, 0, nil
}
