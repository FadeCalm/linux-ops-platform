//go:build !unix

package update

import "errors"

func Restart(exe string) error {
	return errors.New("self-update is only supported on unix")
}
