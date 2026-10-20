package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/DavidGodefroid/lockkit/update"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/separate"
)

func init() {
	commands["update"] = command{human: true, help: "install the latest signed release [--check]", run: runUpdate}
}

// releaseWorkflow is the signer of lockfile's releases: its release
// workflow, run on a version tag.
const releaseWorkflow = `^https://github\.com/DavidGodefroid/lockfile/\.github/workflows/release\.yml@refs/tags/`

// releaseTag is an exact release tag vX.Y.Z.
var releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// releaseIdentity binds the signature to the release's own tag: a
// checksums.txt signed by the run of another (older) tag does not verify.
func releaseIdentity(tag string) update.Identity {
	return update.Identity{
		CertIdentityRegexp: releaseWorkflow + regexp.QuoteMeta(tag) + "$",
		OIDCIssuer:         "https://token.actions.githubusercontent.com",
	}
}

// compareTags compares two vX.Y.Z tags; ok is false when either is not one
// (a development build).
func compareTags(a, b string) (cmp int, ok bool) {
	ma, mb := releaseTag.FindStringSubmatch(a), releaseTag.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return 0, false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			if x < y {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

const latestRelease = "https://api.github.com/repos/DavidGodefroid/lockfile/releases/latest"

// updateHooks replace the network, cosign, the binary and sudo in tests.
type updateHooks struct {
	latest string
	http   *http.Client
	cosign string
	target string
	goos   string
	goarch string
	root   func(script string) error
}

var testUpdateHooks *updateHooks

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) asset(name string) (string, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL, true
		}
	}
	return "", false
}

// maxDownload caps every download of an update.
const maxDownload = 128 << 20

func download(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing non-https URL %s", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDownload {
		return nil, fmt.Errorf("%s is too large", url)
	}
	return b, nil
}

// binaryFromArchive extracts the lockfile binary from a release archive.
func binaryFromArchive(archive []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the archive holds no lockfile binary")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == product.P.Name {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

func runUpdate(e env, args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	check := fs.Bool("check", false, "only tell whether a newer release exists")
	yes := fs.Bool("yes", false, "replace the binary without asking")
	force := fs.Bool("allow-downgrade", false, "install a release older than this one")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErr(e, "update [--check] [--yes] [--allow-downgrade]")
	}
	h := updateHooks{latest: latestRelease, http: http.DefaultClient, goos: runtime.GOOS, goarch: runtime.GOARCH}
	if testUpdateHooks != nil {
		h = *testUpdateHooks
	}
	ctx, stop := agentContext()
	defer stop()
	body, err := download(ctx, h.http, h.latest)
	if err != nil {
		return failHuman(e, "update", err)
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil || !releaseTag.MatchString(rel.Tag) {
		return failHuman(e, "update", errors.New("unexpected answer from the release API"))
	}
	if rel.Tag == product.Version {
		fmt.Fprintln(e.stdout, "lockfile "+product.Version+" is the latest release")
		return product.ExitOK
	}
	if c, ok := compareTags(rel.Tag, product.Version); ok && c < 0 && !*force {
		// An older signed release is still validly signed: refuse it, or a
		// replayed answer would roll back fixes.
		return failHuman(e, "update", fmt.Errorf("the release offered (%s) is older than this one (%s): nothing installed (--allow-downgrade to force)", rel.Tag, product.Version))
	}
	fmt.Fprintf(e.stdout, "lockfile %s is available (this is %s)\n", rel.Tag, product.Version)
	if *check {
		return product.ExitOK
	}
	sumsURL, ok1 := rel.asset("checksums.txt")
	bundleURL, ok2 := rel.asset("checksums.txt.sigstore.json")
	name := fmt.Sprintf("%s_%s_%s_%s.tar.gz", product.P.Name, strings.TrimPrefix(rel.Tag, "v"), h.goos, h.goarch)
	archURL, ok3 := rel.asset(name)
	if !ok1 || !ok2 || !ok3 {
		return failHuman(e, "update", fmt.Errorf("release %s lacks checksums.txt, its signature or %s", rel.Tag, name))
	}
	sums, err := download(ctx, h.http, sumsURL)
	if err != nil {
		return failHuman(e, "update", err)
	}
	bundle, err := download(ctx, h.http, bundleURL)
	if err != nil {
		return failHuman(e, "update", err)
	}
	// The signature first: nothing else of the release is trusted before.
	if err := update.Verify(ctx, sums, bundle, releaseIdentity(rel.Tag), update.Options{Cosign: h.cosign}); err != nil {
		if errors.Is(err, update.ErrCosignMissing) {
			return failHuman(e, "update", fmt.Errorf("%w: nothing installed", err))
		}
		return failHuman(e, "update", fmt.Errorf("checksums.txt of %s is not signed by lockfile's release workflow: nothing installed", rel.Tag))
	}
	want, err := update.ChecksumFor(sums, name)
	if err != nil {
		return failHuman(e, "update", err)
	}
	archive, err := download(ctx, h.http, archURL)
	if err != nil {
		return failHuman(e, "update", err)
	}
	if err := update.CheckSHA256(archive, want); err != nil {
		return failHuman(e, "update", fmt.Errorf("%s does not match its signed checksum: nothing installed", name))
	}
	bin, err := binaryFromArchive(archive)
	if err != nil {
		return failHuman(e, "update", err)
	}
	return replaceBinary(e, h, bin, *yes)
}

// replaceBinary puts bin in place of the running binary: by rename when
// its directory is this account's, else with sudo from a root-checked
// copy (the separated setup's /usr/local/bin).
func replaceBinary(e env, h updateHooks, bin []byte, yes bool) int {
	target := h.target
	if target == "" {
		var err error
		if target, err = os.Executable(); err == nil {
			target, err = filepath.EvalSymlinks(target)
		}
		if err != nil {
			return failHuman(e, "update", err)
		}
	}
	tmp, err := os.CreateTemp("", "lockfile-update-")
	if err != nil {
		return failHuman(e, "update", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return failHuman(e, "update", err)
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return failHuman(e, "update", err)
	}
	// This account owns the directory: an atomic rename.
	if f, err := os.CreateTemp(filepath.Dir(target), ".lockfile-update-"); err == nil {
		staged := f.Name()
		_, werr := f.Write(bin)
		f.Close()
		if werr == nil {
			werr = os.Chmod(staged, 0o755)
		}
		if werr == nil {
			werr = os.Rename(staged, target)
		}
		if werr != nil {
			os.Remove(staged)
			return failHuman(e, "update", werr)
		}
		fmt.Fprintln(e.stdout, "updated "+target)
		return product.ExitOK
	}
	sum, err := separate.FileSum(tmp.Name())
	if err != nil {
		return failHuman(e, "update", err)
	}
	group, sumCmd := "root", "sha256sum -c -"
	if h.goos == "darwin" {
		group, sumCmd = "wheel", "shasum -a 256 -c -"
	}
	prefix, src := separate.PinnedSource(product.P, tmp.Name(), sum, sumCmd)
	script := "set -eu\n" + prefix + fmt.Sprintf("install -m 0755 -o root -g %s %s %s\n", group, src, separate.ShellQuote(target))
	fmt.Fprintf(e.stdout, "%s is not yours to replace; these commands run as root:\n\n    %s\n", target,
		strings.ReplaceAll(strings.TrimRight(script, "\n"), "\n", "\n    "))
	if !yes {
		fmt.Fprint(e.stdout, "Run them now with sudo? [y/N] ")
		ans, _ := bufio.NewReader(e.stdin).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(ans), "y") {
			fmt.Fprintln(e.stdout, "nothing done")
			return product.ExitOK
		}
	}
	run := h.root
	if run == nil {
		run = func(script string) error {
			cmd := exec.Command("sudo", "sh", "-c", script)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, e.stdout, e.stderr
			return cmd.Run()
		}
	}
	if err := run(script); err != nil {
		return failHuman(e, "update", err)
	}
	fmt.Fprintln(e.stdout, "updated "+target)
	return product.ExitOK
}
