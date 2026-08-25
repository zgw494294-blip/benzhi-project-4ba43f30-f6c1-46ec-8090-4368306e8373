package main

import (
	"os"
	"path/filepath"
)

func prepareDatabaseDirectory(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0755)
}
