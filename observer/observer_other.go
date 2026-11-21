//go:build !linux

package observer

import "errors"

// Run needs Linux fanotify; the observer always runs in a Linux container,
// also under Docker Desktop.
func Run(Config) error {
	return errors.New("the observer runs only on Linux")
}
