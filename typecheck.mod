module github.com/kafuneri/captcha-tools-go

go 1.24.0

require (
	github.com/emmansun/gmsm v0.43.0
	github.com/hajimehoshi/go-mp3 v0.3.4
	github.com/pixiv/go-libjpeg v0.0.0-20161111043336-c49be700eba7
	github.com/refraction-networking/utls v1.8.2
	github.com/srwiley/oksvg v0.0.0-20261128051108-63f39d1445ed
	github.com/srwiley/rasterx v0.0.0-20261128054241-78f2104be592
	github.com/yalue/onnxruntime_go v1.38.0
)

require (
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/klauspost/compress v1.17.4 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/image v0.0.0-20211028202545-6944b10bf410 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/pixiv/go-libjpeg => ./.typecheck/stubs/go-libjpeg

replace github.com/yalue/onnxruntime_go => ./.typecheck/stubs/onnxruntime_go
