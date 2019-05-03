// Package maildirpp implements Maildir++.
package maildirpp

import (
	"errors"
	"strings"
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
