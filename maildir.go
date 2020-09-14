// The maildir package provides an interface to mailboxes in the Maildir format.
//
// Maildir mailboxes are designed to be safe for concurrent delivery. This
// means that at the same time, multiple processes can deliver to the same
// mailbox. However only one process can receive and read messages stored in
// the Maildir.
package maildir

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The separator separates a messages unique key from its flags in the filename.
// This should only be changed on operating systems where the colon isn't
// allowed in filenames.
var separator rune = ':'

var id int64 = 10000

// createMode holds the permissions used when creating a directory.
const createMode = 0700

// A KeyError occurs when a key matches more or less than one message.
type KeyError struct {
	Key string // the (invalid) key
	N   int    // number of matches (!= 1)
}

func (e *KeyError) Error() string {
	return "maildir: key " + e.Key + " matches " + strconv.Itoa(e.N) + " files."
}

// A FlagError occurs when a non-standard info section is encountered.
type FlagError struct {
	Info         string // the encountered info section
	Experimental bool   // info section starts with 1
}

func (e *FlagError) Error() string {
	if e.Experimental {
		return "maildir: experimental info section encountered: " + e.Info[2:]
	}
	return "maildir: bad info section encountered: " + e.Info
}

// A MailfileError occurs when a mailfile has an invalid format
type MailfileError struct {
	Name string // the name of the mailfile
}

func (e *MailfileError) Error() string {
	return "maildir: invalid mailfile format: " + e.Name
}

// A Dir represents a single directory in a Maildir mailbox.
//
// Dir is used by programs receiving and reading messages from a Maildir. Only
// one process can perform these operations. Programs which only need to
// deliver new messages to the Maildir should use Delivery.
type Dir string

// Unseen moves messages from new to cur and returns their keys.
// This means the messages are now known to the application. To find out whether
// a user has seen a message, use Flags().
func (d Dir) Unseen() ([]string, error) {
	f, err := os.Open(filepath.Join(string(d), "new"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(0)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, n := range names {
		if n[0] != '.' {
			split := strings.FieldsFunc(n, func(r rune) bool {
				return r == separator
			})
			key := split[0]
			info := "2,"
			// Messages in new shouldn't have an info section but
			// we act as if, in case some other program didn't
			// follow the spec.
			if len(split) > 1 {
				info = split[1]
			}
			keys = append(keys, key)
			err = os.Rename(filepath.Join(string(d), "new", n),
				filepath.Join(string(d), "cur", key+string(separator)+info))
		}
	}
	return keys, err
}

// UnseenCount returns the number of messages in new without looking at them.
func (d Dir) UnseenCount() (int, error) {
	f, err := os.Open(filepath.Join(string(d), "new"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	names, err := f.Readdirnames(0)
	if err != nil {
		return 0, err
	}
	c := 0
	for _, n := range names {
		if n[0] != '.' {
			c += 1
		}
	}
	return c, nil
}

func parseKey(filename string) (string, error) {
	split := strings.FieldsFunc(filename, func(r rune) bool {
		return r == separator
	})

	if len(split) == 0 {
		return "", fmt.Errorf("Cannot parse key from filename %s", filename)
	}

	return split[0], nil
}

// Key returns the key for the given file path.
func (d Dir) Key(path string) (string, error) {
	if filepath.Dir(path) != string(d) {
		return "", fmt.Errorf("Filepath %s belongs to a different Maildir", path)
	}

	filename := filepath.Base(path)
	return parseKey(filename)
}

// Keys returns a slice of valid keys to access messages by.
func (d Dir) Keys() ([]string, error) {
	f, err := os.Open(filepath.Join(string(d), "cur"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(0)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, n := range names {
		if n[0] != '.' {
			key, err := parseKey(n)
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (d Dir) filenameGuesses(key string) []string {
	basename := filepath.Join(string(d), "cur", key+string(separator)+"2,")
	return []string{
		basename,

		basename + string(FlagPassed),
		basename + string(FlagReplied),
		basename + string(FlagSeen),
		basename + string(FlagDraft),
		basename + string(FlagFlagged),

		basename + string(FlagPassed),
		basename + string(FlagPassed) + string(FlagSeen),
		basename + string(FlagPassed) + string(FlagSeen) + string(FlagFlagged),
		basename + string(FlagPassed) + string(FlagFlagged),

		basename + string(FlagReplied) + string(FlagSeen),
		basename + string(FlagReplied) + string(FlagSeen) + string(FlagFlagged),
		basename + string(FlagReplied) + string(FlagFlagged),

		basename + string(FlagSeen) + string(FlagFlagged),
	}
}

// Filename returns the path to the file corresponding to the key.
func (d Dir) Filename(key string) (string, error) {
	// before doing an expensive Glob, see if we can guess the path based on some
	// common flags
	for _, guess := range d.filenameGuesses(key) {
		if _, err := os.Stat(guess); err == nil {
			return guess, nil
		}
	}
	matches, err := filepath.Glob(filepath.Join(string(d), "cur", key+"*"))
	if err != nil {
		return "", err
	}
	if n := len(matches); n != 1 {
		return "", &KeyError{key, n}
	}
	return matches[0], nil
}

// Open reads a message by key.
func (d Dir) Open(key string) (io.ReadCloser, error) {
	filename, err := d.Filename(key)
	if err != nil {
		return nil, err
	}
	return os.Open(filename)
}

type Flag rune

const (
	// The user has resent/forwarded/bounced this message to someone else.
	FlagPassed Flag = 'P'
	// The user has replied to this message.
	FlagReplied Flag = 'R'
	// The user has viewed this message, though perhaps he didn't read all the
	// way through it.
	FlagSeen Flag = 'S'
	// The user has moved this message to the trash; the trash will be emptied
	// by a later user action.
	FlagTrashed Flag = 'T'
	// The user considers this message a draft; toggled at user discretion.
	FlagDraft Flag = 'D'
	// User-defined flag; toggled at user discretion.
	FlagFlagged Flag = 'F'
)

type flagList []Flag

func (s flagList) Len() int           { return len(s) }
func (s flagList) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s flagList) Less(i, j int) bool { return s[i] < s[j] }

// Flags returns the flags for a message sorted in ascending order.
// See the documentation of SetFlags for details.
func (d Dir) Flags(key string) ([]Flag, error) {
	filename, err := d.Filename(key)
	if err != nil {
		return nil, err
	}
	split := strings.FieldsFunc(filename, func(r rune) bool {
		return r == separator
	})
	switch {
	case len(split) <= 1:
		return nil, &MailfileError{filename}
	case len(split[1]) < 2,
		split[1][1] != ',':
		return nil, &FlagError{split[1], false}
	case split[1][0] == '1':
		return nil, &FlagError{split[1], true}
	case split[1][0] != '2':
		return nil, &FlagError{split[1], false}
	}
	fl := flagList(split[1][2:])
	sort.Sort(fl)
	return []Flag(fl), nil
}

func formatInfo(flags []Flag) string {
	info := "2,"
	fl := flagList(flags)
	sort.Sort(fl)
	for _, f := range fl {
		if []rune(info)[len(info)-1] != rune(f) {
			info += string(f)
		}
	}
	return info
}

// SetFlags appends an info section to the filename according to the given flags.
// This function removes duplicates and sorts the flags, but doesn't check
// whether they conform with the Maildir specification.
func (d Dir) SetFlags(key string, flags []Flag) error {
	return d.SetInfo(key, formatInfo(flags))
}

// Set the info part of the filename.
// Only use this if you plan on using a non-standard info part.
func (d Dir) SetInfo(key, info string) error {
	filename, err := d.Filename(key)
	if err != nil {
		return err
	}
	err = os.Rename(filename, filepath.Join(string(d), "cur", key+
		string(separator)+info))
	return err
}

// newKey generates a new unique key as described in the Maildir specification.
// For the third part of the key (delivery identifier) it uses an internal
// counter, the process id and a cryptographical random number to ensure
// uniqueness among messages delivered in the same second.
func newKey() (string, error) {
	var key string
	key += strconv.FormatInt(time.Now().Unix(), 10)
	key += "."
	host, err := os.Hostname()
	if err != err {
		return "", err
	}
	host = strings.Replace(host, "/", "\057", -1)
	host = strings.Replace(host, string(separator), "\072", -1)
	key += host
	key += "."
	key += strconv.FormatInt(int64(os.Getpid()), 10)
	key += strconv.FormatInt(id, 10)
	atomic.AddInt64(&id, 1)
	bs := make([]byte, 10)
	_, err = io.ReadFull(rand.Reader, bs)
	if err != nil {
		return "", err
	}
	key += hex.EncodeToString(bs)
	return key, nil
}

// Init creates the directory structure for a Maildir.
//
// If the main directory already exists, it tries to create the subdirectories
// in there. If an error occurs while creating one of the subdirectories, this
// function may leave a partially created directory structure.
func (d Dir) Init() error {
	err := os.Mkdir(string(d), os.ModeDir|createMode)
	if err != nil && !os.IsExist(err) {
		return err
	}
	err = os.Mkdir(filepath.Join(string(d), "tmp"), os.ModeDir|createMode)
	if err != nil && !os.IsExist(err) {
		return err
	}
	err = os.Mkdir(filepath.Join(string(d), "new"), os.ModeDir|createMode)
	if err != nil && !os.IsExist(err) {
		return err
	}
	err = os.Mkdir(filepath.Join(string(d), "cur"), os.ModeDir|createMode)
	if err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}

// Move moves a message from this Maildir to another.
func (d Dir) Move(target Dir, key string) error {
	path, err := d.Filename(key)
	if err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(string(target), "cur", filepath.Base(path)))
}

// Copy copies the message with key from this Maildir to the target, preserving
// its flags, returning the newly generated key for the target maildir or an
// error.
func (d Dir) Copy(target Dir, key string) (string, error) {
	flags, err := d.Flags(key)
	if err != nil {
		return "", err
	}
	targetKey, err := d.copyToTmp(target, key)
	if err != nil {
		return "", err
	}
	tmpfile := filepath.Join(string(target), "tmp", targetKey)
	curfile := filepath.Join(string(target), "cur", targetKey+"2,")
	if err = os.Rename(tmpfile, curfile); err != nil {
		return "", err
	}
	if err = target.SetFlags(targetKey, flags); err != nil {
		return "", err
	}
	return targetKey, nil
}

// copyToTmp copies the message with key from d into a file in the target
// maildir's tmp directory with a new key, returning the newly generated key or
// an error.
func (d Dir) copyToTmp(target Dir, key string) (string, error) {
	rc, err := d.Open(key)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	targetKey, err := newKey()
	if err != nil {
		return "", err
	}
	tmpfile := filepath.Join(string(target), "tmp", targetKey)
	wc, err := os.OpenFile(tmpfile, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer wc.Close()
	if _, err = io.Copy(wc, rc); err != nil {
		return "", err
	}
	return targetKey, nil
}

// Create inserts a new message into the Maildir.
func (d Dir) Create(flags []Flag) (key string, w io.WriteCloser, err error) {
	key, err = newKey()
	if err != nil {
		return "", nil, err
	}
	name := key + string(separator) + formatInfo(flags)
	w, err = os.Create(filepath.Join(string(d), "cur", name))
	if err != nil {
		return "", nil, err
	}
	return key, w, err
}

// Remove removes the actual file behind this message.
func (d Dir) Remove(key string) error {
	f, err := d.Filename(key)
	if err != nil {
		return err
	}
	return os.Remove(f)
}

// Clean removes old files from tmp and should be run periodically.
// This does not use access time but modification time for portability reasons.
func (d Dir) Clean() error {
	f, err := os.Open(filepath.Join(string(d), "tmp"))
	if err != nil {
		return err
	}
	defer f.Close()
	names, err := f.Readdirnames(0)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(string(d), "tmp", n))
		if err != nil {
			continue
		}
		if now.Sub(fi.ModTime()).Hours() > 36 {
			err = os.Remove(filepath.Join(string(d), "tmp", n))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Delivery represents an ongoing message delivery to the mailbox. It
// implements the io.WriteCloser interface. On Close the underlying file is
// moved/relinked to new.
//
// Multiple processes can perform a delivery on the same Maildir concurrently.
type Delivery struct {
	file *os.File
	d    Dir
	key  string
}

// NewDelivery creates a new Delivery.
func NewDelivery(d string) (*Delivery, error) {
	key, err := newKey()
	if err != nil {
		return nil, err
	}
	del := &Delivery{}
	file, err := os.Create(filepath.Join(d, "tmp", key))
	if err != nil {
		return nil, err
	}
	del.file = file
	del.d = Dir(d)
	del.key = key
	return del, nil
}

// Write implements io.Writer.
func (d *Delivery) Write(p []byte) (int, error) {
	return d.file.Write(p)
}

// Close closes the underlying file and moves it to new.
func (d *Delivery) Close() error {
	tmppath := d.file.Name()
	err := d.file.Close()
	if err != nil {
		return err
	}
	err = os.Link(tmppath, filepath.Join(string(d.d), "new", d.key))
	if err != nil {
		return err
	}
	err = os.Remove(tmppath)
	if err != nil {
		return err
	}
	return nil
}

// Abort closes the underlying file and removes it completely.
func (d *Delivery) Abort() error {
	tmppath := d.file.Name()
	err := d.file.Close()
	if err != nil {
		return err
	}
	err = os.Remove(tmppath)
	if err != nil {
		return err
	}
	return nil
}
