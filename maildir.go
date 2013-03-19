// The maildir package provides an interface to mailboxes in the Maildir format.
package maildir

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The Separator separates a messages unique key from its flags in the filename.
// This should only be changed on operating systems where the colon isn't
// allowed in filenames.
var Separator rune = ':'

var id int64 = 10000

// CreateMode is holds the permissions used when creating a directory.
const CreateMode = 0700

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

// A Dir represents a single directory in a Maildir mailbox.
type Dir string

// Unseen moves messages from new to cur (they are now "seen") and returns their keys.
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
				return r == Separator
			})
			keys = append(keys, split[0])
			err = os.Rename(filepath.Join(string(d), "new", n),
				filepath.Join(string(d), "cur", n+string(Separator)+"2,S"))
		}
	}
	return keys, err
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
			split := strings.FieldsFunc(n, func(r rune) bool {
				return r == Separator
			})
			keys = append(keys, split[0])
		}
	}
	return keys, nil
}

// Filename returns the path to the file corresponding to the key.
func (d Dir) Filename(key string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(string(d), "cur", key+"*"))
	if err != nil {
		return "", err
	}
	if n := len(matches); n != 1 {
		return "", &KeyError{key, n}
	}
	return matches[0], nil
}

// Header returns the corresponding mail header to a key.
func (d Dir) Header(key string) (header mail.Header, err error) {
	filename, err := d.Filename(key)
	if err != nil {
		return
	}
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()
	tp := textproto.NewReader(bufio.NewReader(file))
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return
	}
	header = mail.Header(hdr)
	return
}

// Message returns a Message by key.
func (d Dir) Message(key string) (*mail.Message, error) {
	filename, err := d.Filename(key)
	if err != nil {
		return &mail.Message{}, err
	}
	r, err := os.Open(filename)
	if err != nil {
		return &mail.Message{}, err
	}
	defer r.Close()
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, r)
	if err != nil {
		return &mail.Message{}, err
	}
	msg, err := mail.ReadMessage(buf)
	if err != nil {
		return msg, err
	}
	return msg, nil
}

type runeSlice []rune

func (s runeSlice) Len() int           { return len(s) }
func (s runeSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s runeSlice) Less(i, j int) bool { return s[i] < s[j] }

// Flags returns the flags for a message sorted in ascending order.
func (d Dir) Flags(key string) ([]rune, error) {
	filename, err := d.Filename(key)
	if err != nil {
		return nil, err
	}
	split := strings.FieldsFunc(filename, func(r rune) bool {
		return r == Separator
	})
	switch {
	case len(split[1]) < 2,
		split[1][1] != ',':
		return nil, &FlagError{split[1], false}
	case split[1][0] == '1':
		return nil, &FlagError{split[1], true}
	case split[1][0] != '2':
		return nil, &FlagError{split[1], false}
	}
	rs := runeSlice(split[1][2:])
	sort.Sort(rs)
	return []rune(rs), nil
}

// SetFlags appends an info section to the filename according to the given flags.
// This function removes duplicates and sorts the flags, but doesn't check
// wether they conform with the Maildir specification.
func (d Dir) SetFlags(key string, flags []rune) error {
	info := "2,"
	rs := runeSlice(flags)
	sort.Sort(rs)
	for _, r := range rs {
		if []rune(info)[len(info)-1] != r {
			info += string(r)
		}
	}
	return d.SetInfo(key, info)
}

// Set the info part of the filename.
// Only use this if you plan on using a non-standard info part.
func (d Dir) SetInfo(key, info string) error {
	filename, err := d.Filename(key)
	if err != nil {
		return err
	}
	err = os.Rename(filename, filepath.Join(string(d), "cur", key+
		string(Separator)+info))
	return err
}

// Key generates a new unique key as described in the Maildir specification.
// For the third part of the key (delivery identifier) it uses an internal
// counter, the process id and a cryptographical random number to ensure
// uniqueness among messages delivered in the same second.
func Key() (string, error) {
	var key string
	key += strconv.FormatInt(time.Now().Unix(), 10)
	key += "."
	host, err := os.Hostname()
	if err != err {
		return "", err
	}
	host = strings.Replace(host, "/", "\057", -1)
	host = strings.Replace(host, string(Separator), "\072", -1)
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

// Create creates the directory structure for a Maildir.
// If an error occurs while creating one of the subdirectories, this function
// may leave a partially created directory structure.
func (d Dir) Create() error {
	err := os.Mkdir(string(d), os.ModeDir|CreateMode)
	if err != nil {
		return err
	}
	err = os.Mkdir(filepath.Join(string(d), "tmp"), os.ModeDir|CreateMode)
	if err != nil {
		return err
	}
	err = os.Mkdir(filepath.Join(string(d), "new"), os.ModeDir|CreateMode)
	if err != nil {
		return err
	}
	err = os.Mkdir(filepath.Join(string(d), "cur"), os.ModeDir|CreateMode)
	if err != nil {
		return err
	}
	return nil
}

// Delivery represents an ongoing message delivery to the mailbox.
// It implements the WriteCloser interface. On closing the underlying file is
// moved/relinked to new.
type Delivery struct {
	file *os.File
	d    Dir
	key  string
}

// NewDelivery creates a new Delivery.
func (d Dir) NewDelivery() (*Delivery, error) {
	key, err := Key()
	if err != nil {
		return nil, err
	}
	file, err := os.Create(filepath.Join(string(d), "tmp", key))
	if err != nil {
		return nil, err
	}
	return &Delivery{file, d, key}, nil
}

func (d *Delivery) Write(p []byte) (int, error) {
	return d.file.Write(p)
}

// Close closes the underlying file and moves it to new.
func (d *Delivery) Close() error {
	err := d.file.Close()
	if err != nil {
		return err
	}
	err = os.Link(filepath.Join(string(d.d), "tmp", d.key),
		filepath.Join(string(d.d), "new", d.key))
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(string(d.d), "tmp", d.key))
	if err != nil {
		return err
	}
	return nil
}

// Move moves a message from this Maildir to another.
func (d Dir) Move(target Dir, key string) error {
	return os.Rename(filepath.Join(string(d), "cur", key),
		filepath.Join(string(target), "cur", key))
}

// Purge removes the actual file behind this message.
func (d Dir) Purge(key string) error {
	return os.Remove(filepath.Join(string(d), "cur", key))
}
