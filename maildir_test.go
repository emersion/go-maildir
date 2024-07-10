package maildir

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// cleanup removes a Dir's directory structure
func cleanup(tb testing.TB, d Dir) {
	err := os.RemoveAll(string(d))
	if err != nil {
		tb.Error(err)
	}
}

// exists checks if the given path exists
func exists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	panic(err)
}

// cat returns the content of a file as a string
func cat(t *testing.T, path string) string {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := ioutil.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(c)
}

// makeDelivery creates a new message
func makeDelivery(tb testing.TB, d Dir, msg string) {
	del, err := NewDelivery(string(d))
	if err != nil {
		tb.Fatal(err)
	}
	_, err = del.Write([]byte(msg))
	if err != nil {
		tb.Fatal(err)
	}
	err = del.Close()
	if err != nil {
		tb.Fatal(err)
	}
}

func TestInit(t *testing.T) {
	t.Parallel()

	var d Dir = "test_init"
	err := d.Init()
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open("test_init")
	if err != nil {
		t.Fatal(err)
	}

	fis, err := f.Readdir(0)
	subdirs := make(map[string]os.FileInfo)
	for _, fi := range fis {
		if !fi.IsDir() {
			t.Errorf("%s was not a directory", fi.Name())
			continue
		}
		subdirs[fi.Name()] = fi
	}

	// Verify the directories have been created.
	if _, ok := subdirs["tmp"]; !ok {
		t.Error("'tmp' directory was not created")
	}
	if _, ok := subdirs["new"]; !ok {
		t.Error("'new' directory was not created")
	}
	if _, ok := subdirs["cur"]; !ok {
		t.Error("'cur' directory was not created")
	}

	// Make sure no error is returned if the directories already exist.
	err = d.Init()
	if err != nil {
		t.Fatal(err)
	}

	defer cleanup(t, d)
}

func TestDelivery(t *testing.T) {
	t.Parallel()

	var d Dir = "test_delivery"
	err := d.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d)

	var msg = "this is a message"
	makeDelivery(t, d, msg)

	msgs, err := d.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	path := msgs[0].Filename()
	if !exists(path) {
		t.Fatal("File doesn't exist")
	}

	if cat(t, path) != msg {
		t.Fatal("Content doesn't match")
	}
}

func TestDir_Create(t *testing.T) {
	t.Parallel()

	var d Dir = "test_create"
	err := d.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d)

	var text = "this is a message"
	msg, w, err := d.Create([]Flag{FlagFlagged})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := io.WriteString(w, text); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	msg, err = d.MessageByKey(msg.Key())
	if err != nil {
		t.Fatal(err)
	}

	flags := msg.Flags()
	if len(flags) != 1 || flags[0] != FlagFlagged {
		t.Errorf("Dir.Flags() = %v, want {FlagFlagged}", flags)
	}

	path := msg.Filename()
	if !exists(path) {
		t.Fatal("File doesn't exist")
	}
	if cat(t, path) != text {
		t.Fatal("Content doesn't match")
	}
}

func TestPurge(t *testing.T) {
	t.Parallel()

	var d Dir = "test_purge"
	err := d.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d)

	makeDelivery(t, d, "foo")

	msgs, err := d.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	path := msgs[0].Filename()
	if err := msgs[0].Remove(); err != nil {
		t.Fatal(err)
	}

	if exists(path) {
		t.Fatal("File still exists")
	}
}

func TestMove(t *testing.T) {
	t.Parallel()

	var d1 Dir = "test_move1"
	err := d1.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d1)
	var d2 Dir = "test_move2"
	err = d2.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d2)

	const msg = "a moving message"
	makeDelivery(t, d1, msg)
	msgs, err := d1.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	err = msgs[0].MoveTo(d2)
	if err != nil {
		t.Fatal(err)
	}

	msgs, err = d2.Messages()
	if err != nil {
		t.Fatal(err)
	}
	path := msgs[0].Filename()
	if cat(t, path) != msg {
		t.Fatal("Content doesn't match")
	}
}

func TestCopy(t *testing.T) {
	t.Parallel()
	var d1 Dir = "test_copy1"
	err := d1.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d1)
	var d2 Dir = "test_copy2"
	err = d2.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d2)
	const msg = "a moving message"
	makeDelivery(t, d1, msg)
	msgs, err := d1.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	if err = msgs[0].SetFlags([]Flag{FlagSeen}); err != nil {
		t.Fatal(err)
	}
	msgCopy, err := msgs[0].CopyTo(d2)
	if err != nil {
		t.Fatal(err)
	}
	path := msgs[0].Filename()
	if cat(t, path) != msg {
		t.Error("original content has changed")
	}
	path = msgCopy.Filename()
	if cat(t, path) != msg {
		t.Error("target content doesn't match source")
	}
	flags := msgCopy.Flags()
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 1 {
		t.Fatal("no flags on target")
	}
	if flags[0] != FlagSeen {
		t.Error("seen flag not present on target")
	}
}

func TestIllegal(t *testing.T) {
	t.Parallel()
	var d1 Dir = "test_illegal"
	err := d1.Init()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d1)
	const msg = "an illegal message"
	makeDelivery(t, d1, msg)
	msgs, err := d1.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	if err := msgs[0].SetFlags([]Flag{FlagSeen}); err != nil {
		t.Fatal(err)
	}
	path := msgs[0].Filename()
	if err := os.Rename(path, "test_illegal/cur/"+msgs[0].Key()); err != nil {
		t.Fatal(err)
	}
	err = d1.Walk(func(*Message) error {
		return nil
	})
	var mailfileErr *MailfileError
	if !errors.As(err, &mailfileErr) {
		t.Fatalf("want *MailfileError, but Walk() = %v", err)
	}
}

func TestFolderWithSquareBrackets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	name := "[Google Mail].All Mail"

	dir := Dir(filepath.Join(root, name))
	if err := dir.Init(); err != nil {
		t.Fatal(err)
	}

	key := func() string {
		msg, writer, err := dir.Create([]Flag{FlagPassed, FlagReplied})
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		_, err = writer.Write([]byte("this is a message"))
		if err != nil {
			t.Fatal(err)
		}
		return msg.Key()
	}()

	_, err := dir.MessageByKey(key)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedKeysAreUnique(t *testing.T) {
	t.Parallel()
	totalThreads := 10
	unique := sync.Map{}

	for thread := 0; thread < totalThreads; thread++ {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			total := 5000
			for i := 0; i < total; i++ {
				key, err := newKey()
				if err != nil {
					t.Fatalf("error generating key: %s", err)
				}
				if _, found := unique.Load(key); found {
					t.Fatalf("non unique key generated: %q", key)
				}
				unique.Store(key, true)
			}
		})
	}

}

func TestDifferentSizesOfReaddirChunks(t *testing.T) {
	totalFiles := 3
	// don't run this test in // as it modifies a package variable
	source := t.TempDir()

	dir := Dir(source)
	if err := dir.Init(); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < totalFiles; i++ {
		makeDelivery(t, dir, fmt.Sprintf("here is message number %d", i))
	}

	// grab unseen messages
	msgs, err := dir.Unseen()
	if err != nil {
		t.Fatal(err)
	}

	// avoid the filename guesser
	for _, msg := range msgs {
		err := msg.SetFlags([]Flag{FlagPassed, FlagReplied})
		if err != nil {
			t.Fatal(err)
		}
	}

	previousReaddirChunk := readdirChunk
	// set it back to normal for the following tests
	defer func() {
		readdirChunk = previousReaddirChunk
	}()

	// try different sizes of chunks
	for chunkSize := 0; chunkSize <= totalFiles+1; chunkSize++ {
		readdirChunk = chunkSize
		for _, msg := range msgs {
			found, err := dir.MessageByKey(msg.Key())
			if err != nil {
				t.Fatal(err)
			}
			if found.Key() != msg.Key() {
				t.Errorf("cannot find filename for key %q", msg.Key())
			}
		}
	}
}

func BenchmarkFilename(b *testing.B) {
	// set up test maildir
	d := Dir("benchmark_filename")
	if err := d.Init(); err != nil {
		b.Fatalf("could not set up benchmark: %v", err)
	}
	defer cleanup(b, d)

	// make 5000 deliveries
	for i := 0; i < 5000; i++ {
		makeDelivery(b, d, fmt.Sprintf("here is message number %d", i))
	}

	// grab unseen messages
	msgs, err := d.Unseen()
	if err != nil {
		b.Fatal(err)
	}

	// shuffle keys
	rand.Shuffle(len(msgs), func(i, j int) {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	})

	// set some flags
	for i, msg := range msgs {
		var err error
		switch i % 5 {
		case 0:
			// no flags
			fallthrough
		case 1:
			err = msg.SetFlags([]Flag{FlagSeen})
		case 2:
			err = msg.SetFlags([]Flag{FlagSeen, FlagReplied})
		case 3:
			err = msg.SetFlags([]Flag{FlagReplied})
		case 4:
			err = msg.SetFlags([]Flag{FlagFlagged})
		}
		if err != nil {
			b.Fatal(err)
		}
	}

	// run benchmark for the first N shuffled keys
	msgIdx := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		_, err := d.MessageByKey(msgs[msgIdx].Key())
		b.StopTimer()
		if err != nil {
			b.Errorf("could not get filename for key %s", msgs[msgIdx].Key())
		}
		msgIdx++
		if msgIdx >= len(msgs) {
			msgIdx = 0
		}
	}
}
