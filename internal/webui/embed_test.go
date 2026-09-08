package webui

import (
	"io/fs"
	"testing"
)

func TestFSContainsIndex(t *testing.T) {
	ui, err := FS()
	if err != nil {
		t.Fatal(err)
	}
	f, err := ui.Open("index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Fatal("index.html is a directory")
	}
	if _, err := fs.Stat(ui, "index.html"); err != nil {
		t.Fatal(err)
	}
}
