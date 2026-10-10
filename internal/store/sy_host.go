package store

import (
	"os"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

// SYHost is the offline CLI adapter for the shared Evergreen core. It contains
// no semantic rules of its own; online SiYuan and offline eg therefore decode,
// validate, hash, and scan the same .sy representation.
type SYHost struct {
	registry *evergreencore.Registry
}

func NewSYHost(registry *evergreencore.Registry) *SYHost {
	if registry == nil {
		registry = evergreencore.DefaultRegistry()
	}
	return &SYHost{registry: registry}
}

func (h *SYHost) Decode(data []byte) (*evergreencore.SYDocument, error) {
	return evergreencore.DecodeSY(data, h.registry)
}

func (h *SYHost) Encode(base []byte, envelope *evergreencore.DocumentEnvelope) ([]byte, error) {
	return evergreencore.EncodeSY(base, envelope, h.registry)
}

func (h *SYHost) SemanticHash(data []byte) (string, error) {
	return evergreencore.SemanticHash(data)
}

func (h *SYHost) Scan(root string) (*evergreencore.LogicalIndex, error) {
	return evergreencore.ScanFS(os.DirFS(root), h.registry)
}
