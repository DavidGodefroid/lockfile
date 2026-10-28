//go:build unix

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shFixture runs scripts/install.sh against a fake release: curl serves
// files from a directory, and cosign records its arguments. PATH holds only
// the tools the script needs, so that a cosign installed on the machine is
// not found.
type shFixture struct {
	release, bin, dest, share, cosignArgs string
	env                                   []string
}

const shVersion = "0.9.1"

// Digests the fake cosign reports for the release images.
var (
	fakeDigest      = "sha256:" + strings.Repeat("a", 64)
	fakeVaultDigest = "sha256:" + strings.Repeat("b", 64)
)

// shIdentity is the signer install.sh and the launcher's update accept for
// the release vX.Y.Z of shVersion: its release workflow run on that tag.
var shIdentity = `^https://github\.com/DavidGodefroid/lockfile/\.github/workflows/release\.yml@refs/tags/v0\.9\.1$`

func newShFixture(t *testing.T) *shFixture {
	t.Helper()
	root := t.TempDir()
	f := &shFixture{
		release:    filepath.Join(root, "release"),
		bin:        filepath.Join(root, "bin"),
		dest:       filepath.Join(root, "dest"),
		share:      filepath.Join(root, "share", "lockfile"),
		cosignArgs: filepath.Join(root, "cosign-args"),
	}
	for _, d := range []string{f.release, f.bin, f.dest} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"sh", "awk", "sha256sum", "shasum", "tar", "gzip", "install", "mktemp", "rm", "mkdir", "uname", "cat", "sed", "tr", "tail", "printf", "grep", "wc", "sort", "head", "dirname", "id", "cksum"} {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(f.bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// curl -fsSL -o OUT URL: the last path segment of URL, from the release.
	writeExec(t, filepath.Join(f.bin, "curl"), `#!/bin/sh
out=""; while [ $# -gt 1 ]; do [ "$1" = -o ] && { out=$2; shift; }; shift; done
cat "`+f.release+`/${1##*/}" > "$out"
`)
	osName := map[string]string{"linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if osName == "" || arch == "" {
		t.Skip("install.sh supports Linux and macOS on amd64 and arm64 only")
	}
	launcher, err := os.ReadFile(filepath.Join("..", "..", "scripts", "lockfile"))
	if err != nil {
		t.Fatal(err)
	}
	f.archive(t, map[string][]byte{
		"lockfile":         []byte("#!/bin/sh\necho lockfile v" + shVersion + "\n"),
		"scripts/lockfile": launcher,
		"seccomp.json":     []byte(`{"defaultAction":"SCMP_ACT_ERRNO"}`),
	})
	if err := os.WriteFile(filepath.Join(f.release, "checksums.txt.sigstore.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.env = []string{"PATH=" + f.bin, "HOME=" + root, "LOCKFILE_VERSION=v" + shVersion,
		"LOCKFILE_INSTALL_DIR=" + f.dest, "LOCKFILE_SHARE_DIR=" + f.share}
	return f
}

// archive writes the release archive of this OS with files, and its
// checksums.txt.
func (f *shFixture) archive(t *testing.T, files map[string][]byte) {
	t.Helper()
	name := "lockfile_" + shVersion + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	data := tarGzFiles(t, files)
	if err := os.WriteFile(filepath.Join(f.release, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.release, "checksums.txt"), []byte(hexSum(data)+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cosign installs a fake cosign that records its arguments (every call,
// appended), accepts the signature of checksums.txt when blobOK, and
// verifies the release images, printing their digests, when imageOK.
func (f *shFixture) cosign(t *testing.T, blobOK, imageOK bool) {
	t.Helper()
	code := func(ok bool) string {
		if ok {
			return "0"
		}
		return "1"
	}
	writeExec(t, filepath.Join(f.bin, "cosign"), `#!/bin/sh
printf '%s\n' "$@" >> '`+f.cosignArgs+`'
case "$1" in
verify-blob) exit `+code(blobOK)+` ;;
verify)
	[ `+code(imageOK)+` = 0 ] || exit 1
	for a in "$@"; do ref=$a; done
	case "$ref" in *-vault) d='`+fakeVaultDigest+`' ;; *) d='`+fakeDigest+`' ;; esac
	printf '[{"critical":{"identity":{"docker-reference":"%s"},"image":{"docker-manifest-digest":"%s"},"type":"cosign container image signature"}}]\n' "${ref%:*}" "$d"
	;;
esac
`)
}

func (f *shFixture) run(t *testing.T, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(f.bin, "sh"), filepath.Join("..", "..", "scripts", "install.sh"))
	cmd.Env = append(append([]string{}, f.env...), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *shFixture) installed() bool {
	_, err := os.Stat(filepath.Join(f.dest, "lockfile"))
	return err == nil
}

// install.sh installs the release's launcher, not its binary, set to run
// the release's images by their verified digests, and its seccomp profile.
func TestInstallShInstallsTheLauncher(t *testing.T) {
	f := newShFixture(t)
	f.cosign(t, true, true)
	out, err := f.run(t)
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(f.dest, "lockfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\nlauncher_version=" + shVersion + "\n", "\nimage_digest=" + fakeDigest + "\n",
		"\nvault_image_digest=" + fakeVaultDigest + "\n", "\nseccomp_profile=" + filepath.Join(f.share, "seccomp.json") + "\n", "docker run"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("launcher lacks %q", want)
		}
	}
	if p, err := os.ReadFile(filepath.Join(f.share, "seccomp.json")); err != nil || !strings.Contains(string(p), "SCMP_ACT_ERRNO") {
		t.Errorf("seccomp profile: %v %q", err, p)
	}
	args, _ := os.ReadFile(f.cosignArgs)
	for _, want := range []string{"verify-blob\n", "verify\n", "ghcr.io/davidgodefroid/lockfile:" + shVersion + "\n",
		"ghcr.io/davidgodefroid/lockfile:" + shVersion + "-vault\n"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("cosign args lack %q:\n%s", want, args)
		}
	}
	// checksums.txt and both images, each against this tag's release run.
	if n := strings.Count(string(args), "--certificate-identity-regexp\n"+shIdentity+"\n"); n != 3 {
		t.Errorf("%d checks bound to v%s, want 3:\n%s", n, shVersion, args)
	}
	if !strings.Contains(out, "ghcr.io/davidgodefroid/lockfile@"+fakeDigest) {
		t.Errorf("no image named:\n%s", out)
	}
}

// Without cosign nothing is installed: lockfile has no unsigned mode.
func TestInstallShRequiresCosign(t *testing.T) {
	f := newShFixture(t)
	out, err := f.run(t)
	if err == nil || f.installed() || !strings.Contains(out, "cosign is required") {
		t.Fatalf("%v, installed %v\n%s", err, f.installed(), out)
	}
}

// A bad signature of checksums.txt, an image that does not verify (or is
// not published yet) or a bad checksum stops the install.
func TestInstallShRefusesWhatDoesNotVerify(t *testing.T) {
	for _, tc := range []struct {
		blobOK, imageOK bool
		want            string
	}{
		{false, true, "not signed by lockfile's release workflow"},
		{true, false, "does not verify, or the image is not published yet"},
	} {
		f := newShFixture(t)
		f.cosign(t, tc.blobOK, tc.imageOK)
		out, err := f.run(t)
		if err == nil || f.installed() || !strings.Contains(out, tc.want) {
			t.Errorf("%+v: %v, installed %v\n%s", tc, err, f.installed(), out)
		}
	}
	f := newShFixture(t)
	f.cosign(t, true, true)
	name := "lockfile_" + shVersion + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if err := os.WriteFile(filepath.Join(f.release, "checksums.txt"), []byte(strings.Repeat("0", 64)+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := f.run(t); err == nil || f.installed() || !strings.Contains(out, "checksum mismatch") {
		t.Errorf("bad checksum: %v, installed %v\n%s", err, f.installed(), out)
	}
}

// A release before the launcher has none (nor seccomp.json) in its
// archive: install.sh says so and installs nothing.
func TestInstallShRefusesAnArchiveWithoutLauncher(t *testing.T) {
	f := newShFixture(t)
	f.cosign(t, true, true)
	f.archive(t, map[string][]byte{"lockfile": []byte("#!/bin/sh\n")})
	out, err := f.run(t)
	if err == nil || f.installed() || !strings.Contains(out, "holds no launcher") {
		t.Fatalf("%v, installed %v\n%s", err, f.installed(), out)
	}
}

// LOCKFILE_VERSION takes only an exact vX.Y.Z.
func TestInstallShRefusesABadVersion(t *testing.T) {
	for _, v := range []string{"v1.2.3;id", "v1.2", "v1.2.3/x", "latest"} {
		f := newShFixture(t)
		f.cosign(t, true, true)
		out, err := f.run(t, "LOCKFILE_VERSION="+v)
		if err == nil || f.installed() || !strings.Contains(out, "invalid release tag") {
			t.Errorf("%q: %v, installed %v\n%s", v, err, f.installed(), out)
		}
	}
}

// An installed launcher of a newer release is not downgraded unless asked.
func TestInstallShRefusesADowngradeOfTheLauncher(t *testing.T) {
	f := newShFixture(t)
	f.cosign(t, true, true)
	writeExec(t, filepath.Join(f.dest, "lockfile"), "#!/bin/sh\nlauncher_version=1.0.0\n")
	if out, err := f.run(t); err == nil || !strings.Contains(out, "newer than v"+shVersion) {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := f.run(t, "LOCKFILE_ALLOW_DOWNGRADE=1"); err != nil {
		t.Fatalf("LOCKFILE_ALLOW_DOWNGRADE=1: %v\n%s", err, out)
	}
}

func writeExec(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// tarGzFiles is a tar.gz archive of files, by path.
func tarGzFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, b := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
