package storage

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func SafeFilename(name string) (string, error) {
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) {
		return "", fmt.Errorf("unsafe server filename %q", name)
	}
	if strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, 0) || strings.ContainsAny(name, "<>:") || strings.ContainsRune(name, '"') || strings.ContainsAny(name, "|?*") {
		return "", fmt.Errorf("unsafe server filename %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("unsafe server filename %q", name)
		}
	}
	return name, nil
}

func FilenameFromResource(resource string) (string, error) {
	resource = strings.ReplaceAll(resource, "\\", "/")
	return SafeFilename(path.Base(resource))
}

type Store struct {
	root     string
	saveRoot string
}

func New(root, saveRoot string) (*Store, error) {
	for _, dir := range []string{root, saveRoot, filepath.Join(saveRoot, "img_pass"), filepath.Join(saveRoot, "img_fail")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: absolute, saveRoot: saveRoot}, nil
}

type Request struct {
	store        *Store
	ID           string
	Dir          string
	originalName string
}

func (s *Store) Begin() (*Request, error) {
	dir, err := os.MkdirTemp(s.root, "request-")
	if err != nil {
		return nil, err
	}
	return &Request{store: s, ID: strings.TrimPrefix(filepath.Base(dir), "request-"), Dir: dir}, nil
}

func (r *Request) WriteOriginal(name string, data []byte) error {
	safe, err := SafeFilename(name)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(r.Dir, safe), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	r.originalName = safe
	return nil
}

func (r *Request) WriteArtifact(name string, data []byte) error {
	safe, err := SafeFilename(name)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.Dir, safe), data, 0o600)
}

func (r *Request) Commit(pictureType string, passed bool) error {
	if r.originalName == "" {
		return fmt.Errorf("original image was not recorded")
	}
	class := "img_fail"
	if passed {
		class = "img_pass"
	}
	base := strings.SplitN(r.originalName, ".", 2)[0]
	artifactDir := filepath.Join(r.store.saveRoot, class, base+"-"+r.ID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return err
	}
	if err := r.copyOriginalUnique(); err != nil {
		return err
	}
	var artifacts []string
	switch pictureType {
	case "nine":
		for i := 0; i < 10; i++ {
			artifacts = append(artifacts, fmt.Sprintf("cropped_%d.jpg", i))
		}
	case "icon", "icon1":
		artifacts = []string{"icon_result.jpg"}
	default:
		return fmt.Errorf("unknown picture type %q", pictureType)
	}
	for _, artifact := range artifacts {
		source := filepath.Join(r.Dir, artifact)
		if _, err := os.Stat(source); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := copyExclusive(filepath.Join(artifactDir, artifact), source); err != nil {
			return err
		}
	}
	return nil
}

func (r *Request) copyOriginalUnique() error {
	ext := filepath.Ext(r.originalName)
	base := strings.TrimSuffix(r.originalName, ext)
	for attempt := 0; attempt < 1000; attempt++ {
		name := r.originalName
		if attempt == 1 {
			name = base + "-" + r.ID + ext
		}
		if attempt > 1 {
			name = fmt.Sprintf("%s-%s-%d%s", base, r.ID, attempt-1, ext)
		}
		err := copyExclusive(filepath.Join(r.store.saveRoot, name), filepath.Join(r.Dir, r.originalName))
		if os.IsExist(err) {
			continue
		}
		return err
	}
	return fmt.Errorf("could not allocate a unique saved-image name")
}

func copyExclusive(destination, source string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (r *Request) Cleanup() error {
	if r == nil || r.Dir == "" {
		return nil
	}
	parent, child := filepath.Dir(r.Dir), filepath.Base(r.Dir)
	if parent != r.store.root || !strings.HasPrefix(child, "request-") {
		return fmt.Errorf("refusing unsafe cleanup of %q", r.Dir)
	}
	return os.RemoveAll(r.Dir)
}
