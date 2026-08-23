package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
)

type Generator interface {
	New(prefix string) (string, error)
}

type Random struct{}

func (Random) New(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

type Sequence struct {
	n atomic.Uint64
}

func (s *Sequence) New(prefix string) (string, error) {
	return fmt.Sprintf("%s_%08d", prefix, s.n.Add(1)), nil
}
