//go:build !linux || !amd64

package led

import "errors"

func OpenBus(Hardware) (Bus, error) {
	return nil, errors.New("hardware access requires Linux amd64; dry-run and tests work on other platforms")
}
