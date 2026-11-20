package inference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var environment struct {
	once sync.Once
	err  error
}

func Initialize(libraryPath string) error {
	environment.once.Do(func() {
		if libraryPath == "" {
			if runtime.GOOS == "windows" {
				libraryPath = filepath.Join(".onnxruntime", "onnxruntime.dll")
			} else {
				libraryPath = filepath.Join(".onnxruntime", "libonnxruntime.so")
			}
		}
		if _, err := os.Stat(libraryPath); err != nil {
			environment.err = fmt.Errorf("ONNX Runtime shared library %q: %w", libraryPath, err)
			return
		}
		ort.SetSharedLibraryPath(libraryPath)
		environment.err = ort.InitializeEnvironment()
	})
	return environment.err
}

func DestroyEnvironment() error {
	if !ort.IsInitialized() {
		return nil
	}
	return ort.DestroyEnvironment()
}

type Session struct {
	native    *ort.DynamicAdvancedSession
	outputs   int
	closeOnce sync.Once
	closeErr  error
}

func OpenSession(ctx context.Context, libraryPath, modelPath string, inputNames, outputNames []string) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := Initialize(libraryPath); err != nil {
		return nil, err
	}
	native, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("load ONNX model %s: %w", modelPath, err)
	}
	if err := ctx.Err(); err != nil {
		_ = native.Destroy()
		return nil, err
	}
	return &Session{native: native, outputs: len(outputNames)}, nil
}

func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		if s.native != nil {
			s.closeErr = s.native.Destroy()
			s.native = nil
		}
	})
	return s.closeErr
}

type Tensor struct {
	Shape   []int64
	Float32 []float32
	Int64   []int64
}

func (s *Session) Run(ctx context.Context, inputs []ort.Value) ([]Tensor, error) {
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("ONNX session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, input := range inputs {
		if input == nil {
			return nil, fmt.Errorf("nil ONNX input")
		}
	}
	defer func() {
		for _, input := range inputs {
			_ = input.Destroy()
		}
	}()
	options, err := ort.NewRunOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	watchDone := make(chan struct{})
	watchExited := make(chan struct{})
	go func() {
		defer close(watchExited)
		select {
		case <-ctx.Done():
			_ = options.Terminate()
		case <-watchDone:
		}
	}()
	outputs := make([]ort.Value, s.outputs)
	runErr := s.native.RunWithOptions(inputs, outputs, options)
	close(watchDone)
	<-watchExited
	defer func() {
		for _, output := range outputs {
			if output != nil {
				_ = output.Destroy()
			}
		}
	}()
	if runErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ONNX inference canceled: %w", ctx.Err())
		}
		return nil, runErr
	}
	result := make([]Tensor, len(outputs))
	for i, value := range outputs {
		if value == nil {
			return nil, fmt.Errorf("ONNX output %d is nil", i)
		}
		switch tensor := value.(type) {
		case *ort.Tensor[float32]:
			result[i].Shape = append([]int64(nil), tensor.GetShape()...)
			result[i].Float32 = append([]float32(nil), tensor.GetData()...)
		case *ort.Tensor[int64]:
			result[i].Shape = append([]int64(nil), tensor.GetShape()...)
			result[i].Int64 = append([]int64(nil), tensor.GetData()...)
		default:
			return nil, fmt.Errorf("ONNX output %d has unsupported type %T", i, value)
		}
	}
	return result, nil
}
