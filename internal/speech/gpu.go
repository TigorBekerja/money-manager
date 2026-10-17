package speech

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GPU describes an NVIDIA GPU found through nvidia-smi.
type GPU struct {
	Name   string
	Driver string
	CUDA   float64 // highest CUDA version the driver supports, 0 if unknown
}

// Usable reports whether the GPU build of whisper.cpp can run on it.
func (g GPU) Usable() bool { return g.Name != "" && g.CUDA >= minCUDA }

var cudaVersionRe = regexp.MustCompile(`CUDA Version:\s*([0-9]+\.[0-9]+)`)

// detectGPU runs nvidia-smi. It returns a zero GPU and no error if there is no NVIDIA GPU.
func detectGPU(ctx context.Context) (GPU, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name,driver_version", "--format=csv,noheader").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return GPU{}, nil
		}
		return GPU{}, err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	name, driver, _ := strings.Cut(first, ",")
	g := GPU{Name: strings.TrimSpace(name), Driver: strings.TrimSpace(driver)}

	if banner, err := exec.CommandContext(ctx, "nvidia-smi").Output(); err == nil {
		g.CUDA = parseCUDAVersion(string(banner))
	}
	return g, nil
}

func parseCUDAVersion(nvidiaSMI string) float64 {
	m := cudaVersionRe.FindStringSubmatch(nvidiaSMI)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}
