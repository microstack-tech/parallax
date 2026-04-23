package addrman

import (
	"errors"
	"os"
)

// Tiny file IO wrappers so addrman_test.go doesn't pull in os directly
// across many Go versions. Test-only.

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func isFutureSchemaErr(err error) bool {
	return errors.Is(err, ErrFutureSchema)
}
