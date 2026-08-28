package common

import (
	"runtime/debug"
	"sync"
)

// modulePath is MoniGo's import path, used to find its own entry in the
// consuming binary's dependency list.
const modulePath = "github.com/iyashjayesh/monigo"

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

Returns "" when the answer is not knowable -- MoniGo built from a local checkout
or with a replace directive, which is what happens while developing it. The
dashboard hides the badge in that case rather than showing a placeholder,
because "(devel)" in a released binary would be worse than no badge at all.
*/
func LibraryVersion() string {
	versionOnce.Do(func() {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		// As a dependency, MoniGo appears in Deps with a real version.
		for _, dep := range info.Deps {
			if dep.Path == modulePath {
				if dep.Replace != nil {
					return // replaced locally; the version is not meaningful
				}
				version = dep.Version
				return
			}
		}
		// Built as the main module -- developing MoniGo itself. Go reports
		// "(devel)" here, which is not a version anyone should see.
		if info.Main.Path == modulePath && info.Main.Version != "" &&
			info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	})
	return version
}
