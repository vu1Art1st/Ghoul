package llmrec

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Autumn-27/artex/internal/testdb"
)

func TestMain(m *testing.M) {
	if err := testdb.Configure(); err != nil && !errors.Is(err, testdb.ErrNotConfigured) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
