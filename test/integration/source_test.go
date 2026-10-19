//go:build integration

// Integration tests of the S3 and SFTP sources against real protocol
// servers run in-process (an S3 fake over HTTP, an SSH server with the SFTP
// subsystem), with synthetic objects only.
package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/DavidGodefroid/lockfile/internal/source"
)

const (
	user    = "reader"
	pass    = "reader-test-secret"
	payload = "%PDF-1.7 synthetic integration object"
)

func TestS3StatOpen(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	defer srv.Close()
	admin := s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test-secret", ""),
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
	})
	if _, err := admin.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("documents")}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("documents"), Key: aws.String("invoices/2026/a.pdf"),
		Body: bytes.NewReader([]byte(payload)),
	}); err != nil {
		t.Fatal(err)
	}

	src, err := source.NewS3(source.S3Config{Bucket: "documents", Region: "us-east-1", Endpoint: srv.URL, Prefix: "invoices/"},
		source.S3Creds{AccessKeyID: "test", SecretAccessKey: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := src.Stat(ctx, "invoices/2026/a.pdf")
	if err != nil || fi.Size != int64(len(payload)) {
		t.Fatalf("Stat = %+v, %v", fi, err)
	}
	rc, err := src.Open(ctx, "invoices/2026/a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != payload {
		t.Fatalf("read %q", b)
	}
	if _, err := src.Stat(ctx, "invoices/2026/missing.pdf"); !errors.Is(err, source.ErrSource) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := src.Stat(ctx, "other/a.pdf"); !errors.Is(err, source.ErrKeyOutsidePrefix) {
		t.Fatalf("outside prefix: %v", err)
	}
}

// sftpServer serves root over SFTP for password user/pass, and returns its
// address and host key.
func sftpServer(t *testing.T, root string) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(p) == pass {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(nc, cfg, root)
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}

func serveConn(nc net.Conn, cfg *ssh.ServerConfig, root string) {
	defer nc.Close()
	_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, in, err := nch.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				req.Reply(ok, nil)
				if ok {
					s, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(root), sftp.ReadOnly())
					if err == nil {
						s.Serve()
					}
					ch.Close()
				}
			}
		}()
	}
}

func knownHostsFile(t *testing.T, addr string, key ssh.PublicKey) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "known_hosts")
	var content []byte
	if key != nil {
		content = []byte(knownhosts.Line([]string{knownhosts.Normalize(addr)}, key) + "\n")
	}
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func sftpCfg(t *testing.T, addr, knownHosts, baseDir string) source.SFTPConfig {
	host, port, _ := net.SplitHostPort(addr)
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return source.SFTPConfig{Host: host, Port: p, User: user, BaseDir: baseDir, KnownHosts: knownHosts}
}

func TestSFTPStatOpen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "outgoing"), 0o700)
	os.WriteFile(filepath.Join(root, "outgoing", "a.pdf"), []byte(payload), 0o600)
	addr, key := sftpServer(t, root)
	src, err := source.NewSFTP(sftpCfg(t, addr, knownHostsFile(t, addr, key), filepath.Join(root, "outgoing")),
		source.SFTPCreds{Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := src.Stat(ctx, "a.pdf")
	if err != nil || fi.Size != int64(len(payload)) {
		t.Fatalf("Stat = %+v, %v", fi, err)
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
	if _, err := src.Stat(ctx, "missing.pdf"); !errors.Is(err, source.ErrSource) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := src.Stat(ctx, "../a.pdf"); !errors.Is(err, source.ErrKeyOutsidePrefix) {
		t.Fatalf("escape: %v", err)
	}
}

func TestSFTPUnknownHostRefused(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.pdf"), []byte(payload), 0o600)
	addr, _ := sftpServer(t, root)
	src, err := source.NewSFTP(sftpCfg(t, addr, knownHostsFile(t, addr, nil), root), source.SFTPCreds{Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Stat(context.Background(), "a.pdf"); !errors.Is(err, source.ErrSource) {
		t.Fatalf("unknown host accepted: %v", err)
	}
}

func TestSFTPWrongHostKeyRefused(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.pdf"), []byte(payload), 0o600)
	addr, _ := sftpServer(t, root)
	_, otherAddrKey := sftpServer(t, root)
	src, err := source.NewSFTP(sftpCfg(t, addr, knownHostsFile(t, addr, otherAddrKey), root), source.SFTPCreds{Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Stat(context.Background(), "a.pdf"); !errors.Is(err, source.ErrSource) {
		t.Fatalf("wrong host key accepted: %v", err)
	}
}
