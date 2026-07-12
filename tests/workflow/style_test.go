package workflowtest

import (
	"testing"

	"github.com/gopact-ai/gopact/gopacttest"
)

func TestCodeStyle(t *testing.T) {
	gopacttest.RequireCodeStyle(t, "../..")
}
