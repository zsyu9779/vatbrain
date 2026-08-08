package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApiClampWeight(t *testing.T) {
	assert.Equal(t, 0.0, clampWeight(-1.0))
	assert.Equal(t, 0.0, clampWeight(-0.1))
	assert.Equal(t, 0.0, clampWeight(0.0))
	assert.Equal(t, 0.5, clampWeight(0.5))
	assert.Equal(t, 1.0, clampWeight(1.0))
	assert.Equal(t, 1.0, clampWeight(1.5))
	assert.Equal(t, 1.0, clampWeight(100.0))
}
