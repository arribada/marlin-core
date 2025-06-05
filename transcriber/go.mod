module github.com/arribada/marlin-core/transcriber

go 1.22

require (
	github.com/arribada/marlin-core/common v0.1.0
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0-20240409172755-8f253ef3af1c
	github.com/go-audio/audio v1.0.0
	github.com/go-audio/wav v1.1.0
)

require github.com/go-audio/riff v1.0.0 // indirect

replace (
	github.com/arribada/marlin-core/common => ../common
	github.com/ggerganov/whisper.cpp/bindings/go => ./whisper.cpp/bindings/go
)
