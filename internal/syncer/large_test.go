package syncer_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/paths"
	"github.com/yoanbernabeu/skillcrew/internal/state"
	"github.com/yoanbernabeu/skillcrew/internal/syncer"
	tu "github.com/yoanbernabeu/skillcrew/internal/testutil"
)

// F-UPD-07: a sync with nothing to change takes under 5 seconds, even on a
// registry of 10,000 plugins (it took seconds when loading the registry was
// quadratic in its size).
func TestNoChangeSyncOnALargeRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 10,000-plugin registry")
	}
	var gov strings.Builder
	gov.WriteString("org: acme\nskills:\n")
	for i := range 20 {
		fmt.Fprintf(&gov, "  p%d:s%d: required\n", i, i)
	}
	origin := tu.NewLargeOrigin(t, 10000, gov.String())
	p := paths.Paths{Home: t.TempDir()}
	ctx := context.Background()
	if _, err := gitx.Clone(ctx, origin.Dir, p.Registry()); err != nil {
		t.Fatal(err)
	}
	if err := (state.Config{Registry: origin.Dir, Ref: "main"}).Save(p.Config()); err != nil {
		t.Fatal(err)
	}
	s := syncer.New(p)
	sync := func(what string) time.Duration {
		t.Helper()
		start := time.Now()
		if _, err := s.Sync(ctx, true); err != nil {
			t.Fatal(err)
		}
		d := time.Since(start)
		t.Logf("%s: %s", what, d)
		return d
	}
	sync("first sync (20 skills)")
	if d := sync("no-change sync"); d > 5*time.Second {
		t.Fatalf("a no-change sync of a 10,000-plugin registry took %s", d)
	}
	s.TrustLocalTags = true
	if d := sync("no-change hook sync"); d > 5*time.Second {
		t.Fatalf("a no-change hook sync of a 10,000-plugin registry took %s", d)
	}
}
