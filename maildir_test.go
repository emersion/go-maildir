package maildir

import (
	"io/ioutil"
	"os"
	"testing"
)

// cleanup removes a Dir's directory structure
func cleanup(t *testing.T, d Dir) {
	err := os.RemoveAll(string(d))
	if err != nil {
		t.Error(err)
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
func makeDelivery(t *testing.T, d Dir, msg string) {
	del, err := d.NewDelivery()
	if err != nil {
		t.Fatal(err)
	}
	_, err = del.Write([]byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	err = del.Close()
	if err != nil {
		t.Fatal(err)
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

	defer cleanup(t, d)
}

func TestDelivery(t *testing.T) {
	t.Parallel()

	var d Dir = "test_delivery"
	err := d.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d)

	var msg = "this is a message"
	makeDelivery(t, d, msg)

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
	defer cleanup(t, d)

	makeDelivery(t, d, "foo")

	keys, err := d.Unseen()
	if err != nil {
		t.Fatal(err)
	}
	path, err := d.Filename(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	err = d.Purge(keys[0])
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
	defer cleanup(t, d1)
	var d2 Dir = "test_move2"
	err = d2.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(t, d2)

	const msg = "a moving message"
	makeDelivery(t, d1, msg)
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
