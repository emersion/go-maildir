// Package maildirpp implements Maildir++.
package maildirpp

import (
	"errors"
	"io"
	"os"
	"strings"
)

const (
	separator    = '.'
	readdirChunk = 4096
)

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

// Walk calls fn for every Maildir++ subfolders of the root directory.
//
// It stops if fn returns a non-nil error.
func Walk(root string, fn func(key string) error) error {
	f, err := os.Open(root)
	if err != nil {
		return err
	}
	defer f.Close()

	for {
		dis, err := f.ReadDir(readdirChunk)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}

		for _, di := range dis {
			if di.IsDir() && di.Name()[0] == separator {
				if err := fn(di.Name()); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
