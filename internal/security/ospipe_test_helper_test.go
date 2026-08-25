package security

import "os"

// osPipe wraps os.Pipe for tests: the read end (*os.File) is registered with
// the runtime poller and supports SetReadDeadline, same as exec pipe readers.
func osPipe() (*os.File, *os.File, error) {
	return os.Pipe()
}
