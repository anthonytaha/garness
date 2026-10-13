//go:build !darwin && !linux

package session

import "fmt"

func lockSessionFile(path string) (func(), error) {
	return nil, fmt.Errorf("session locking requires macOS or Linux")
}
