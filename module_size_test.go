package monigo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mediaExtensions are the file types that belong to documentation rather than
// to the library.
var mediaExtensions = map[string]bool{
	".gif": true, ".mp4": true, ".mov": true, ".webm": true, ".avi": true,
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".bmp": true,
	".tiff": true, ".psd": true, ".sketch": true, ".fig": true,
}

// maxDocMediaBytes is the ceiling for a single documentation image tracked
// outside the embedded dashboard.
//
// Anything is really too much -- see below -- but a small badge or diagram is
// not worth failing a build over. This catches the case that actually hurt.
const maxDocMediaBytes = 256 * 1024

// TestDocumentationMediaDoesNotShipInTheModule keeps README assets out of the
// Go module zip.
//
// Everything at the module root is included in the zip that every `go get`
// downloads, and the compiler never looks at any of it. monigo.gif was 24.7 MB
// of a 25 MB zip: 96% of what every consumer fetched, on every CI run, was a
// README image -- one that had gone stale and showed a dashboard the project
// no longer ships.
//
// Documentation media belongs on the `assets` branch, which is never tagged
// and so is not part of any module version. Reference it from the README by
// absolute raw.githubusercontent URL; GitHub renders it identically.
//
// static/ is exempt: those files are served by the dashboard at runtime and
// are already capped by TestEmbeddedAssetsStaySmall.
func TestDocumentationMediaDoesNotShipInTheModule(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}

	var offenders []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" || strings.HasPrefix(name, "static/") {
			continue
		}
		if !mediaExtensions[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		size, err := fileSize(name)
		if err != nil || size <= maxDocMediaBytes {
			continue
		}
		offenders = append(offenders,
			name+" ("+humanSize(size)+")")
	}

	if len(offenders) > 0 {
		t.Errorf("%d documentation asset(s) are tracked inside the module:\n  %s\n\n"+
			"Every byte here is downloaded by `go get` and never used by the "+
			"compiler. Move it to the `assets` branch and reference it from the "+
			"README by absolute raw.githubusercontent URL.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

func fileSize(name string) (int64, error) {
	fi, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
