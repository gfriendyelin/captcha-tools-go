package voice

import (
	"math"
	"testing"
)

func TestFFTMatchesDirectDFT(t *testing.T) {
	const n = 64
	input := make([]float64, n)
	for i := range input {
		input[i] = math.Sin(float64(i)*0.3) + 0.5*math.Cos(float64(i)*0.11)
	}
	out := make([]complex128, n)
	fft(input, out)
	for k := 0; k < n; k++ {
		var re, im float64
		for j := 0; j < n; j++ {
			angle := -2 * math.Pi * float64(j*k) / float64(n)
			re += input[j] * math.Cos(angle)
			im += input[j] * math.Sin(angle)
		}
		if math.Abs(real(out[k])-re) > 1e-9 || math.Abs(imag(out[k])-im) > 1e-9 {
			t.Fatalf("bin %d = %v, want (%v,%v)", k, out[k], re, im)
		}
	}
}

func TestVoiceFeatureShape(t *testing.T) {
	audio := make([]float32, 5000)
	for i := range audio {
		audio[i] = float32(0.3 * math.Sin(float64(i)*0.05))
	}
	feature := voiceFeature(audio)
	if len(feature) != 39 {
		t.Fatalf("feature length = %d, want 39", len(feature))
	}
	for i, value := range feature {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("feature[%d] = %v", i, value)
		}
	}
}

func TestVoiceFeatureShortAudio(t *testing.T) {
	feature := voiceFeature([]float32{1})
	if len(feature) != 39 {
		t.Fatalf("feature length = %d, want 39", len(feature))
	}
	for _, value := range feature {
		if value != 0 {
			t.Fatalf("short audio feature = %v, want zeros", value)
		}
	}
}

func TestSplitSpeech(t *testing.T) {
	const toneSeconds = 0.4
	// Gaps must exceed the 0.18s merge threshold plus the 2048-sample analysis
	// window smear (~0.13s) so the seven tones stay separate.
	const gapSeconds = 0.4
	audio := make([]float32, 0, sampleRate*4)
	audio = append(audio, make([]float32, sampleRate/10)...)
	for i := 0; i < 7; i++ {
		for j := 0; j < int(sampleRate*toneSeconds); j++ {
			audio = append(audio, float32(0.5*math.Sin(2*math.Pi*300*float64(j)/sampleRate)))
		}
		if i != 6 {
			audio = append(audio, make([]float32, int(sampleRate*gapSeconds))...)
		}
	}
	spans := splitSpeech(audio)
	if len(spans) != 7 {
		t.Fatalf("spans = %d, want 7", len(spans))
	}
	for i, span := range spans {
		if span[0] < 0 || span[1] > len(audio) || span[1] <= span[0] {
			t.Fatalf("span %d = %v out of range", i, span)
		}
		if i > 0 && span[0] < spans[i-1][1] {
			t.Fatalf("span %d overlaps previous", i)
		}
	}
}

func TestSplitSpeechSilence(t *testing.T) {
	if spans := splitSpeech(make([]float32, sampleRate)); len(spans) != 0 {
		t.Fatalf("silence spans = %v, want none", spans)
	}
}

func TestLoadTemplates(t *testing.T) {
	for _, version := range []string{"legacy", "v4_voice_20261012"} {
		table, err := loadTemplate(version)
		if err != nil {
			t.Fatalf("%s: %v", version, err)
		}
		if len(table.y) != 10 {
			t.Fatalf("%s: y length = %d, want 10", version, len(table.y))
		}
		centroids, ok := table.centroids["zho"]
		if !ok || len(centroids) != 10 {
			t.Fatalf("%s: zho centroids missing", version)
		}
		if len(centroids[0]) != 39 || len(table.prompts["eng"]) != 39 {
			t.Fatalf("%s: feature width != 39", version)
		}
	}
}

func TestInvalidTemplate(t *testing.T) {
	if _, err := Text(nil, "", "bad version!"); err == nil {
		t.Fatal("expected an error for an invalid template version")
	}
	if _, err := Text(nil, "", "unknown"); err == nil {
		t.Fatal("expected an error for an unknown template version")
	}
}

func TestDecodeFailure(t *testing.T) {
	if _, err := Text([]byte("not an mp3"), "", "legacy"); err == nil {
		t.Fatal("expected a decode error")
	}
}
