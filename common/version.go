package common

import (
	"runtime/debug"
	"sync"
)

// modulePath is MoniGo's import path, used to find its own entry in the
// consuming binary's dependency list.
const modulePath = "github.com/iyashjayesh/monigo"

// devVersion is what the badge shows when there is no released version to
// report: a local checkout, or a replace directive pointing at the working
// tree. It is never shown for a real dependency.
const devVersion = "dev"

var (
	versionOnce sync.Once
	version     string
)

/*
LibraryVersion reports the version of MoniGo compiled into the running binary.

It is read from the build info rather than declared as a constant, because a
constant is a promise nobody keeps: the dashboard footer claimed v1.0.0 for four
releases after it stopped being true, since nothing in the build had any reason
to touch it. The linker already knows the answer.

Returns "dev" when the answer is not knowable -- MoniGo built from a local
checkout or through a replace directive, which is what happens while developing
it or running the examples. That is honest and keeps the badge from being an
empty box; it is never returned for a real dependency, because a real dependency
always has a resolved version.
*/
func LibraryVersion() string {
	versionOnce.Do(func() {
		version = devVersion
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return // no build info: keep "dev"
		}
		// As a dependency, MoniGo appears in Deps with a real version.
		for _, dep := range info.Deps {
			if dep.Path == modulePath {
				if dep.Replace != nil {
					return // replaced locally; keep "dev"
				}
				version = dep.Version
				return
			}
		}
		// Built as the main module -- developing MoniGo itself. Go reports
		// "(devel)" here, which is not a version anyone should see, so it stays
		// as "dev".
		if info.Main.Path == modulePath && info.Main.Version != "" &&
			info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	})
	return version
}
