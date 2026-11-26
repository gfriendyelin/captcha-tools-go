package inference

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// GridResource holds the SigLIP-style vision backbone and the match head used
// by the Lotus GridMatcher (grid-vision.onnx / grid-head.onnx).
type GridResource struct {
	Vision *Session
	Head   *Session
}

func (r *GridResource) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.Head != nil {
		errs = append(errs, r.Head.Close())
	}
	if r.Vision != nil {
		errs = append(errs, r.Vision.Close())
	}
	return errors.Join(errs...)
}

// LoadGrid opens the two grid models. The Lotus models use input 'image' and
// output 'features' for the backbone; the head takes 'prompt_features' +
// 'cell_features' and returns 'logits'.
func LoadGrid(ctx context.Context, library, directory string) (*GridResource, error) {
	resource := &GridResource{}
	var err error
	resource.Vision, err = OpenSession(ctx, library, filepath.Join(directory, "grid-vision.onnx"), []string{"image"}, []string{"features"})
	if err != nil {
		return nil, fmt.Errorf("load grid-vision: %w", err)
	}
	resource.Head, err = OpenSession(ctx, library, filepath.Join(directory, "grid-head.onnx"), []string{"prompt_features", "cell_features"}, []string{"logits"})
	if err != nil {
		_ = resource.Vision.Close()
		return nil, fmt.Errorf("load grid-head: %w", err)
	}
	return resource, nil
}

// SymbolResource holds the symbol detector (YOLOX-style [1,3549,6] output) and
// the symbol embedding model (96x96 input, [N,4608] output).
type SymbolResource struct {
	Detect    *Session
	Embedding *Session
}

func (r *SymbolResource) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.Embedding != nil {
		errs = append(errs, r.Embedding.Close())
	}
	if r.Detect != nil {
		errs = append(errs, r.Detect.Close())
	}
	return errors.Join(errs...)
}

// LoadSymbols opens the symbol detect and embedding models.
func LoadSymbols(ctx context.Context, library, directory string) (*SymbolResource, error) {
	resource := &SymbolResource{}
	var err error
	resource.Detect, err = OpenSession(ctx, library, filepath.Join(directory, "symbol-detect.onnx"), []string{"images"}, []string{"output"})
	if err != nil {
		return nil, fmt.Errorf("load symbol-detect: %w", err)
	}
	resource.Embedding, err = OpenSession(ctx, library, filepath.Join(directory, "symbol-embedding.onnx"), []string{"input"}, []string{"output"})
	if err != nil {
		_ = resource.Detect.Close()
		return nil, fmt.Errorf("load symbol-embedding: %w", err)
	}
	return resource, nil
}

// DirectionResource holds the icon-direction detector ([1,12,8400] output).
type DirectionResource struct {
	Detect *Session
}

func (r *DirectionResource) Close() error {
	if r == nil || r.Detect == nil {
		return nil
	}
	return r.Detect.Close()
}

// LoadDirection opens the direction model.
func LoadDirection(ctx context.Context, library, directory string) (*DirectionResource, error) {
	session, err := OpenSession(ctx, library, filepath.Join(directory, "icon-direction.onnx"), []string{"images"}, []string{"output0"})
	if err != nil {
		return nil, err
	}
	return &DirectionResource{Detect: session}, nil
}

// WordResource holds the word/phrase detector and the recognition model.
type WordResource struct {
	Detect    *Session
	Recognize *Session
}

func (r *WordResource) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.Recognize != nil {
		errs = append(errs, r.Recognize.Close())
	}
	if r.Detect != nil {
		errs = append(errs, r.Detect.Close())
	}
	return errors.Join(errs...)
}

// LoadWord opens word-detect and text-recognize.
func LoadWord(ctx context.Context, library, directory string) (*WordResource, error) {
	resource := &WordResource{}
	var err error
	resource.Detect, err = OpenSession(ctx, library, filepath.Join(directory, "word-detect.onnx"), []string{"images"}, []string{"output"})
	if err != nil {
		return nil, fmt.Errorf("load word-detect: %w", err)
	}
	resource.Recognize, err = OpenSession(ctx, library, filepath.Join(directory, "text-recognize.onnx"), []string{"x"}, []string{"fetch_name_0"})
	if err != nil {
		_ = resource.Detect.Close()
		return nil, fmt.Errorf("load text-recognize: %w", err)
	}
	return resource, nil
}

// PhraseResource holds the phrase detector, recognizer, and optional ordering
// model (a small transformer). It may run without the ordering model.
type PhraseResource struct {
	Detect    *Session
	Recognize *Session
	Order     *Session
}

func (r *PhraseResource) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.Order != nil {
		errs = append(errs, r.Order.Close())
	}
	if r.Recognize != nil {
		errs = append(errs, r.Recognize.Close())
	}
	if r.Detect != nil {
		errs = append(errs, r.Detect.Close())
	}
	return errors.Join(errs...)
}

// LoadPhrase opens phrase-detect, phrase-recognize and (optionally) phrase-order.
func LoadPhrase(ctx context.Context, library, directory string, withOrder bool) (*PhraseResource, error) {
	resource := &PhraseResource{}
	var err error
	resource.Detect, err = OpenSession(ctx, library, filepath.Join(directory, "phrase-detect.onnx"), []string{"images"}, []string{"output0"})
	if err != nil {
		return nil, fmt.Errorf("load phrase-detect: %w", err)
	}
	resource.Recognize, err = OpenSession(ctx, library, filepath.Join(directory, "phrase-recognize.onnx"), []string{"x"}, []string{"fetch_name_0"})
	if err != nil {
		_ = resource.Detect.Close()
		return nil, fmt.Errorf("load phrase-recognize: %w", err)
	}
	if withOrder {
		resource.Order, err = OpenSession(ctx, library, filepath.Join(directory, "phrase-order.onnx"), []string{"input_ids", "attention_mask"}, []string{"logits"})
		if err != nil {
			_ = resource.Close()
			return nil, fmt.Errorf("load phrase-order: %w", err)
		}
	}
	return resource, nil
}
