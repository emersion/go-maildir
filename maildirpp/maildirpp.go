// Package maildirpp implements Maildir++.
package maildirpp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/emersion/go-maildir"
)

const separator = '.'

func Split(key string) ([]string, error) {
	if len(key) == 0 || key[0] != '.' {
		return nil, errors.New("maildirpp: invalid key")
	}
	return strings.Split(key, string(separator))[1:], nil
}

func Join(elems []string) (key string, err error) {
	for _, d := range elems {
		if strings.ContainsRune(d, separator) {
			return "", errors.New("maildirpp: directory name cannot contain a dot")
		}
	}
	return "." + strings.Join(elems, string(separator)), nil
}

func Dir(root, key string) maildir.Dir {
	return maildir.Dir(filepath.Join(root, key))
}

// Walk calls fn for every Maildir++ subfolders of the root directory.
//
// It stops if fn returns a non-nil error.
func Walk(root string, fn func(key string) error) error {
	f, err := os.Open(root)
	if err != nil {
		return err
	}

	dis, err := f.ReadDir(0)
	f.Close()
	if err != nil {
		return err
	}

	for _, di := range dis {
		if di.IsDir() && di.Name()[0] == separator {
			if err := fn(di.Name()); err != nil {
				return err
			}
		}
	}

	return nil
}
