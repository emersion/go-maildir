package maildir

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"testing"
)

// cleanup removes a Dir's directory structure
func cleanup(errorfn func(...interface{}), d Dir) {
	err := os.RemoveAll(string(d))
	if err != nil {
		errorfn(err)
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
func makeDelivery(fatalfn func(...interface{}), d Dir, msg string) {
	del, err := d.NewDelivery()
	if err != nil {
		fatalfn(err)
	}
	_, err = del.Write([]byte(msg))
	if err != nil {
		fatalfn(err)
	}
	err = del.Close()
	if err != nil {
		fatalfn(err)
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()

	var d Dir = "test_create"
	err := d.Create()
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open("test_create")
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
	err = d.Create()
	if err != nil {
		t.Fatal(err)
	}

	defer cleanup(t.Error, d)
}

func TestDelivery(t *testing.T) {
	t.Parallel()

	var d Dir = "test_delivery"
	err := d.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t.Error, d)

	var msg = "this is a message"
	makeDelivery(t.Fatal, d, msg)

	keys, err := d.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	path, err := d.Filename(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if !exists(path) {
		t.Fatal("File doesn't exist")
	}

	if cat(t, path) != msg {
		t.Fatal("Content doesn't match")
	}
}

func TestPurge(t *testing.T) {
	t.Parallel()

	var d Dir = "test_purge"
	err := d.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t.Error, d)

	makeDelivery(t.Fatal, d, "foo")

	keys, err := d.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	path, err := d.Filename(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	err = d.Remove(keys[0])
	if err != nil {
		t.Fatal(err)
	}

	if exists(path) {
		t.Fatal("File still exists")
	}
}

func TestMove(t *testing.T) {
	t.Parallel()

	var d1 Dir = "test_move1"
	err := d1.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t.Error, d1)
	var d2 Dir = "test_move2"
	err = d2.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t.Error, d2)

	const msg = "a moving message"
	makeDelivery(t.Fatal, d1, msg)
	keys, err := d1.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	err = d1.Move(d2, keys[0])
	if err != nil {
		t.Fatal(err)
	}

	keys, err = d2.Keys()
	if err != nil {
		t.Fatal(err)
	}
	path, err := d2.Filename(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if cat(t, path) != msg {
		t.Fatal("Content doesn't match")
	}

}

func TestCopy(t *testing.T) {
	t.Parallel()
	var d1 Dir = "test_copy1"
	err := d1.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t.Error, d1)
	var d2 Dir = "test_copy2"
	err = d2.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t.Error, d2)
	const msg = "a moving message"
	makeDelivery(t.Fatal, d1, msg)
	keys, err := d1.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	if err = d1.SetFlags(keys[0], []Flag{FlagSeen}); err != nil {
		t.Fatal(err)
	}
	key2, err := d1.Copy(d2, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	path, err := d1.Filename(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if cat(t, path) != msg {
		t.Error("original content has changed")
	}
	path, err = d2.Filename(key2)
	if err != nil {
		t.Fatal(err)
	}
	if cat(t, path) != msg {
		t.Error("target content doesn't match source")
	}
	flags, err := d2.Flags(key2)
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

func BenchmarkFilename(b *testing.B) {
	// set up test maildir
	d := Dir("benchmark_filename")
	if err := d.Create(); err != nil {
		b.Fatalf("could not set up benchmark: %v", err)
	}
	defer cleanup(b.Error, d)

	// make 5000 deliveries
	for i := 0; i < 5000; i++ {
		makeDelivery(b.Fatal, d, fmt.Sprintf("here is message number %d", i))
	}

	// grab keys
	keys, err := d.Unseen()
	if err != nil {
		b.Fatal(err)
	}

	// shuffle keys
	rand.Shuffle(len(keys), func(i, j int) {
		keys[i], keys[j] = keys[j], keys[i]
	})

	// set some flags
	for i, key := range keys {
		var err error
		switch i % 5 {
		case 0:
			// no flags
			fallthrough
		case 1:
			err = d.SetFlags(key, []Flag{FlagSeen})
		case 2:
			err = d.SetFlags(key, []Flag{FlagSeen, FlagReplied})
		case 3:
			err = d.SetFlags(key, []Flag{FlagReplied})
		case 4:
			err = d.SetFlags(key, []Flag{FlagFlagged})
		}
		if err != nil {
			b.Fatal(err)
		}
	}

	// run benchmark for the first N shuffled keys
	keyIdx := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		_, err := d.Filename(keys[keyIdx])
		b.StopTimer()
		if err != nil {
			b.Errorf("could not get filename for key %s", keys[keyIdx])
		}
		keyIdx++
		if keyIdx >= len(keys) {
			keyIdx = 0
		}
	}
}
