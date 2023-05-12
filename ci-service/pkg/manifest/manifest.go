package manifest

import (
	"fmt"
	"os"
	"path/filepath"
)

func ScanToml(fs string) ([]string, error) {

	var files []string

	root := fs

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {

		if err != nil {

			fmt.Println(err)
			return err
		}

		if !info.IsDir() && filepath.Ext(path) == ".toml" {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

func ScanDocker(fs string) ([]string, error) {
	var files []string

	root := fs

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {

		if err != nil {

			fmt.Println(err)
			return err
		}

		if !info.IsDir() && info.Name() == "Dockerfile" {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}
