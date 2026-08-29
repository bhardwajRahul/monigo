package monigo

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Executable magic numbers. A compiled Go binary on any platform a contributor
// is likely to use starts with one of these.
var executableMagic = []struct {
	name  string
	magic []byte
}{
	{"ELF", []byte{0x7f, 'E', 'L', 'F'}},
	{"Mach-O 32-bit", []byte{0xfe, 0xed, 0xfa, 0xce}},
	{"Mach-O 64-bit", []byte{0xfe, 0xed, 0xfa, 0xcf}},
	{"Mach-O 32-bit LE", []byte{0xce, 0xfa, 0xed, 0xfe}},
	{"Mach-O 64-bit LE", []byte{0xcf, 0xfa, 0xed, 0xfe}},
	{"Mach-O universal", []byte{0xca, 0xfe, 0xba, 0xbe}},
	{"PE (Windows)", []byte{'M', 'Z'}},
}

// TestNoCompiledBinaryIsTracked keeps 502 MB from coming back.
//
// Thirteen compiled example programs were committed -- macOS arm64, so they
// could not run on Linux, Windows, or an Intel Mac, and nobody could use them.
// They were 95% of the repository. .gitignore covers *.exe, *.dylib and /bin,
// none of which match a Go binary on macOS or Linux: it takes the name of its
// directory and has no extension. No glob can separate that from an
// extensionless source file, so the file's own magic number is the check.
func TestNoCompiledBinaryIsTracked(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}

	var offenders []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		// Only read enough to identify the file.
		head, err := readHead(name, 4)
		if err != nil || len(head) < 2 {
			continue
		}
		for _, m := range executableMagic {
			// "MZ" alone is weak -- it is two printable ASCII bytes and could
			// begin a text file -- so it only counts without an extension,
			// which is what a stray Go build produces.
			if m.name == "PE (Windows)" && filepath.Ext(name) != "" {
				continue
			}
			if bytes.HasPrefix(head, m.magic) {
				offenders = append(offenders, name+" ("+m.name+")")
				break
			}
		}
	}

	if len(offenders) > 0 {
		t.Errorf("%d compiled binaries are tracked:\n  %s\n\n"+
			"Build artifacts do not belong in the repository: they are "+
			"platform-specific, unusable to anyone on a different OS or "+
			"architecture, and downloaded by everyone who clones. Run "+
			"`git rm --cached <file>` and add the path to .gitignore.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// readHead reads at most n bytes from the start of a file.
func readHead(name string, n int) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:read], nil
}
