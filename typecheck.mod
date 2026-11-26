module github.com/kafuneri/captcha-tools-go

go 1.24.0

require (
	github.com/emmansun/gmsm v0.43.0
	github.com/hajimehoshi/go-mp3 v0.3.4
	github.com/pixiv/go-libjpeg v0.0.0-20161111043336-c49be700eba7
	github.com/yalue/onnxruntime_go v1.38.0
)

require golang.org/x/crypto v0.48.0 // indirect

replace github.com/pixiv/go-libjpeg => ./.typecheck/stubs/go-libjpeg

replace github.com/yalue/onnxruntime_go => ./.typecheck/stubs/onnxruntime_go
