// Package solver maps protocol-independent V4 puzzles onto the recognition
// pipelines in internal/inference and the model-free board/voice solvers.
package solver

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/kafuneri/captcha-tools-go/internal/board"
	"github.com/kafuneri/captcha-tools-go/internal/inference"
	"github.com/kafuneri/captcha-tools-go/internal/svgcaptcha"
	"github.com/kafuneri/captcha-tools-go/internal/v4"
	"github.com/kafuneri/captcha-tools-go/internal/vision"
	"github.com/kafuneri/captcha-tools-go/internal/voice"
)

// V4 solves one puzzle using the same candidate order as the reference
// registry: direction detection before symbol embedding, then text/phrase.
func V4(ctx context.Context, registry *inference.Registry, puzzle v4.Puzzle) (v4.Answer, error) {
	switch puzzle.Kind {
	case "silent":
		return v4.Answer{Kind: "silent"}, nil
	case "slide":
		offset, err := registry.SlideSolve(ctx, puzzle.Background, puzzle.Piece, puzzle.Y)
		if err != nil {
			return v4.Answer{}, err
		}
		return v4.Answer{Kind: "slide", Offset: offset}, nil
	case "grid":
		points, err := gridIndices(ctx, registry, puzzle)
		if err != nil {
			return v4.Answer{}, err
		}
		return v4.Answer{Kind: "grid", Indices: points}, nil
	case "click":
		points, err := clickPoints(ctx, registry, puzzle)
		if err != nil {
			return v4.Answer{}, err
		}
		return v4.Answer{Kind: "points", Points: points}, nil
	case "match", "gomoku":
		cells, err := board.Solve(puzzle.Kind, puzzle.Board)
		if err != nil {
			return v4.Answer{}, err
		}
		return v4.Answer{Kind: "board", Cells: [][2]int{{cells[0][0], cells[0][1]}, {cells[1][0], cells[1][1]}}}, nil
	case "voice":
		text, err := voice.Text(puzzle.Audio, puzzle.Language, puzzle.TemplateVersion)
		if err != nil {
			return v4.Answer{}, err
		}
		return v4.Answer{Kind: "text", Text: text}, nil
	case "svg":
		result, err := svgcaptcha.Solve(string(puzzle.Question), puzzle.Answers)
		if err != nil {
			return v4.Answer{}, err
		}
		return v4.Answer{
			Kind:     "svg",
			Layer:    result.Layer,
			Row:      result.Row,
			Col:      result.Col,
			Cols:     result.Cols,
			Passtime: svgPasstime(result.StartMS, result.EndMS),
		}, nil
	default:
		return v4.Answer{}, fmt.Errorf("Unsupported puzzle: %s", puzzle.Kind)
	}
}

// svgPasstime picks an interaction time inside the frame's visible window. The
// window ends up in the track duration, so the click is reported while its
// frame is on screen.
func svgPasstime(startMS, endMS int) int {
	low, high := startMS+50, endMS-50
	if high > low {
		return low + rand.Intn(high-low)
	}
	return 800 + rand.Intn(700)
}

func gridIndices(ctx context.Context, registry *inference.Registry, puzzle v4.Puzzle) ([]int, error) {
	if len(puzzle.Prompts) != 1 {
		return nil, fmt.Errorf("grid expects one prompt, got %d", len(puzzle.Prompts))
	}
	if len(puzzle.Cells) != 9 {
		return nil, fmt.Errorf("grid expects nine cells, got %d", len(puzzle.Cells))
	}
	prompt, err := vision.RGBFromBytes(puzzle.Prompts[0])
	if err != nil {
		return nil, fmt.Errorf("grid prompt: %w", err)
	}
	cells := make([]*vision.RGB, len(puzzle.Cells))
	for i, encoded := range puzzle.Cells {
		cell, err := vision.RGBFromBytes(encoded)
		if err != nil {
			return nil, fmt.Errorf("grid cell %d: %w", i, err)
		}
		cells[i] = cell
	}
	return registry.GridMatch(ctx, prompt, cells, puzzle.Count)
}

func clickPoints(ctx context.Context, registry *inference.Registry, puzzle v4.Puzzle) ([][2]float64, error) {
	switch puzzle.Style {
	case "text":
		points, err := registry.WordSolve(ctx, puzzle.Image, puzzle.Prompts)
		return convert(points), err
	case "phrase":
		points, err := registry.PhraseSolve(ctx, puzzle.Image, true)
		return convert(points), err
	default:
		if allDirectionPrompts(puzzle.Prompts) {
			prompts := make([]string, len(puzzle.Prompts))
			for i, prompt := range puzzle.Prompts {
				prompts[i] = string(prompt)
			}
			if points, err := registry.DirectionSolve(ctx, puzzle.Image, prompts); err == nil {
				return convert(points), nil
			}
			// Fall through to the embedding solver like the candidate loop.
		}
		points, err := registry.SymbolEmbeddingSolve(ctx, puzzle.Image, puzzle.Prompts, 1)
		return convert(points), err
	}
}

func allDirectionPrompts(prompts [][]byte) bool {
	if len(prompts) == 0 {
		return false
	}
	for _, prompt := range prompts {
		if !inference.DirectionPrompt(prompt) {
			return false
		}
	}
	return true
}

func convert(points []vision.Point) [][2]float64 {
	result := make([][2]float64, len(points))
	for i, point := range points {
		result[i] = [2]float64{point.X, point.Y}
	}
	return result
}
