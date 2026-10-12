//go:build !darwin && !linux

package evergreencore

import "errors"

func writerProcessAlive(int) (bool, error) {
	return false, errors.New("process liveness checks are unsupported on this platform")
}
