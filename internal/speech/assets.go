// Package speech manages the local speech-to-text engine (whisper.cpp): checking what is
// installed, downloading and verifying it, and running whisper-server.
package speech

// Asset is a pinned download. Every download is verified against SHA256 before use.
type Asset struct {
	Name   string // file name
	Label  string // shown to the user
	URL    string
	Size   int64
	SHA256 string
	Zip    bool // extract after download
}

// Build pinned to whisper.cpp v1.9.4 (release b5130, 2026-09-11). Digests are from the
// GitHub release API; update all three fields together when upgrading.
const buildName = "whisper.cpp v1.9.4 (b5130)"

var (
	programGPU = Asset{
		Name:   "whisper-cublas-12.4.0-bin-x64.zip",
		Label:  "speech program (NVIDIA GPU version)",
		URL:    "https://github.com/ggml-org/whisper.cpp/releases/download/b5130/whisper-cublas-12.4.0-bin-x64.zip",
		Size:   674_539_285,
		SHA256: "af520ddd034d985b55dfeea3e465ed93653ba2aee1a55e865033edc548c272a7",
		Zip:    true,
	}
	programCPU = Asset{
		Name:   "whisper-bin-x64.zip",
		Label:  "speech program (CPU version)",
		URL:    "https://github.com/ggml-org/whisper.cpp/releases/download/b5130/whisper-bin-x64.zip",
		Size:   8_573_270,
		SHA256: "f9ec6c52a2e949b62ab51fa21d0d497958f9e41c3010c157c4e42932d5316f3c",
		Zip:    true,
	}
	// Model from a fixed revision of huggingface.co/ggerganov/whisper.cpp.
	modelTurbo = Asset{
		Name:   "ggml-large-v3-turbo-q5_0.bin",
		Label:  "speech model (Whisper large-v3-turbo)",
		URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-large-v3-turbo-q5_0.bin",
		Size:   574_041_195,
		SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2",
	}
)

// minCUDA is the CUDA version the GPU build needs from the NVIDIA driver.
const minCUDA = 12.4
