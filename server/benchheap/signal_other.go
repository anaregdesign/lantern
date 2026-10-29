//go:build !linux && !darwin

package benchheap

import "os"

func Signal() (os.Signal, error) { return nil, ErrInput }
