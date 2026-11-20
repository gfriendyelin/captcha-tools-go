package inference

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

type PDLResource struct{ Session *Session }

func (r *PDLResource) Close() error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Close()
}

type DFINEResource struct{ Session *Session }

func (r *DFINEResource) Close() error {
	if r == nil || r.Session == nil {
		return nil
	}
	return r.Session.Close()
}

type MultiResource struct{ YOLO, DINO, Atten *Session }

func (r *MultiResource) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.Atten != nil {
		errs = append(errs, r.Atten.Close())
	}
	if r.DINO != nil {
		errs = append(errs, r.DINO.Close())
	}
	if r.YOLO != nil {
		errs = append(errs, r.YOLO.Close())
	}
	return errors.Join(errs...)
}

func LoadPDL(ctx context.Context, library, directory string) (*PDLResource, error) {
	session, err := OpenSession(ctx, library, filepath.Join(directory, "PP-HGNetV2-B4.onnx"), []string{"x"}, []string{"save_infer_model/scale_0.tmp_0"})
	if err != nil {
		return nil, err
	}
	return &PDLResource{Session: session}, nil
}

func LoadDFINE(ctx context.Context, library, directory string) (*DFINEResource, error) {
	session, err := OpenSession(ctx, library, filepath.Join(directory, "d-fine-n.onnx"), []string{"images", "orig_target_sizes"}, []string{"labels", "boxes", "scores"})
	if err != nil {
		return nil, err
	}
	return &DFINEResource{Session: session}, nil
}

func LoadMulti(ctx context.Context, library, directory string) (_ *MultiResource, returnErr error) {
	resource := &MultiResource{}
	defer func() {
		if returnErr != nil {
			_ = resource.Close()
		}
	}()
	var err error
	resource.YOLO, err = OpenSession(ctx, library, filepath.Join(directory, "yolo11n.onnx"), []string{"images"}, []string{"output0"})
	if err != nil {
		return nil, fmt.Errorf("load Multi YOLO11n: %w", err)
	}
	resource.DINO, err = OpenSession(ctx, library, filepath.Join(directory, "dinov3-small.onnx"), []string{"pixel_values"}, []string{"last_hidden_state"})
	if err != nil {
		return nil, fmt.Errorf("load Multi DINOv3: %w", err)
	}
	resource.Atten, err = OpenSession(ctx, library, filepath.Join(directory, "atten.onnx"), []string{"patch_tokens"}, []string{"embedding"})
	if err != nil {
		return nil, fmt.Errorf("load Multi atten: %w", err)
	}
	return resource, nil
}
