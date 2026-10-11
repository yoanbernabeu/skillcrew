package gitx_test

import (
	"os"
	"testing"

	"github.com/yoanbernabeu/skillcrew/internal/testutil"
)

// TestMain keeps the developer's git configuration out of the tests.
func TestMain(m *testing.M) {
	testutil.IsolateGit()
	os.Exit(m.Run())
}
