// The maildir package provides an interface to mailboxes in the Maildir format.
//
// Maildir mailboxes are designed to be safe for concurrent delivery. This
// means that at the same time, multiple processes can deliver to the same
// mailbox. However only one process can receive and read messages stored in
// the Maildir.
package maildir

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// readdirChunk represents the number of files to load at once from the mailbox
// when searching for a message
var readdirChunk = 4096

var id int64 = 10000

// A KeyError occurs when a key matches more or less than one message.
type KeyError struct {
	Key string // the (invalid) key
	N   int    // number of matches (!= 1)
}

func (e *KeyError) Error() string {
	return fmt.Sprintf("maildir: key %q matches %v files, expected exactly one", e.Key, e.N)
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

// Flag is a message flag.
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

func parseBasename(basename string) (key string, flags []Flag, err error) {
	split := strings.FieldsFunc(basename, func(r rune) bool {
		return r == separator
	})
	if len(split) < 2 {
		return "", nil, &MailfileError{basename}
	}
	key, info := split[0], split[1]

	switch {
	case len(info) < 2, info[1] != ',':
		return "", nil, &FlagError{info, false}
	case info[0] == '1':
		return "", nil, &FlagError{info, true}
	case info[0] != '2':
		return "", nil, &FlagError{info, false}
	}

	flags = []Flag(info[2:])
	sort.Sort(flagList(flags))

	return key, flags, nil
}

func formatBasename(key string, flags []Flag) string {
	info := "2,"
	sort.Sort(flagList(flags))
	for _, f := range flags {
		if []rune(info)[len(info)-1] != rune(f) {
			info += string(f)
		}
	}
	return key + string(separator) + info
}

type flagList []Flag

func (s flagList) Len() int           { return len(s) }
func (s flagList) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s flagList) Less(i, j int) bool { return s[i] < s[j] }

// Message represents a message in a Maildir.
type Message struct {
	filename string
	key      string
	flags    []Flag
}

// Filename returns the filesystem path to the message's file.
//
// The filename is not stable, it changes depending on the message flags.
func (msg *Message) Filename() string {
	return msg.filename
}

// Key returns the stable, unique identifier for the message.
func (msg *Message) Key() string {
	return msg.key
}

// Flags returns the message flags.
func (msg *Message) Flags() []Flag {
	return msg.flags
}

// SetFlags sets the message flags.
//
// Any duplicate flags are dropped, and flags are sorted before being saved.
func (msg *Message) SetFlags(flags []Flag) error {
	newBasename := formatBasename(msg.key, flags)
	_, flags, err := parseBasename(newBasename)
	if err != nil {
		return err
	}

	newFilename := filepath.Join(filepath.Dir(msg.filename), newBasename)
	if err := os.Rename(msg.filename, newFilename); err != nil {
		return err
	}
	msg.filename = newFilename
	msg.flags = flags
	return nil
}

// Open reads the contents of a message.
func (msg *Message) Open() (io.ReadCloser, error) {
	return os.Open(msg.filename)
}

// Remove deletes a message.
func (msg *Message) Remove() error {
	return os.Remove(msg.filename)
}

// MoveTo moves a message from this Maildir to another one.
//
// The message flags are preserved, but its key might change.
func (msg *Message) MoveTo(target Dir) error {
	newFilename := filepath.Join(string(target), "cur", filepath.Base(msg.filename))
	if err := os.Rename(msg.filename, newFilename); err != nil {
		return err
	}
	msg.filename = newFilename
	return nil
}

// CopyTo copies a message from this Maildir to another one.
//
// The copied message is returned. Its flags will be identical but its key
// might be different.
func (msg *Message) CopyTo(target Dir) (*Message, error) {
	src, err := msg.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	newMsg, dst, err := target.Create(msg.flags)
	if err != nil {
		return nil, err
	}
	defer dst.Close()

	if _, err = io.Copy(dst, src); err != nil {
		return nil, err
	}
	if err := dst.Close(); err != nil {
		return nil, err
	}

	return newMsg, nil
}

type tmpMessage struct {
	*os.File
	dest string
}

func (msg tmpMessage) Close() error {
	if err := msg.File.Close(); err != nil {
		return err
	}
	return os.Rename(msg.File.Name(), msg.dest)
}

// A Dir represents a single directory in a Maildir mailbox.
//
// Dir is used by programs receiving and reading messages from a Maildir. Only
// one process can perform these operations. Programs which only need to
// deliver new messages to the Maildir should use Delivery.
type Dir string

func (d Dir) newMessage(dir, basename string) (*Message, error) {
	key, flags, err := parseBasename(basename)
	if err != nil {
		return nil, err
	}

	return &Message{
		filename: filepath.Join(dir, basename),
		key:      key,
		flags:    flags,
	}, nil
}

// Unseen moves messages from new to cur and returns them.
// This means the messages are now known to the application.
func (d Dir) Unseen() ([]*Message, error) {
	f, err := os.Open(filepath.Join(string(d), "new"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var msgs []*Message
	for {
		names, err := f.Readdirnames(readdirChunk)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return msgs, err
		}
		for _, n := range names {
			if n[0] == '.' {
				continue
			}

			// Messages in new shouldn't have an info field, but some programs
			// (e.g. offlineimap) do that anyways. Discard the info field in
			// that case.
			key, _, _ := strings.Cut(n, string(separator))
			info := "2,"
			newBasename := key + string(separator) + info

			err = os.Rename(filepath.Join(string(d), "new", n),
				filepath.Join(string(d), "cur", newBasename))
			if err != nil {
				return msgs, err
			}

			msg, err := d.newMessage(filepath.Join(string(d), "cur"), newBasename)
			if err != nil {
				panic(err) // unreachable
			}

			msgs = append(msgs, msg)
		}
	}

	return msgs, nil
}

// UnseenCount returns the number of messages in new without looking at them.
func (d Dir) UnseenCount() (int, error) {
	f, err := os.Open(filepath.Join(string(d), "new"))
	if err != nil {
		return 0, err
	}
	defer f.Close()

	c := 0
	for {
		names, err := f.Readdirnames(readdirChunk)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return 0, err
		}

		for _, n := range names {
			if n[0] != '.' {
				c++
			}
		}
	}

	return c, nil
}

// Walk calls fn for every message.
//
// If Walk encounters a malformed entry, it accumulates errors and continues
// iterating. If fn returns an error, Walk stops and returns a new error that
// contains fn's error in its tree (and can be checked via errors.Is).
func (d Dir) Walk(fn func(*Message) error) error {
	f, err := os.Open(filepath.Join(string(d), "cur"))
	if err != nil {
		return err
	}
	defer f.Close()

	var formatErrs []error
	for {
		names, err := f.Readdirnames(readdirChunk)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}

		for _, n := range names {
			if n[0] == '.' {
				continue
			}

			msg, err := d.newMessage(f.Name(), n)
			if err != nil {
				formatErrs = append(formatErrs, err)
				continue
			}

			if err := fn(msg); err != nil {
				return errors.Join(append(formatErrs, err)...)
			}
		}
	}

	return errors.Join(formatErrs...)
}

// Messages returns a list of all messages in cur.
func (d Dir) Messages() ([]*Message, error) {
	var msgs []*Message
	err := d.Walk(func(msg *Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	return msgs, err
}

func (d Dir) filenameGuesses(key string) []string {
	filename := filepath.Join(string(d), "cur", key+string(separator)+"2,")
	return []string{
		filename,

		filename + string(FlagPassed),
		filename + string(FlagReplied),
		filename + string(FlagSeen),
		filename + string(FlagDraft),
		filename + string(FlagFlagged),

		filename + string(FlagFlagged) + string(FlagPassed),
		filename + string(FlagFlagged) + string(FlagPassed) + string(FlagSeen),
		filename + string(FlagFlagged) + string(FlagReplied),
		filename + string(FlagFlagged) + string(FlagReplied) + string(FlagSeen),
		filename + string(FlagFlagged) + string(FlagSeen),

		filename + string(FlagPassed),
		filename + string(FlagPassed) + string(FlagSeen),

		filename + string(FlagReplied) + string(FlagSeen),
	}
}

// filenameByKey returns the path to the file corresponding to the key.
func (d Dir) filenameByKey(key string) (string, error) {
	// before doing an expensive Glob, see if we can guess the path based on some
	// common flags
	for _, guess := range d.filenameGuesses(key) {
		if _, err := os.Stat(guess); err == nil {
			return guess, nil
		}
	}

	file, err := os.Open(filepath.Join(string(d), "cur"))
	if err != nil {
		return "", err
	}
	defer file.Close()

	// search for a valid candidate (in blocks of readdirChunk)
	for {
		names, err := file.Readdirnames(readdirChunk)
		if errors.Is(err, io.EOF) {
			// no match
			return "", &KeyError{key, 0}
		} else if err != nil {
			return "", err
		}

		for _, name := range names {
			if strings.HasPrefix(name, key+string(separator)) {
				return filepath.Join(file.Name(), name), nil
			}
		}
	}
}

// MessageByKey finds a message by key.
func (d Dir) MessageByKey(key string) (*Message, error) {
	filename, err := d.filenameByKey(key)
	if err != nil {
		return nil, err
	}
	dir, basename := filepath.Split(filename)
	return d.newMessage(dir, basename)
}

// newKey generates a new unique key as described in the Maildir specification.
// For the third part of the key (delivery identifier) it uses an internal
// counter, the process id and a cryptographical random number to ensure
// uniqueness among messages delivered in the same second.
func newKey() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	host = strings.Replace(host, "/", `\057`, -1)
	host = strings.Replace(host, string(separator), `\072`, -1)

	bs := make([]byte, 10)
	_, err = io.ReadFull(rand.Reader, bs)
	if err != nil {
		return "", err
	}

	key := fmt.Sprintf("%d.%d%d%x.%s",
		time.Now().Unix(),
		os.Getpid(),
		atomic.AddInt64(&id, 1),
		bs,
		host,
	)
	return key, nil
}

// Init creates the directory structure for a Maildir.
//
// If the main directory already exists, it tries to create the subdirectories
// in there. If an error occurs while creating one of the subdirectories, this
// function may leave a partially created directory structure.
func (d Dir) Init() error {
	dirnames := []string{
		string(d),
		filepath.Join(string(d), "tmp"),
		filepath.Join(string(d), "new"),
		filepath.Join(string(d), "cur"),
	}
	for _, name := range dirnames {
		if err := os.MkdirAll(name, 0700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

// Create inserts a new message into the Maildir.
func (d Dir) Create(flags []Flag) (*Message, io.WriteCloser, error) {
	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}

	tmpFilename := filepath.Join(string(d), "tmp", key)
	f, err := os.OpenFile(tmpFilename, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0666)
	if err != nil {
		return nil, nil, err
	}

	basename := formatBasename(key, flags)
	curFilename := filepath.Join(string(d), "cur", basename)

	flagsCopy := make([]Flag, len(flags))
	copy(flagsCopy, flags)

	return &Message{
		filename: curFilename,
		key:      key,
		flags:    flagsCopy,
	}, &tmpMessage{File: f, dest: curFilename}, err
}

// Clean removes old files from tmp and should be run periodically.
// This does not use access time but modification time for portability reasons.
func (d Dir) Clean() error {
	f, err := os.Open(filepath.Join(string(d), "tmp"))
	if err != nil {
		return err
	}
	defer f.Close()

	now := time.Now()
	for {
		names, err := f.Readdirnames(readdirChunk)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}

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
	filename := filepath.Join(d, "tmp", key)
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0666)
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
	newfile := filepath.Join(string(d.d), "new", d.key)
	if err = os.Rename(tmppath, newfile); err != nil {
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
	return os.Remove(tmppath)
}
