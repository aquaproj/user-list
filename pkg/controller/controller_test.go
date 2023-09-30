package controller_test

import (
	"testing"

	"github.com/aquaproj/user-list/pkg/controller"
)

func TestNew(t *testing.T) {
	t.Parallel()
	c := controller.New()
	if c == nil {
		t.Fatal("controller must not be nil")
	}
}
