package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func GetRootPath() string {

	dir, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("Failed to get current working directory: %v", err))
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	commonPaths := []string{
		"/app",
		"./",
		"../",
	}

	for _, path := range commonPaths {
		if _, err := os.Stat(filepath.Join(path, "config.yaml")); err == nil {
			absPath, _ := filepath.Abs(path)
			return absPath
		}
	}

	panic("Could not find project root directory (config.yaml not found while traversing up the directory tree)")
}

func MkdirIfNotExists(path string) error {
	if path == "" {
		return errors.New("path cannot be empty")
	}

	path = filepath.Clean(path)

	if filepath.IsAbs(path) {
		if filepath.Ext(path) != "" {
			path = filepath.Dir(path)
		}
	} else {
		var rootPath string
		func() {
			defer func() {
				if r := recover(); r != nil {
					if wd, err := os.Getwd(); err == nil {
						rootPath = wd
					} else {
						rootPath = "."
					}
				}
			}()
			rootPath = GetRootPath()
		}()

		if filepath.Ext(path) != "" {
			path = filepath.Dir(path)
		}

		path = filepath.Join(rootPath, path)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.MkdirAll(path, 0755)
	}
	return nil
}
