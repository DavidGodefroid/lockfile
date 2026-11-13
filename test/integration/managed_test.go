//go:build integration && lockfamily_managed_test

package integration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/managed"

	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/source"
)

// A managed sftp profile is served from the managed file alone and reads
// through the SFTP server; a config profile of its name fails the parse.
func TestManagedSFTPProfile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "outgoing"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outgoing", "a.pdf"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, key := sftpServer(t, root)
	cfg := sftpCfg(t, addr, knownHostsFile(t, addr, key), filepath.Join(root, "outgoing"))
	body := fmt.Sprintf(`version = 1
organisation = "Acme"

[lockfile.bounds]
source = ["sftp"]
hosts = [%q]

[lockfile.profiles.erp]
source = "sftp"
host = %q
port = %d
user = %q
auth = "password"
known_hosts = %q
base_dir = %q
`, cfg.Host, cfg.Host, cfg.Port, cfg.User, cfg.KnownHosts, cfg.BaseDir)
	mpath := filepath.Join(t.TempDir(), managed.FileName)
	if err := os.WriteFile(mpath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	managed.SetTestPath(mpath)
	t.Cleanup(func() { managed.SetTestPath("") })

	// No config file of its own: the managed profile alone.
	f, err := profile.ReadFile(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := f.Profiles["erp"]
	if !ok || p.SFTP != cfg {
		t.Fatalf("managed profile %+v, want %+v", p.SFTP, cfg)
	}
	src, err := source.NewSFTP(p.SFTP, source.SFTPCreds{Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := src.Open(ctx, "a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if string(b) != payload {
		t.Fatalf("read %q", b)
	}

	own := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(own, []byte("[profiles.erp]\nsource = \"local\"\npath = \"/srv\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.ReadFile(own); err == nil || !strings.Contains(err.Error(), `profile "erp" is provided by the company's managed configuration`) {
		t.Fatalf("a config profile of a managed name: %v", err)
	}
}
