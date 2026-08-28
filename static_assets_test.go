package monigo

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The dashboard is compiled into the consuming service's binary via
// //go:embed static/*, served offline, and has no build step, linter, or test
// runner of its own. These tests are the only automated verification the UI
// gets, so they guard the three failure modes that have actually happened:
// a reference to a file that is not there, a dependency fetched from the
// internet, and unreferenced weight shipping to every user.

// resourceRefPattern matches references that the browser must FETCH for the page
// to work: stylesheets, scripts and images. Anchor hrefs are deliberately
// excluded -- an <a> pointing at github.com is a link, not a dependency.
var resourceRefPattern = regexp.MustCompile(`(?is)<(?:link|script|img)\b[^>]*?\b(?:href|src)\s*=\s*"([^"]*)"`)

// cssURLPattern matches url(...) in stylesheets, which are fetched the same way.
var cssURLPattern = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")]+)['"]?\s*\)`)

// htmlCommentPattern and cssCommentPattern match commented-out regions. These
// must be stripped before scanning: a commented <script> tag fetches nothing, so
// treating one as a live reference produces a false positive. Three commented-out
// references to a non-existent js/core/main.js exist today and are NOT defects.
var htmlCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)
var cssCommentPattern = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripComments removes commented-out regions so only live references are scanned.
func stripComments(content, ext string) string {
	if strings.EqualFold(ext, ".css") {
		return cssCommentPattern.ReplaceAllString(content, "")
	}
	return htmlCommentPattern.ReplaceAllString(content, "")
}

// knownExternalResources are remote fetches the dashboard is permitted to make.
//
// It is empty, and should stay that way: the dashboard is served from inside a
// consuming service's binary and must render on an airgapped host. Anything
// added here is a page that breaks on a machine with no route to the internet.
//
// It previously held three entries -- Font Awesome and html2canvas from cdnjs,
// and a Lato webfont the vendored template CSS pulled from Google Fonts. All
// three were removed when the inline icon sprite replaced Font Awesome.
var knownExternalResources = map[string]string{}

// maxEmbeddedBytes caps the total size of the embedded dashboard. Every byte
// ships inside every consuming binary and is downloaded by every `go get`, so
// this is a real cost paid by users who may never open the dashboard.
//
// Tighten this whenever the payload legitimately shrinks. Do not raise it
// without a note in the PR saying what was added and why it earns its size.
//
// Headroom over the current payload is deliberately small (~5%), so that
// anything substantial being added has to be an explicit decision.
const maxEmbeddedBytes = 420_000

// htmlFiles returns every embedded .html file.
func htmlFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(path.Ext(p), ".html") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded files: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no embedded .html files found; the embed directive may have changed")
	}
	return out
}

// isRemote reports whether a reference points off-host.
func isRemote(ref string) bool {
	return strings.HasPrefix(ref, "http://") ||
		strings.HasPrefix(ref, "https://") ||
		strings.HasPrefix(ref, "//")
}

// isFetchable reports whether a reference names a file that has to exist.
func isFetchable(ref string) bool {
	if ref == "" || ref == "#" {
		return false
	}
	for _, skip := range []string{"#", "data:", "mailto:", "tel:", "javascript:", "about:"} {
		if strings.HasPrefix(ref, skip) {
			return false
		}
	}
	return !isRemote(ref)
}

// resolve turns a reference as written in a file into an embedded FS path.
// Query strings and fragments are stripped; "./x" and "../x" are resolved
// relative to the referencing file's directory.
func resolve(fromFile, ref string) (string, error) {
	if u, err := url.Parse(ref); err == nil {
		ref = u.Path
	}
	if ref == "" {
		return "", fmt.Errorf("empty path after stripping query")
	}
	joined := path.Join(path.Dir(fromFile), ref)
	if !strings.HasPrefix(joined, "static/") {
		return joined, fmt.Errorf("resolves outside the embedded tree")
	}
	return joined, nil
}

// Every asset an embedded page tells the browser to fetch must exist in the
// embedded filesystem. A missing one is a 404 on every page load, invisible
// unless someone opens devtools -- which is how js/core/main.js survived.
func TestEmbeddedPagesReferenceOnlyExistingAssets(t *testing.T) {
	type miss struct{ file, ref, resolved, why string }
	var misses []miss

	for _, f := range htmlFiles(t) {
		b, err := fs.ReadFile(staticFiles, f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		content := stripComments(string(b), path.Ext(f))
		for _, m := range resourceRefPattern.FindAllStringSubmatch(content, -1) {
			ref := strings.TrimSpace(m[1])
			if !isFetchable(ref) {
				continue
			}
			resolved, err := resolve(f, ref)
			if err != nil {
				misses = append(misses, miss{f, ref, resolved, err.Error()})
				continue
			}
			if _, err := fs.Stat(staticFiles, resolved); err != nil {
				misses = append(misses, miss{f, ref, resolved, "not present in embed.FS"})
			}
		}
	}

	if len(misses) > 0 {
		sort.Slice(misses, func(i, j int) bool {
			if misses[i].file != misses[j].file {
				return misses[i].file < misses[j].file
			}
			return misses[i].ref < misses[j].ref
		})
		var b strings.Builder
		fmt.Fprintf(&b, "%d referenced asset(s) missing from the embedded dashboard:\n", len(misses))
		for _, m := range misses {
			fmt.Fprintf(&b, "  %s\n    references %q\n    -> %s (%s)\n", m.file, m.ref, m.resolved, m.why)
		}
		b.WriteString("\nEach of these is a failed request on every load of that page.")
		t.Error(b.String())
	}
}

// The dashboard must work with no internet access. This test does not ban
// remote resources outright -- three exist today -- but it pins the set, so
// adding a fourth is a deliberate act rather than an accident.
func TestEmbeddedPagesAddNoNewExternalResources(t *testing.T) {
	files := htmlFiles(t)

	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(path.Ext(p), ".css") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded files: %v", err)
	}

	allowed := func(ref string) bool {
		for prefix := range knownExternalResources {
			if strings.HasPrefix(ref, prefix) {
				return true
			}
		}
		return false
	}

	seen := map[string][]string{}
	for _, f := range files {
		b, err := fs.ReadFile(staticFiles, f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		content := stripComments(string(b), path.Ext(f))

		refs := []string{}
		for _, m := range resourceRefPattern.FindAllStringSubmatch(content, -1) {
			refs = append(refs, strings.TrimSpace(m[1]))
		}
		if strings.EqualFold(path.Ext(f), ".css") {
			for _, m := range cssURLPattern.FindAllStringSubmatch(content, -1) {
				refs = append(refs, strings.TrimSpace(m[1]))
			}
		}

		for _, ref := range refs {
			if !isRemote(ref) || allowed(ref) {
				continue
			}
			seen[ref] = append(seen[ref], f)
		}
	}

	if len(seen) > 0 {
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var b strings.Builder
		b.WriteString("new external resource dependencies found in the embedded dashboard:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s\n    in: %s\n", k, strings.Join(seen[k], ", "))
		}
		b.WriteString("\nThe dashboard must render on an airgapped host. Vendor the asset, or\n")
		b.WriteString("add it to knownExternalResources with a reason and a plan to remove it.")
		t.Error(b.String())
	}
}

// Known remote resources must actually still be present. Without this, the
// allowlist above silently rots into a list of things that were removed years
// ago, and stops protecting anything.
func TestKnownExternalResourcesAreStillPresent(t *testing.T) {
	var content strings.Builder
	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(path.Ext(p))
		if ext != ".html" && ext != ".css" {
			return nil
		}
		b, err := fs.ReadFile(staticFiles, p)
		if err != nil {
			return err
		}
		content.WriteString(stripComments(string(b), ext))
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded files: %v", err)
	}

	for ref, reason := range knownExternalResources {
		if !strings.Contains(content.String(), ref) {
			t.Errorf("knownExternalResources lists %q (%s) but it is no longer referenced;\n"+
				"remove the entry so the allowlist keeps shrinking", ref, reason)
		}
	}
}

// The embedded dashboard ships inside every consuming binary, so its size is a
// cost borne by users who may never open it.
func TestEmbeddedPayloadWithinBudget(t *testing.T) {
	var total int64
	sizes := map[string]int64{}

	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		sizes[p] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded files: %v", err)
	}

	t.Logf("embedded dashboard: %.1f MB across %d files (budget %.1f MB)",
		float64(total)/1e6, len(sizes), float64(maxEmbeddedBytes)/1e6)

	if total > maxEmbeddedBytes {
		type entry struct {
			p string
			n int64
		}
		list := make([]entry, 0, len(sizes))
		for p, n := range sizes {
			list = append(list, entry{p, n})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })

		var b strings.Builder
		fmt.Fprintf(&b, "embedded dashboard is %d bytes, over the %d byte budget by %d.\n",
			total, maxEmbeddedBytes, total-maxEmbeddedBytes)
		b.WriteString("largest contributors:\n")
		for i, e := range list {
			if i >= 8 {
				break
			}
			fmt.Fprintf(&b, "  %8.2f MB  %s\n", float64(e.n)/1e6, e.p)
		}
		t.Error(b.String())
	}
}

// Editor and OS droppings should not be compiled into users' binaries.
func TestEmbeddedPayloadHasNoJunkFiles(t *testing.T) {
	junk := []string{".DS_Store", "Thumbs.db", ".gitkeep"}
	var found []string

	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		for _, j := range junk {
			if strings.EqualFold(path.Base(p), j) {
				found = append(found, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded files: %v", err)
	}

	if len(found) > 0 {
		sort.Strings(found)
		t.Errorf("junk files embedded into the binary:\n  %s\n\nDelete them and add the pattern to .gitignore.",
			strings.Join(found, "\n  "))
	}
}

// The dashboard is a browser page. It cannot be trusted to assert its own
// privilege level, because anything it sends, any visitor can send.
//
// It previously did exactly that: authenticatedFetch attached a privileged
// role header and a hardcoded shared secret to every request that carried no
// API key, both copied from example/security-examples/custom-auth -- whose
// auth function grants access for precisely those two credentials. A consumer
// following that documented example got a dashboard that satisfied their own
// auth check on its own say-so.
//
// The same block was copy-pasted into all six page scripts, so this checks
// every embedded file rather than the one it was noticed in.
func TestDashboardAssertsNoPrivilegeOfItsOwn(t *testing.T) {
	banned := map[string]string{
		"X-User-Role":         "a self-asserted role header",
		"monigo-admin-secret": "a hardcoded shared secret from the custom-auth example",
		"MoniGo-Admin":        "a spoofed User-Agent used as a credential",
	}

	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".js") && !strings.HasSuffix(p, ".html") {
			return nil
		}
		// Vendored template code is not ours and never had this.
		if strings.Contains(p, "/core/") {
			return nil
		}
		body, err := staticFiles.ReadFile(p)
		if err != nil {
			return err
		}
		for needle, what := range banned {
			if bytes.Contains(body, []byte(needle)) {
				t.Errorf("%s contains %q -- %s.\n"+
					"The dashboard must not send a credential that grants it access; "+
					"custom authentication belongs in the middleware, not in page "+
					"JavaScript, because a browser cannot vouch for itself.",
					p, needle, what)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded files: %v", err)
	}
}

// An API key in a query string is recorded in browser history, sent in the
// Referer header to any external link the page carries, and written to every
// access log between the browser and the process. APIKeyMiddleware accepts an
// X-API-Key header too (monigo.go), so the header costs nothing and leaks
// nothing.
func TestDashboardSendsTheAPIKeyAsAHeaderNotAQueryParameter(t *testing.T) {
	// Matches building a URL with the key appended, which is how it used to be
	// done: `${url}${separator}api_key=${...}`.
	urlBuild := regexp.MustCompile(`api_key=\$\{|[?&]api_key=`)

	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		if strings.Contains(p, "/core/") {
			return nil
		}
		body, err := staticFiles.ReadFile(p)
		if err != nil {
			return err
		}
		if urlBuild.Match(body) {
			t.Errorf("%s builds a URL containing api_key. Send it as an X-API-Key "+
				"header instead: a query parameter lands in browser history, in the "+
				"Referer sent to external links, and in access logs.", p)
		}
		// Reading the key from the page's own URL is how it arrives and is fine.
		if !bytes.Contains(body, []byte("X-API-Key")) && bytes.Contains(body, []byte("getApiKey")) {
			t.Errorf("%s reads an API key but never sends an X-API-Key header", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded scripts: %v", err)
	}
}

// ourStylesheets returns the embedded stylesheets MoniGo actually authors.
//
// Everything under static/css/core is vendored Bootstrap and template CSS: it
// is not ours to fix, and backend.css carries a genuinely dead
// var(--fill-percentage) that no rule, script or page ever sets.
//
// This is a predicate rather than a hardcoded filename so that renaming or
// splitting our stylesheet cannot silently switch the checks below off. If it
// ever matches nothing, that is a failure, not a pass.
func ourStylesheets(t *testing.T) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(staticFiles, "static/css", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".css") {
			return err
		}
		if strings.Contains(p, "/core/") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded stylesheets: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no first-party stylesheet found under static/css -- this test " +
			"would silently check nothing. If the CSS moved, update ourStylesheets.")
	}
	sort.Strings(out)
	return out
}

var (
	tokenDefPattern    = regexp.MustCompile(`(--[\w-]+)\s*:`)
	tokenUsePattern    = regexp.MustCompile(`var\(\s*(--[\w-]+)\s*\)`)
	inlineStylePattern = regexp.MustCompile(`(?i)\bstyle\s*=\s*"([^"]*)"`)
)

// A CSS custom property that is never defined does not error. The browser drops
// the declaration and the element silently keeps its inherited value, so the
// page still renders -- just wrong, and plausibly enough that review misses it.
// That is exactly how a footer once shipped using the design file's token names
// instead of this stylesheet's, rendering transparent on the wrong text tone.
//
// var(--x, fallback) is exempt: supplying a fallback is the author saying what
// to do when the token is absent.
//
// HTML style="..." attributes are scanned too. The design's own idiom is
// style="color:var(--dim2)", so the mistake this test exists to catch can
// arrive from markup as easily as from a stylesheet.
func TestStylesheetsDefineEveryTokenTheyUse(t *testing.T) {
	defined := map[string]bool{}
	for _, p := range ourStylesheets(t) {
		b, err := staticFiles.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		css := cssCommentPattern.ReplaceAllString(string(b), "")
		for _, m := range tokenDefPattern.FindAllStringSubmatch(css, -1) {
			defined[m[1]] = true
		}
	}

	report := func(where string, missing []string) {
		if len(missing) == 0 {
			return
		}
		sort.Strings(missing)
		t.Errorf("%s uses custom properties nothing defines: %s\n"+
			"These resolve to nothing and the declaration is dropped silently, so the "+
			"element keeps whatever it inherited. Define them, use the name the "+
			"stylesheet actually declares, or supply a fallback as var(%s, <value>).",
			where, strings.Join(missing, ", "), missing[0])
	}

	collect := func(text string) []string {
		var missing []string
		seen := map[string]bool{}
		for _, m := range tokenUsePattern.FindAllStringSubmatch(text, -1) {
			if !defined[m[1]] && !seen[m[1]] {
				seen[m[1]] = true
				missing = append(missing, m[1])
			}
		}
		return missing
	}

	for _, p := range ourStylesheets(t) {
		b, _ := staticFiles.ReadFile(p)
		report(p, collect(cssCommentPattern.ReplaceAllString(string(b), "")))
	}

	for _, f := range htmlFiles(t) {
		b, err := fs.ReadFile(staticFiles, f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		html := stripComments(string(b), path.Ext(f))
		var inline strings.Builder
		for _, m := range inlineStylePattern.FindAllStringSubmatch(html, -1) {
			inline.WriteString(m[1])
			inline.WriteByte(';')
		}
		report(f+` (style="..." attributes)`, collect(inline.String()))
	}
}

// One token, one source. The palette has already drifted once: the same colour
// role existed under two names in two files, and the newer fix landed on only
// one of them, leaving the most-used text tone below the contrast floor
// wherever the stale copy won.
//
// Defining a token twice within a theme block is how that happens, so it is
// refused. Legitimate redefinition -- a light block overriding the dark
// default -- lives in a *different* selector block, which this allows.
func TestNoTokenIsDefinedTwiceInTheSameBlock(t *testing.T) {
	blockPattern := regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)

	for _, p := range ourStylesheets(t) {
		b, err := staticFiles.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		css := cssCommentPattern.ReplaceAllString(string(b), "")

		for _, blk := range blockPattern.FindAllStringSubmatch(css, -1) {
			selector := strings.Join(strings.Fields(blk[1]), " ")
			seen := map[string]int{}
			for _, m := range tokenDefPattern.FindAllStringSubmatch(blk[2], -1) {
				seen[m[1]]++
			}
			var dupes []string
			for name, n := range seen {
				if n > 1 {
					dupes = append(dupes, fmt.Sprintf("%s (%d times)", name, n))
				}
			}
			if len(dupes) > 0 {
				sort.Strings(dupes)
				t.Errorf("%s: %q defines the same token more than once: %s\n"+
					"Only the last wins, so the others are dead text that reads as "+
					"authoritative. Keep one definition per token per block.",
					p, selector, strings.Join(dupes, ", "))
			}
		}
	}
}

// resourceRefPattern deliberately ignores anchors, because <a href> to an
// external site is a link rather than a dependency. But an anchor pointing at
// another page of this dashboard IS a dependency: a nav item whose target does
// not exist is a 404 the user reaches by clicking the sidebar.
func TestLocalPageLinksResolve(t *testing.T) {
	anchorPattern := regexp.MustCompile(`(?is)<a\b[^>]*?\bhref\s*=\s*"([^"]*)"`)

	for _, f := range htmlFiles(t) {
		b, err := fs.ReadFile(staticFiles, f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		content := stripComments(string(b), path.Ext(f))

		for _, m := range anchorPattern.FindAllStringSubmatch(content, -1) {
			ref := strings.TrimSpace(m[1])
			if !isFetchable(ref) {
				continue
			}
			// Only same-origin page links; assets are covered elsewhere.
			base := ref
			if i := strings.IndexAny(base, "?#"); i >= 0 {
				base = base[:i]
			}
			if !strings.HasSuffix(strings.ToLower(base), ".html") {
				continue
			}
			resolved, err := resolve(f, ref)
			if err != nil {
				t.Errorf("%s links to %q which does not resolve: %v", f, ref, err)
				continue
			}
			if _, err := fs.Stat(staticFiles, resolved); err != nil {
				t.Errorf("%s links to %q -> %s, which is not in the embed.\n"+
					"That is a 404 reached by clicking the navigation.",
					f, ref, resolved)
			}
		}
	}
}

// A version number written into a page is a promise nobody keeps. The footer
// here read "v1.0.0" and linked to that release tag for four releases after it
// stopped being true, because nothing in the build has any reason to touch it.
//
// The fix is not to update the literal, it is to stop having one: link to the
// releases index, which is correct at every version. So no hardcoded semver may
// appear in an embedded page at all.
func TestEmbeddedPagesCarryNoHardcodedVersion(t *testing.T) {
	semver := regexp.MustCompile(`\bv[0-9]+\.[0-9]+\.[0-9]+\b`)

	for _, f := range htmlFiles(t) {
		b, err := fs.ReadFile(staticFiles, f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		content := stripComments(string(b), path.Ext(f))
		for _, hit := range semver.FindAllString(content, -1) {
			t.Errorf("%s hardcodes the version %q.\n"+
				"Nothing updates it, so it goes stale silently -- this footer claimed "+
				"v1.0.0 four releases on. Link to /releases and drop the literal.",
				f, hit)
		}
	}
}

// A custom property that resolves to itself is invalid at computed-value time:
// the browser discards the declaration and every use of the token falls back to
// whatever it inherited. It renders, so review passes it.
//
// This design has shipped that exact defect before -- `--onacc: var(--onacc)`
// left eight accent buttons inheriting body text colour at 2.02:1 -- and the
// alias layer added for the shell swap is the natural place to reintroduce it,
// since aliasing is what it does.
func TestNoTokenResolvesToItself(t *testing.T) {
	aliasPattern := regexp.MustCompile(`(--[\w-]+)\s*:\s*var\(\s*(--[\w-]+)`)

	for _, p := range ourStylesheets(t) {
		b, err := staticFiles.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		css := cssCommentPattern.ReplaceAllString(string(b), "")
		for _, m := range aliasPattern.FindAllStringSubmatch(css, -1) {
			if m[1] == m[2] {
				t.Errorf("%s: %s is defined as var(%s) -- it resolves to itself.\n"+
					"The declaration is discarded and every use of it silently falls "+
					"back to the inherited value.", p, m[1], m[2])
			}
		}
	}
}

// The hidden attribute works by applying `display: none` from the UA
// stylesheet, which sits below every author rule. Any class that sets its own
// display therefore silently defeats it: the element stays on screen, and
// JavaScript that sets .hidden = true appears to do nothing.
//
// That shipped once. A function row's detail panel used display:flex, so
// closing it left the panel visible and an unopened row still reserved space
// for an empty box.
func TestHiddenAttributeIsNotDefeatedByADisplayRule(t *testing.T) {
	// The boolean attribute, not aria-hidden and not hidden="...".
	hiddenAttr := regexp.MustCompile(`(?i)(?:^|[^-\w])hidden(?:\s|>|/)`)
	tagPattern := regexp.MustCompile(`(?is)<[a-z][^>]*>`)
	classPattern := regexp.MustCompile(`(?i)class\s*=\s*"([^"]*)"`)

	classes := map[string]string{} // class -> first page that hides it
	for _, f := range htmlFiles(t) {
		b, err := fs.ReadFile(staticFiles, f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		for _, tag := range tagPattern.FindAllString(stripComments(string(b), path.Ext(f)), -1) {
			if !hiddenAttr.MatchString(tag) {
				continue
			}
			m := classPattern.FindStringSubmatch(tag)
			if m == nil {
				continue
			}
			for _, c := range strings.Fields(m[1]) {
				if _, seen := classes[c]; !seen {
					classes[c] = f
				}
			}
		}
	}

	var sb strings.Builder
	for _, p := range ourStylesheets(t) {
		b, err := staticFiles.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		sb.WriteString(cssCommentPattern.ReplaceAllString(string(b), ""))
	}
	sheet := sb.String()

	names := make([]string, 0, len(classes))
	for c := range classes {
		names = append(names, c)
	}
	sort.Strings(names)

	// RE2 has no negative lookahead, so the display values are extracted and
	// compared here rather than excluded in the pattern.
	for _, c := range names {
		q := regexp.QuoteMeta(c)
		rules := regexp.MustCompile(`\.` + q + `(?:[^\w-][^{}]*)?\{([^}]*)\}`)
		display := regexp.MustCompile(`display\s*:\s*([a-z-]+)`)

		setsVisibleDisplay := false
		for _, body := range rules.FindAllStringSubmatch(sheet, -1) {
			for _, d := range display.FindAllStringSubmatch(body[1], -1) {
				if d[1] != "none" {
					setsVisibleDisplay = true
				}
			}
		}
		guarded := regexp.MustCompile(`\.` + q + `\[hidden\]`).MatchString(sheet)

		if setsVisibleDisplay && !guarded {
			t.Errorf(".%s carries the hidden attribute in %s but also sets its own "+
				"display, which overrides the UA rule that makes hidden work.\n"+
				"Add `.%s[hidden] { display: none; }`, or the element stays visible "+
				"when it is supposed to be hidden.", c, classes[c], c)
		}
	}
}
