package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSafeFilename(t *testing.T) {
	for _, name := range []string{"../x.jpg", "..\\x.jpg", "a/b.jpg", "a\\b.jpg", "", ".", "..", "a:b.jpg", "nul\x00.jpg"} {
		if _, err := SafeFilename(name); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
	name, err := FilenameFromResource("/80/2023/icon/picture.jpg")
	if err != nil || name != "picture.jpg" {
		t.Fatalf("FilenameFromResource = %q, %v", name, err)
	}
}

func TestConcurrentRequestsDoNotOverwrite(t *testing.T) {
	root := t.TempDir()
	store, err := New(filepath.Join(root, "work"), filepath.Join(root, "saved"))
	if err != nil {
		t.Fatal(err)
	}
	const count = 8
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := store.Begin()
			if err != nil {
				errs <- err
				return
			}
			defer req.Cleanup()
			if err := req.WriteOriginal("same.jpg", []byte(req.ID)); err != nil {
				errs <- err
				return
			}
			if err := req.WriteArtifact("cropped_0.jpg", []byte(req.ID)); err != nil {
				errs <- err
				return
			}
			if err := req.Commit("nine", true); err != nil {
				errs <- err
				return
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "saved", "img_pass"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != count {
		t.Fatalf("got %d artifact directories, want %d", len(entries), count)
	}
}
