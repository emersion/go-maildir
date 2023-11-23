//go:build windows

package maildir

// The separator separates a messages unique key from its flags in the filename.
// This should only be changed on operating systems where the colon isn't
// allowed in filenames.
const separator rune = ';'
