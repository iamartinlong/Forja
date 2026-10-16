package vector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmbed_Deterministic(t *testing.T) {
	v1 := Embed("hello")
	v2 := Embed("hello")
	assert.Equal(t, v1, v2)
	assert.Len(t, v1, Dim)
	// normalized
	var sum float32
	for _, v := range v1 {
		sum += v * v
	}
	assert.InDelta(t, 1.0, sum, 0.001)
}

func TestEmbed_Different(t *testing.T) {
	v1 := Embed("hello")
	v2 := Embed("world")
	assert.NotEqual(t, v1, v2)
}

func TestFormatVector(t *testing.T) {
	v := []float32{0.1, 0.2, 0.3}
	s := FormatVector(v)
	assert.Contains(t, s, "0.1")
	assert.Contains(t, s, "0.2")
}
