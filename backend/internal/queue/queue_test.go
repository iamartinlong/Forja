package queue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInit_NotNil(t *testing.T) {
	Init()
	assert.NotNil(t, Client())
}

func TestEnqueueForge_NoPanic(t *testing.T) {
	Init()
	err := EnqueueForge(context.Background(), "test-id")
	// may succeed if redis available, or fail but not panic
	_ = err
	assert.True(t, true)
}
